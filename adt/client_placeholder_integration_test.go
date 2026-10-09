//go:build integration

package adt_test

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// refusingTransport answers every request with a synthetic 401 and counts the
// requests, without ever opening a connection. It stands in for SAP where the
// test must prove a request does NOT go out: if the placeholder check were
// removed, the wrong credential would reach this transport and not the real
// system, so the test can fail without counting a failed logon against a real
// SAP user.
type refusingTransport struct{ requests atomic.Int32 }

func (rt *refusingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.requests.Add(1)
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Status:     "401 Unauthorized",
		Header:     http.Header{},
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

// TestUnresolvedPlaceholder_Integration checks two things per system:
//
//  1. The real credentials from the configuration pass the placeholder check
//     (one ordinary authenticated read). A false positive on a genuine secret
//     is the risk a live system can show and a mock cannot.
//  2. A client whose password is an unresolved placeholder refuses the request
//     before sending anything. This part never contacts the system, see
//     refusingTransport.
//
// Run with:
//
//	SAP_INTEGRATION_SYSTEMS="<r3-key>,<s4-key>" \
//	  go test -tags=integration -v -run TestUnresolvedPlaceholder_Integration ./adt/...
func TestUnresolvedPlaceholder_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			// A search needs no named object, so the read succeeds on any
			// system whose credentials are accepted, whatever it contains.
			if _, err := sys.Client.SearchObjects(ctx, "Z*", "", 1); err != nil {
				t.Fatalf("a read with the configured credentials failed: %v", err)
			}

			cfg := sys.Config
			cfg.Password = "${env:SYSTEM_PASSWORD}"
			rt := &refusingTransport{}
			client := adt.NewClientWithTransport(cfg, rt)

			_, err := client.SearchObjects(ctx, "Z*", "", 1)

			if !errors.Is(err, adt.ErrUnresolvedPlaceholder) {
				t.Fatalf("error = %v, want one matching ErrUnresolvedPlaceholder", err)
			}
			if got := rt.requests.Load(); got != 0 {
				t.Errorf("transport received %d request(s), want 0", got)
			}
		})
	}
}
