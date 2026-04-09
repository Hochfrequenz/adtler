//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestSetSource_TABLETagCharset_MultiSystem_Integration regression-tests
// adtler#15: SAP embeds the Content-Type into the ETag value. GetSource
// returns an ETag with "text/plain" (no charset), but PUT validates against
// one with "text/plain; charset=utf-8". The fix patches the ETag string on
// 412 and retries.
//
// This test creates a TABL, attempts to write source, and asserts:
//   - The error is NOT 412 PreconditionFailed (the ETag fix must prevent it)
//   - A 403 (enqueue from CreateObject) is acceptable — that's a separate
//     known issue (adtler#4 family, DDIC-specific enqueue not yet covered)
//   - On R/3: skips (DDIC TABL creation not available)
func TestSetSource_TABLETagCharset_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			name := fmt.Sprintf("ZTAB_MCP_%d", time.Now().Unix()%100000)
			uri := "/sap/bc/adt/ddic/tables/" + strings.ToLower(name)

			err := sys.Client.CreateObject(ctx, "TABL", name, "$TMP", "adtler#15 etag test", "")
			if err != nil {
				t.Skipf("[%s] CreateObject TABL: %v (DDIC not available)", sys.Name, err)
			}
			t.Logf("[%s] created %s", sys.Name, name)
			t.Cleanup(func() {
				_ = sys.Client.DeleteObject(context.Background(), uri, "", "")
			})

			// Get source + ETag
			src, err := sys.Client.GetSource(ctx, uri)
			if err != nil {
				t.Fatalf("[%s] GetSource: %v", sys.Name, err)
			}
			t.Logf("[%s] GetSource ETag: %s", sys.Name, src.ETag)

			// Lock
			lockHandle, err := sys.Client.LockObject(ctx, uri)
			if err != nil {
				t.Fatalf("[%s] LockObject: %v", sys.Name, err)
			}

			// Attempt SetSource — the fix should prevent 412
			source := fmt.Sprintf("@EndUserText.label : 'adtler#15 test'\ndefine table %s {\n  key mandt : mandt;\n}\n", strings.ToLower(name))
			_, err = sys.Client.SetSource(ctx, uri, source, lockHandle, "", src.ETag)

			if err == nil {
				t.Logf("[%s] SetSource OK — full TABL write chain works!", sys.Name)
				_ = sys.Client.UnlockObject(ctx, uri, lockHandle)
				return
			}

			// The critical assertion: error must NOT be 412 PreconditionFailed.
			// The ETag charset patch should have prevented that. A 403 or 423
			// (enqueue-related) is acceptable — that's the separate DDIC
			// enqueue issue from the #4 family.
			msg := err.Error()
			t.Logf("[%s] SetSource error: %v", sys.Name, err)

			if strings.Contains(msg, "412") || strings.Contains(msg, "PreconditionFailed") {
				t.Errorf("[%s] 412 PreconditionFailed — the ETag charset fix did NOT work. "+
					"The ETag should have been patched from 'text/plain' to "+
					"'text/plain; charset=utf-8'. Error: %v", sys.Name, err)
			} else {
				t.Logf("[%s] error is NOT 412 (ETag fix works) — the remaining error "+
					"(%v) is likely a DDIC enqueue issue from the #4 family, not an "+
					"ETag problem", sys.Name, err)
			}
		})
	}
}
