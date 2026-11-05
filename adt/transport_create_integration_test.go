//go:build integration && transport

package adt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestCreateTransport_Integration creates one request of each type on every
// configured system and reads back from E070 what SAP actually recorded
// (#137).
//
// A workbench request goes to POST /sap/bc/adt/cts/transports, whose handler
// reads only package and short text and takes the target from the package's
// transport layer. Before #137 adtler also sent a category and a target
// there; SAP dropped both without an error and created a K request whatever
// was asked for. Customizing requests and transports of copies go to
// POST /sap/bc/adt/cts/transportrequests, which only S/4 offers.
//
// Whether a system must support the typed create is derived from its
// SAP_BASIS release, not from the gate under test: a gate that wrongly
// refused would otherwise pass as "not supported". Only SAP_BASIS 750
// (refuses) and 816 (supports) were measured; releases in between may go
// either way.
func TestCreateTransport_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			c := sys.Client
			release := sapBasisRelease(t, c)

			// 1. Workbench request: works on every release.
			wb := createAndCleanup(t, c, adt.CreateTransportOptions{
				Description: "adtler #137 " + timestampedName("K"),
				Package:     testPackage,
			})
			fn, target := readE070(t, c, wb.number)
			if fn != "K" {
				t.Errorf("[1] TRFUNCTION: got %q, want K", fn)
			}
			t.Logf("[1] workbench request created; target taken from the package: set=%t", target != "")
			info, err := c.GetTransportInfo(ctx, wb.number)
			if err != nil {
				t.Fatalf("[1] GetTransportInfo: %v", err)
			}
			if info.Description != wb.opts.Description {
				t.Errorf("[1] short text: got %q, want %q", info.Description, wb.opts.Description)
			}
			if info.State != adt.TransportStateModifiable {
				t.Errorf("[1] state: got %v (raw %q), want modifiable", info.State, info.RawStatus)
			}

			// 2. Customizing request and transport of copies. The copy target
			// is the one SAP gave the workbench request, so it exists on
			// this system without being named here.
			for _, opts := range []adt.CreateTransportOptions{
				{Type: adt.TransportTypeCustomizing, Description: "adtler #137 " + timestampedName("W")},
				{Type: adt.TransportTypeCopies, Description: "adtler #137 " + timestampedName("T"), Target: target},
			} {
				number, err := c.CreateTransport(ctx, opts)
				if adt.ClassifyError(err) == adt.ErrorNotSupported {
					if release >= 816 {
						t.Fatalf("[2] %s refused as not supported on SAP_BASIS %d, which has the typed create: %v", opts.Type, release, err)
					}
					t.Logf("[2] %s refused as not supported (SAP_BASIS %d)", opts.Type, release)
					continue
				}
				if err != nil {
					t.Fatalf("[2] CreateTransport(%s): %v", opts.Type, err)
				}
				t.Cleanup(func() { deleteTransportAfterTest(t, c, number) })
				if release <= 750 {
					t.Errorf("[2] %s accepted on SAP_BASIS %d, which has no typed create", opts.Type, release)
				}
				gotFn, gotTarget := readE070(t, c, number)
				if gotFn != string(opts.Type) {
					t.Errorf("[2] TRFUNCTION: got %q, want %s", gotFn, opts.Type)
				}
				if opts.Target != "" && gotTarget != opts.Target {
					t.Errorf("[2] %s: target not taken over (set=%t)", opts.Type, gotTarget != "")
				}
				t.Logf("[2] %s created with TRFUNCTION %s", opts.Type, gotFn)
			}
		})
	}
}

type createdRequest struct {
	number string
	opts   adt.CreateTransportOptions
}

// createAndCleanup creates a request and deletes it again when the test ends.
func createAndCleanup(t *testing.T, c adt.Client, opts adt.CreateTransportOptions) createdRequest {
	t.Helper()
	number, err := c.CreateTransport(context.Background(), opts)
	if err != nil {
		t.Fatalf("CreateTransport(%q): %v", opts.Description, err)
	}
	t.Cleanup(func() { deleteTransportAfterTest(t, c, number) })
	return createdRequest{number: number, opts: opts}
}

func deleteTransportAfterTest(t *testing.T, c adt.Client, number string) {
	if err := c.DeleteTransport(context.Background(), number); err != nil {
		t.Logf("[cleanup] delete request: %v (manual cleanup may be needed)", err)
	}
}

// readE070 returns TRFUNCTION and TARSYSTEM of a request header.
func readE070(t *testing.T, c adt.Client, number string) (trfunction, tarsystem string) {
	t.Helper()
	res, err := c.RunQuery(context.Background(),
		"SELECT trfunction, tarsystem FROM e070 WHERE trkorr = '"+number+"'", 1)
	if err != nil {
		t.Fatalf("E070 query: %v", err)
	}
	if len(res.Rows) != 1 || len(res.Rows[0]) != 2 {
		t.Fatalf("E070: want one row with two columns, got %d rows", len(res.Rows))
	}
	return strings.TrimSpace(res.Rows[0][0]), strings.TrimSpace(res.Rows[0][1])
}
