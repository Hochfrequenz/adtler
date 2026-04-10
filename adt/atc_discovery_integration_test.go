//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestRunATCCheck_DiscoveryDriven_Integration verifies that after the
// discovery-first content-negotiation refactor (adtler#35), RunATCCheck
// still succeeds end-to-end against both R/3 and S/4.
//
// Historically RunATCCheck on R/3 returned HTTP 500 because the client sent
// a hardcoded Content-Type that R/3 did not accept (adtler#12). With the
// discovery-driven transport, the Content-Type is negotiated from the ATC
// collection's advertised accept types, which should make the call succeed
// on R/3 as well as S/4.
//
// Uses eachSystem so a single test function validates behaviour against
// every whitelisted SAP system (R/3 and S/4) in one run. On R/3 this may
// close adtler#12; on S/4 it is a regression guard.
func TestRunATCCheck_DiscoveryDriven_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Unique name per system + per run to avoid collisions with
			// leftover objects from earlier aborted runs.
			name := fmt.Sprintf("Z_ADT_MCP_ATC_%d", time.Now().UnixNano()%100000)
			uri := "/sap/bc/adt/programs/programs/" + name

			// 1. Create a disposable $TMP program to point ATC at.
			if err := sys.Client.CreateObject(ctx, "PROG", name, "$TMP",
				"adtler#35 discovery-driven ATC integration test", ""); err != nil {
				t.Fatalf("[%s] CreateObject: %v", sys.Name, err)
			}
			t.Logf("[%s] created %s", sys.Name, name)

			// Best-effort cleanup — re-lock then delete. Uses a fresh context
			// so cleanup still runs if the test context is cancelled.
			t.Cleanup(func() {
				lh, lockErr := sys.Client.LockObject(context.Background(), uri)
				if lockErr != nil {
					t.Logf("[%s] cleanup lock failed: %v", sys.Name, lockErr)
					return
				}
				if delErr := sys.Client.DeleteObject(context.Background(), uri, lh, ""); delErr != nil {
					t.Logf("[%s] cleanup delete failed: %v", sys.Name, delErr)
				}
			})

			// 2. RunATCCheck — exercises the discovery-driven Content-Type
			//    negotiation for the /sap/bc/adt/atc/runs POST. Any regression
			//    or R/3-specific MIME mismatch will surface as HTTP 500/415.
			result, err := sys.Client.RunATCCheck(ctx, []string{uri}, "DEFAULT")
			if err != nil {
				t.Fatalf("[%s] RunATCCheck: %v", sys.Name, err)
			}
			if result == nil {
				t.Fatalf("[%s] RunATCCheck returned nil result", sys.Name)
			}

			// ATC results are variant-dependent — don't assert a count, just
			// log it so the test output shows the discovery-driven call
			// really reached SAP and returned a parsed worklist.
			t.Logf("[%s] RunATCCheck OK: worklist=%q, %d findings",
				sys.Name, result.WorklistID, len(result.Findings))
		})
	}
}
