//go:build integration

package adt

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestGetTransportRequestsViaQuery_Integration runs the E070 read of
// GetTransportRequests directly, on every configured system, for each filter
// that adds a TRSTATUS condition (#274), and checks that every row it returns
// is in the filter's state. It also runs the DESCENDING sort on both
// releases, which reject the DESC abbreviation.
//
// It reads the first page of every user's requests and logs counts per state
// only.
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
				page, err := sys.c.getTransportRequestsViaQuery(context.Background(), "", tc.filter, MaxTransportPageSize, nil)
				if err != nil {
					t.Fatalf("E070 read with the %v condition failed: %v", tc.filter, err)
				}
				counts := map[string]int{}
				for _, tr := range page.Requests {
					counts[tr.State.String()+"/"+tr.RawStatus]++
					if tr.State != tc.want {
						t.Errorf("E070 read for %v returned a request in state %v (raw %q)", tc.filter, tr.State, tr.RawStatus)
					}
				}
				t.Logf("filter %v: %d request(s), more: %v, state/raw counts %v",
					tc.filter, len(page.Requests), page.Next != "", counts)
				if len(page.Requests) == 0 {
					t.Skipf("no request in state %v on this system", tc.want)
				}
			})
		}
	}
}

// TestGetTransportRequests_Paging_Integration walks every page of the logon
// user's requests with a small page size and checks the pages against one
// read of the whole list: same requests, same order (newest first), none
// twice, none lost. A small page size makes page boundaries fall between
// requests changed at the same date and time, so the cursor's tie-breaking
// on the request number is exercised too.
//
// Before #274 there was no paging, and before E070 was read on every call the
// list was partial (#242 point 1); the comparison catches both. It logs
// counts only.
func TestGetTransportRequests_Paging_Integration(t *testing.T) {
	const pageSize = 7
	for _, sys := range issue63Systems(t) {
		sys := sys
		user := strings.ToUpper(sys.c.cfg.User)
		for _, filter := range []TransportFilter{TransportFilterAll, TransportFilterModifiable, TransportFilterReleased} {
			t.Run(sys.name+"/"+filter.String(), func(t *testing.T) {
				ctx := context.Background()
				whole, err := sys.c.getTransportRequestsViaQuery(ctx, user, filter, MaxTransportPageSize, nil)
				if err != nil {
					t.Fatalf("E070 read failed: %v", err)
				}
				if whole.Next != "" {
					t.Skipf("the logon user owns more than %d requests for the %v filter", MaxTransportPageSize, filter)
				}
				if len(whole.Requests) == 0 {
					t.Skipf("the logon user owns no request for the %v filter", filter)
				}

				var walked []string
				described, pages := 0, 0
				opts := TransportListOptions{Filter: filter, Limit: pageSize}
				for {
					page, err := sys.c.GetTransportRequests(ctx, opts)
					if err != nil {
						t.Fatalf("page %d: %v", pages+1, err)
					}
					pages++
					for _, tr := range page.Requests {
						walked = append(walked, tr.Number)
						if tr.Description != "" {
							described++
						}
					}
					if page.Next == "" {
						break
					}
					if pages > len(whole.Requests) {
						t.Fatalf("still paging after %d pages for %d requests", pages, len(whole.Requests))
					}
					opts.After = page.Next
				}

				want := make([]string, len(whole.Requests))
				for i, tr := range whole.Requests {
					want[i] = tr.Number
				}
				t.Logf("filter %v: %d request(s) in %d page(s), %d with description",
					filter, len(walked), pages, described)
				if !reflect.DeepEqual(walked, want) {
					t.Fatalf("the pages list %d request(s), the whole read %d; they differ in content or order",
						len(walked), len(want))
				}
			})
		}
	}
}
