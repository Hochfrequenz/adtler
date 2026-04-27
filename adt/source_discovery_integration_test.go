//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSourceOperations_DiscoveryDriven_Integration verifies that after the
// discovery-first content-negotiation refactor (adtler#35), source
// operations still succeed end-to-end against both R/3 and S/4.
//
// The test exercises the full Lock -> GetSource -> SetSource -> Unlock cycle
// on a freshly created $TMP PROG object. Any regression in Accept /
// Content-Type wiring will surface as 400/406/415/412 from SAP.
//
// Uses eachSystem so a single test function validates behaviour against
// every whitelisted SAP system (R/3 and S/4) in one run.
func TestSourceOperations_DiscoveryDriven_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Unique name per system + per run to avoid collisions with
			// leftover objects from earlier aborted runs.
			name := fmt.Sprintf("Z_ADT_MCP_DISC_%d", time.Now().UnixNano()%100000)
			uri := "/sap/bc/adt/programs/programs/" + name

			// 1. Create a disposable $TMP program.
			if err := sys.Client.CreateObject(ctx, "PROG", name, "$TMP",
				"adtler#35 discovery-driven source-ops integration test", ""); err != nil {
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

			// 2. Lock — discovery-driven transport should negotiate the correct
			//    Accept header for the lock endpoint on both R/3 and S/4.
			lockHandle, err := sys.Client.LockObject(ctx, uri)
			if err != nil {
				t.Fatalf("[%s] LockObject: %v", sys.Name, err)
			}
			defer func() {
				// Final unlock as safety net in case SetSource fails mid-test.
				// The happy path unlocks below; this is idempotent-enough.
				if err := sys.Client.UnlockObject(ctx, uri, lockHandle); err != nil {
					t.Logf("[%s] deferred UnlockObject: %v", sys.Name, err)
				}
			}()

			// 3. GetSource — exercises source read content negotiation.
			result, err := sys.Client.GetSource(ctx, uri)
			if err != nil {
				t.Fatalf("[%s] GetSource: %v", sys.Name, err)
			}
			if result.Source == "" {
				t.Errorf("[%s] GetSource returned empty source", sys.Name)
			}
			if result.ETag == "" {
				t.Errorf("[%s] GetSource returned empty ETag", sys.Name)
			}

			// 4. SetSource — exercises source write content negotiation and
			//    ETag round-trip. Appends a probe comment so we can see in SAP
			//    that the refactored client really wrote source.
			newSource := strings.TrimRight(result.Source, "\n") +
				"\n* discovery-refactor-probe " + sys.Name + "\n"
			newETag, err := sys.Client.SetSource(ctx, uri, newSource, lockHandle, "", result.ETag)
			if err != nil {
				t.Fatalf("[%s] SetSource: %v", sys.Name, err)
			}
			if newETag == "" {
				t.Errorf("[%s] SetSource returned empty ETag", sys.Name)
			}
			t.Logf("[%s] full cycle OK: lock -> get -> set -> (deferred unlock)", sys.Name)
		})
	}
}
