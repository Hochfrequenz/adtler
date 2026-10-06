//go:build integration

package adt_test

import (
	"context"
	"sync"
	"testing"
)

// TestLogout_ConcurrentReads_Integration runs Logout while other reads are in
// flight on the same client, the overlap aibap.mcp produces when one tool call
// runs CreateObject (which logs out) and another reads (issue #191). Before the
// fix Logout reassigned the cookie jar under net/http's feet; that is a data
// race, so run this with -race to have it guard the fix:
//
//	SAP_INTEGRATION_SYSTEMS="<r3-key>,<s4-key>" \
//	  go test -race -tags=integration -v -run TestLogout_ConcurrentReads_Integration ./adt/...
//
// Without -race it still checks that reads overlapping a Logout succeed and
// that the client opens a working session again afterwards.
func TestLogout_ConcurrentReads_Integration(t *testing.T) {
	const (
		rounds   = 3
		readers  = 3
		reportID = "/sap/bc/adt/programs/programs/RSPARAM" // standard SAP report, present on every release
	)
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			if _, err := sys.Client.GetObjectInfo(ctx, reportID); err != nil {
				t.Fatalf("warm-up read: %v", err)
			}
			for i := 0; i < rounds; i++ {
				var wg sync.WaitGroup
				begin := make(chan struct{})
				wg.Add(readers + 1)
				go func() {
					defer wg.Done()
					<-begin
					if err := sys.Client.Logout(ctx); err != nil {
						t.Errorf("round %d: Logout: %v", i, err)
					}
				}()
				for r := 0; r < readers; r++ {
					go func() {
						defer wg.Done()
						<-begin
						if _, err := sys.Client.GetObjectInfo(ctx, reportID); err != nil {
							t.Errorf("round %d: read overlapping Logout: %v", i, err)
						}
					}()
				}
				close(begin)
				wg.Wait()
			}
			if _, err := sys.Client.GetObjectInfo(ctx, reportID); err != nil {
				t.Errorf("read after the last Logout: %v", err)
			}
		})
	}
}
