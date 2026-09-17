//go:build integration

// Integration test for adtler#125
// (https://github.com/Hochfrequenz/adtler/issues/125): confirm that
// RemoveFromTransport's capability gate (ensureRemoveObjectSupported) blocks
// the write on the ECC system with the typed
// adt.ExceptionTypeRemoveObjectUnsupported error, using the real fixture
// arguments from https://github.com/Hochfrequenz/aibap.mcp/issues/493.
//
// SAFETY: this test wraps the real client in a RoundTripper that refuses any
// PUT to the transportrequests endpoint before the request can hit SAP. The
// HFQ name check only selects the live ECC fixture system whose transport/task
// numbers below are known to exist; it is not the safety boundary.
package adt_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRemoveFromTransport_ECCGateBlocksWrite_Integration is the removeobject
// capability gate's regression test. It runs RemoveFromTransport against the
// ECC system only, using the exact task/parent/object/wbtype/position from
// aibap.mcp#493, and asserts on the typed error the gate
// (ensureRemoveObjectSupported) returns — never on any side effect on the SAP
// side, since none is expected either way.
func TestRemoveFromTransport_ECCGateBlocksWrite_Integration(t *testing.T) {
	// taskNumber/parentNumber/objectName are live, environment-specific
	// identifiers that must already exist on the real ECC system this test
	// runs against — a synthetic value would not correspond to anything
	// there. They are the same reproducer arguments aibap.mcp#493 recorded.
	const (
		taskNumber   = "HFQK902953"
		parentNumber = "HFQK902952"
		pgmID        = "R3TR"
		objectType   = "CLAS"
		objectName   = "ZCL_LOCKREPRO_2"
		wbType       = "CLAS/OC"
		position     = "000001"
	)

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			if sys.Name != "HFQ" {
				t.Skipf("this test only ever runs against the ECC system — never against the S/4 system; got %q", sys.Name)
			}
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.TLSClientConfig = &tls.Config{InsecureSkipVerify: sys.Config.TLSSkipVerify} //nolint:gosec
			t.Cleanup(base.CloseIdleConnections)
			var putCount atomic.Int32
			client := adt.NewClientWithTransport(sys.Config, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut && req.URL.Path == "/sap/bc/adt/cts/transportrequests/"+taskNumber {
					putCount.Add(1)
					return nil, errors.New("blocked transport write in integration test")
				}
				return base.RoundTrip(req)
			}))

			ctx := context.Background()
			err := client.RemoveFromTransport(ctx, taskNumber, parentNumber, pgmID, objectType, objectName, wbType, position)
			if err == nil {
				t.Fatal("expected RemoveFromTransport to be blocked by the removeobject capability gate, got nil error")
			}
			t.Logf("%s: RemoveFromTransport returned (expected): %v", sys.Name, err)
			if got := putCount.Load(); got != 0 {
				t.Fatalf("integration safety wrapper blocked %d unexpected transport PUT(s); the gate must stop the write before it is attempted", got)
			}

			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
			}
			if adtErr.Type != adt.ExceptionTypeRemoveObjectUnsupported {
				t.Fatalf("expected Type %q, got %q (message: %s)",
					adt.ExceptionTypeRemoveObjectUnsupported, adtErr.Type, adtErr.Message)
			}
			t.Logf("%s: gate error text: %s", sys.Name, adtErr.Error())
		})
	}
}
