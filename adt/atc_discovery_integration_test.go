//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestRunATCCheck_DiscoveryDriven_Integration verifies that RunATCCheck
// succeeds end-to-end against both R/3 and S/4 using the canonical 3-step
// Eclipse-style flow:
//
//  1. POST /sap/bc/adt/atc/worklists?checkVariant=DEFAULT (text/plain)
//  2. POST /sap/bc/adt/atc/runs?worklistId={id}            (XML body)
//  3. GET  /sap/bc/adt/atc/worklists/{id}                  (findings)
//
// Historically RunATCCheck on R/3 returned HTTP 500 with empty body
// (adtler#12). The earlier discovery-first content-negotiation refactor
// (adtler#35) did not fix this. Root cause: the previous 2-step shortcut
// posted /runs with a placeholder worklistId="0000000000" that R/3 does
// not accept. The 3-step shape was observed in the abap-adt-api reference
// implementation; this integration test verifies it on both R/3 and S/4.
//
// Uses eachSystem so a single test function validates behaviour against
// every whitelisted SAP system (R/3 and S/4) in one run. Closes adtler#12
// when green on R/3.
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
