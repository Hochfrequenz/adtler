//go:build integration

package adt

import (
	"context"
	"testing"
)

// TestGetTransportRequestsViaQuery_Integration runs the E070 fallback of
// GetTransportRequests directly, on every configured system, for each filter
// that adds a TRSTATUS condition (#274). Through GetTransportRequests the
// fallback runs only where the tree yields nothing that passes the filter,
// which on ECC is rare, so without this test the condition would reach ECC's
// data preview endpoint for the first time in a consumer's call.
//
// It lists without a user and logs counts per state only.
func TestGetTransportRequestsViaQuery_Integration(t *testing.T) {
	cases := []struct {
		filter TransportFilter
		want   TransportState
	}{
		{TransportFilterModifiable, TransportStateModifiable},
		{TransportFilterReleased, TransportStateReleased},
	}
	for _, sys := range issue63Systems(t) {
		sys := sys
		for _, tc := range cases {
			t.Run(sys.name+"/"+tc.filter.String(), func(t *testing.T) {
				requests, err := sys.c.getTransportRequestsViaQuery(context.Background(), "", tc.filter)
				if err != nil {
					t.Fatalf("E070 fallback with the %v condition failed: %v", tc.filter, err)
				}
				counts := map[string]int{}
				for _, tr := range requests {
					counts[tr.State.String()+"/"+tr.RawStatus]++
					if tr.State != tc.want {
						t.Errorf("fallback for %v returned a request in state %v (raw %q)", tc.filter, tr.State, tr.RawStatus)
					}
				}
				t.Logf("filter %v: %d request(s), state/raw counts %v", tc.filter, len(requests), counts)
				if len(requests) == 0 {
					t.Skipf("no request in state %v on this system", tc.want)
				}
			})
		}
	}
}
