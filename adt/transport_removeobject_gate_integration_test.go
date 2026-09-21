//go:build integration

// Integration test for adtler#125
// (https://github.com/Hochfrequenz/adtler/issues/125): confirm, against every
// configured system, that RemoveFromTransport's capability gate
// (ensureRemoveObjectSupported) behaves the way the system's advertised
// capability says it should.
//
// SAFETY, two independent layers, in this order:
//
//  1. The client is wrapped in a RoundTripper that refuses every PUT and
//     DELETE before it can leave the process. That is the boundary. It holds
//     on every system, whatever the gate decides and whatever this test
//     asserts, and it is what makes exercising the supported branch safe.
//  2. The object coordinates are synthetic. Nothing on any SAP system is
//     addressed by them, so the test depends on no environment-specific object
//     existing anywhere and cannot be broken by someone tidying a fixture away.
//
// Neither the capability gate nor the system's configured name is a safety
// boundary here, and neither is relied on as one.
//
// The full create/remove/verify lifecycle — which does write — lives in
// transport_remove_integration_test.go behind the `integration && transport`
// build tag.
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

// errBlockedWrite is what this test's RoundTripper returns instead of letting a
// PUT or DELETE reach SAP. Seeing it means the gate let the call through, which
// is the correct outcome on a system that advertises the capability.
var errBlockedWrite = errors.New("transport write blocked inside the integration test")

// TestRemoveFromTransport_Gate_Integration resolves each configured system's
// removeobject capability from the system itself — never from its configured
// name — and asserts the branch that capability selects:
//
//   - Unsupported: RemoveFromTransport returns an *adt.ADTError of type
//     adt.ExceptionTypeRemoveObjectUnsupported and issues no request at all.
//   - Supported: the gate lets the call through, so a PUT is attempted and this
//     test's own RoundTripper is what stops it.
//
// A capability still Unknown after a successful transport read is a failure,
// not a skip: the gate deliberately fails open on Unknown, so that state would
// send the very request this test exists to characterise.
func TestRemoveFromTransport_Gate_Integration(t *testing.T) {
	// Synthetic coordinates — see the SAFETY note above. They only have to
	// satisfy the library's own transport-number validation.
	const (
		taskNumber   = "DEVK900124"
		parentNumber = "DEVK900123"
		pgmID        = "R3TR"
		objectType   = "PROG"
		objectName   = "Z_ADT_MCP_GATE_NOOP"
		wbType       = "PROG/P"
		position     = "000001"
	)

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.TLSClientConfig = &tls.Config{InsecureSkipVerify: sys.Config.TLSSkipVerify} //nolint:gosec
			t.Cleanup(base.CloseIdleConnections)

			var writeCount atomic.Int32
			client := adt.NewClientWithTransport(sys.Config, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodPut || req.Method == http.MethodDelete {
					writeCount.Add(1)
					return nil, errBlockedWrite
				}
				return base.RoundTrip(req)
			}))

			ctx := context.Background()

			// Warm the capability. It is a cached side effect of any successful
			// transport read (see cacheRemoveObjectSupport), so this read is
			// what populates it — RemoveFromTransport must not be the call that
			// discovers it, or an unclassified system would be probed by the
			// write itself.
			reqs, err := client.GetTransportRequests(ctx, "", adt.TransportStatusModifiable)
			if err != nil {
				t.Fatalf("GetTransportRequests: %v", err)
			}
			if len(reqs) == 0 {
				t.Skip("no modifiable transport request on this system to read the capability from")
			}
			if _, err := client.GetTransportObjects(ctx, reqs[0].Number); err != nil {
				t.Fatalf("GetTransportObjects(%s): %v", reqs[0].Number, err)
			}

			tc, ok := client.(adt.TestClient)
			if !ok {
				t.Fatal("client does not expose the test-only capability accessor")
			}
			support := tc.RemoveObjectSupportForTest()

			err = client.RemoveFromTransport(ctx, taskNumber, parentNumber, pgmID, objectType, objectName, wbType, position)

			switch support {
			case adt.RemoveObjectSupportUnsupported:
				var adtErr *adt.ADTError
				if !errors.As(err, &adtErr) {
					t.Fatalf("capability is Unsupported, so the gate should have returned an *adt.ADTError; got %[1]T: %[1]v", err)
				}
				if adtErr.Type != adt.ExceptionTypeRemoveObjectUnsupported {
					t.Errorf("error Type = %q, want %q", adtErr.Type, adt.ExceptionTypeRemoveObjectUnsupported)
				}
				if got := writeCount.Load(); got != 0 {
					t.Errorf("gate reported unsupported but %d write request(s) were attempted; the whole point is that none is sent", got)
				}
				if got := adt.ClassifyError(err); got != adt.ErrorNotSupported {
					t.Errorf("ClassifyError = %v, want %v — the consumer branches on this", got, adt.ErrorNotSupported)
				}

			case adt.RemoveObjectSupportSupported:
				if !errors.Is(err, errBlockedWrite) {
					t.Fatalf("capability is Supported, so the gate should have let the call reach a PUT this test blocks; got %v", err)
				}
				if got := writeCount.Load(); got != 1 {
					t.Errorf("attempted write requests = %d, want exactly 1", got)
				}

			default:
				// Unknown. The gate fails open by design, so this must behave
				// exactly like Supported: the call goes through and this test's
				// RoundTripper is what stops it. Unknown is not an anomaly —
				// which relations a server advertises depends on the state of
				// the transport that was read, so a system that supports
				// removal can legitimately leave the capability unresolved.
				if !errors.Is(err, errBlockedWrite) {
					t.Fatalf("capability is Unknown, so the gate must fail open and let the call reach a PUT this test blocks; got %v", err)
				}
				if got := writeCount.Load(); got != 1 {
					t.Errorf("attempted write requests = %d, want exactly 1 — an unclassified system must behave as it did before the gate existed", got)
				}
			}
		})
	}
}
