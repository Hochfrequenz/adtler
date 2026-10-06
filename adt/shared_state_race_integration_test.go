//go:build integration

package adt_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
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
// A read that SAP is still processing when the logoff ends its session comes
// back as a plain 500 Internal Server Error. Measured on both systems, with
// and without this fix, depending only on timing. That is SAP's answer, not a
// client fault, so an overlapping read may fail with a 500 and nothing else.
// What the client must guarantee is that the next read after each round, on
// the same client, succeeds again.
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
			var cutOff atomic.Int64
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
						_, err := sys.Client.GetObjectInfo(ctx, reportID)
						var adtErr *adt.ADTError
						switch {
						case err == nil:
						case errors.As(err, &adtErr) && adtErr.StatusCode == http.StatusInternalServerError:
							cutOff.Add(1)
						default:
							t.Errorf("round %d: read overlapping Logout: %v", i, err)
						}
					}()
				}
				close(begin)
				wg.Wait()
				if _, err := sys.Client.GetObjectInfo(ctx, reportID); err != nil {
					t.Errorf("round %d: read after Logout: %v", i, err)
				}
			}
			t.Logf("%d of %d overlapping reads were cut off by the logoff (500)", cutOff.Load(), rounds*readers)
		})
	}
}
