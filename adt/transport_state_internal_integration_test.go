//go:build integration

package adt

import (
	"context"
	"strings"
	"testing"
)

// TestGetTransportRequestsViaQuery_Integration runs the E070 read of
// GetTransportRequests directly, on every configured system, for each filter
// that adds a TRSTATUS condition (#274), and checks that every row it returns
// is in the filter's state.
//
// It lists every user's requests and logs counts per state only.
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

// TestGetTransportRequests_Complete_Integration guards #242 point 1 (#274):
// for every filter, each request E070 holds for the logon user must be in
// GetTransportRequests' result. Before E070 was read on every call, the
// fallback ran only when nothing passed the filter, so on S/4 the all filter
// returned the tree's few released requests and none of the modifiable ones,
// and on ECC none of the released ones.
//
// It logs counts only.
func TestGetTransportRequests_Complete_Integration(t *testing.T) {
	for _, sys := range issue63Systems(t) {
		sys := sys
		user := strings.ToUpper(sys.c.cfg.User)
		for _, filter := range []TransportFilter{TransportFilterAll, TransportFilterModifiable, TransportFilterReleased} {
			t.Run(sys.name+"/"+filter.String(), func(t *testing.T) {
				ctx := context.Background()
				fromE070, err := sys.c.getTransportRequestsViaQuery(ctx, user, filter)
				if err != nil {
					t.Fatalf("E070 read failed: %v", err)
				}
				listed, err := sys.c.GetTransportRequests(ctx, "", filter)
				if err != nil {
					t.Fatalf("GetTransportRequests(%v) failed: %v", filter, err)
				}
				have := map[string]bool{}
				described := 0
				for _, tr := range listed {
					have[tr.Number] = true
					if tr.Description != "" {
						described++
					}
				}
				missing := 0
				for _, tr := range fromE070 {
					if !have[tr.Number] {
						missing++
					}
				}
				t.Logf("filter %v: E070 %d, listed %d (%d with description), missing %d",
					filter, len(fromE070), len(listed), described, missing)
				if len(fromE070) == 0 {
					t.Skipf("the logon user owns no request for the %v filter", filter)
				}
				if missing > 0 {
					t.Fatalf("%d of %d requests E070 holds for the logon user are missing from the %v list",
						missing, len(fromE070), filter)
				}
			})
		}
	}
}
