//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestListShortDumps_RuntimeFields_MultiSystem_Integration regression-tests
// adtler#7 against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Before the fix, the S/4 ATOM feed's <category> labels didn't match the
// case-sensitive label check in parseShortDumpHeaders. After the fix, the
// matching is case-insensitive and should work on both R/3 (German labels)
// and S/4 (potentially English or differently-cased labels).
//
// The test calls ListShortDumps with a broad date range and asserts that
// at least one dump has RuntimeError AND Program populated. If a system
// has zero dumps in the window, the sub-test skips rather than fails.
func TestListShortDumps_RuntimeFields_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Broad window: last 30 days. Most active SAP systems have at
			// least a few dumps in this range.
			dumps, err := sys.Client.ListShortDumps(ctx, "20260301000000", "20260430000000", "")
			if err != nil {
				t.Fatalf("[%s] ListShortDumps: %v", sys.Name, err)
			}
			if len(dumps) == 0 {
				t.Skipf("[%s] no short dumps found in the 30-day window — can't validate field extraction", sys.Name)
			}
			t.Logf("[%s] found %d dumps in window", sys.Name, len(dumps))

			// Check the first 5 dumps (or fewer if less available).
			n := len(dumps)
			if n > 5 {
				n = 5
			}
			populatedCount := 0
			for i := 0; i < n; i++ {
				d := dumps[i]
				t.Logf("[%s] dump[%d]: RuntimeError=%q Program=%q User=%q Timestamp=%q",
					sys.Name, i, d.RuntimeError, d.Program, d.User, d.Timestamp)
				if d.RuntimeError != "" && d.Program != "" {
					populatedCount++
				}
			}

			if populatedCount == 0 {
				t.Errorf("[%s] none of the first %d dumps have RuntimeError AND Program populated — "+
					"the category label matching likely doesn't cover this system's label format. "+
					"Capture the raw ATOM XML <category> elements and update the label matchers.",
					sys.Name, n)
			} else {
				t.Logf("[%s] %d/%d dumps have RuntimeError + Program populated", sys.Name, populatedCount, n)
			}
		})
	}
}
