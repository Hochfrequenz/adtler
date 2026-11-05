//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

func TestCheckTransport_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	result, err := client.CheckTransport(ctx, "R3TR", "PROG", "Z_ADT_MCP_TEST_REPORT")
	if err != nil {
		t.Fatalf("CheckTransport failed: %v", err)
	}

	t.Logf("Result=%s Recording=%v DevClass=%s", result.Result, result.Recording, result.DevClass)
	t.Logf("Available transports: %d", len(result.Requests))
	for i, r := range result.Requests {
		if i >= 5 {
			break
		}
		t.Logf("  %s (%s) — %s", r.Number, r.RawStatus, r.Description)
	}

	if result.Result == "" {
		t.Error("expected non-empty Result")
	}
	if result.ObjectName != "Z_ADT_MCP_TEST_REPORT" {
		t.Errorf("ObjectName: got %q", result.ObjectName)
	}
}

func TestGetTransportRequests_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	transports, err := client.GetTransportRequests(ctx, "", adt.TransportFilterModifiable)
	if err != nil {
		t.Fatalf("GetTransportRequests failed: %v", err)
	}
	if len(transports) == 0 {
		t.Fatal("expected at least one modifiable transport request, got 0")
	}
	t.Logf("got %d modifiable transport requests", len(transports))

	for i, tr := range transports {
		if tr.Number == "" {
			t.Errorf("transport [%d]: Number is empty", i)
		}
		if tr.Owner == "" {
			t.Errorf("transport [%d]: Owner is empty", i)
		}
		if tr.State != adt.TransportStateModifiable {
			t.Errorf("transport [%d]: State is %v (raw %q), want modifiable", i, tr.State, tr.RawStatus)
		}
		if i < 5 {
			t.Logf("  [%d] %s owner=%s status=%s %q", i, tr.Number, tr.Owner, tr.RawStatus, tr.Description)
		}
	}
}

// TestGetTransportRequests_AllSystems_Integration exercises GetTransportRequests
// against every configured system via eachSystem. On S/4HANA systems with
// KORRDEV=SYST/CUST (issue #63) the E070 fallback path is triggered; on
// classic R/3 systems the ADT endpoint returns results directly.
func TestGetTransportRequests_AllSystems_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()

			transports, err := sys.Client.GetTransportRequests(ctx, "", adt.TransportFilterModifiable)
			if err != nil {
				t.Fatalf("GetTransportRequests failed: %v", err)
			}
			if len(transports) == 0 {
				t.Fatal("expected at least one modifiable transport, got 0")
			}
			t.Logf("%s: got %d modifiable transport requests", sys.Name, len(transports))
			for i, tr := range transports {
				if tr.Number == "" {
					t.Errorf("transport [%d]: Number is empty", i)
				}
				if i < 3 {
					t.Logf("  [%d] %s owner=%s status=%s %q", i, tr.Number, tr.Owner, tr.RawStatus, tr.Description)
				}
			}
		})
	}
}

// TestGetTransportRequests_StateFilter_Integration guards #270 and #274: each
// filter must return requests, and only requests in the state it names.
//
// Before #274 the client passed the filter to SAP as a single TRSTATUS letter
// and did not check the answer. The S/4 transport organizer tree ignores that
// parameter and returned the same released requests for every filter, so the
// modifiable case failed there; ECC filtered by the letter, so "R" missed
// requests released with import protection ("N").
//
// The test lists without a user: on ECC the configured user may own no
// released request, while the system as a whole has many. It logs counts per
// state only, never request numbers.
func TestGetTransportRequests_StateFilter_Integration(t *testing.T) {
	cases := []struct {
		filter adt.TransportFilter
		want   adt.TransportState
	}{
		{adt.TransportFilterModifiable, adt.TransportStateModifiable},
		{adt.TransportFilterReleased, adt.TransportStateReleased},
	}
	for _, sys := range eachSystem(t) {
		sys := sys
		for _, tc := range cases {
			t.Run(sys.Name+"/"+tc.filter.String(), func(t *testing.T) {
				ctx := context.Background()
				requests, err := sys.Client.GetTransportRequests(ctx, "", tc.filter)
				if err != nil {
					t.Fatalf("GetTransportRequests(%v) failed: %v", tc.filter, err)
				}
				states := map[string]int{}
				for _, tr := range requests {
					states[tr.State.String()+"/"+tr.RawStatus]++
				}
				t.Logf("filter %v: %d request(s), state/raw counts %v", tc.filter, len(requests), states)
				if len(requests) == 0 {
					t.Skipf("no request visible for the %v filter on this system", tc.filter)
				}
				for _, tr := range requests {
					if tr.State != tc.want {
						t.Fatalf("filter %v returned a request in state %v (raw %q); want only %v",
							tc.filter, tr.State, tr.RawStatus, tc.want)
					}
				}
			})
		}
	}
}
