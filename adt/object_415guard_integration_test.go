//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestCreateObject_DDICUnavailable_MultiSystem_Integration regression-tests
// adtler#16 against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// CreateObject's "DDIC creation requires S/4HANA" hint must fire for any
// older release that doesn't accept the v2 DDIC content type adtler sends —
// regardless of whether the server signals that with HTTP 404 (endpoint
// missing entirely) or HTTP 415 (endpoint exists but rejects the content
// type, which is what the heavy-test sweep observed for DTEL on R/3).
//
// The test asks each system to create a DDIC object that's likely to fail
// on R/3-style releases (DTEL, in $TMP, throwaway name) and asserts:
//
//   - On systems where DDIC create succeeds (S/4-class releases), the
//     test cleans up the object and reports success.
//   - On systems where DDIC create fails, the error MUST be the friendly
//     "is not available on this SAP system" hint — never the raw 404 or
//     415 SAP message body.
//
// A failure on either branch means the 415 guard has regressed.
func TestCreateObject_DDICUnavailable_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Suffix the test object name with a per-run timestamp to avoid
			// the stale-state flake the round-2 review flagged: a fixed name
			// would collide with leftover objects from a previous run whose
			// cleanup failed (e.g. lock contention), and the next run would
			// see "object already exists" instead of the expected 404/415 —
			// the guard wouldn't fire and the test would falsely fail.
			dtelName := fmt.Sprintf("Z_ADT_MCP_415_%d", time.Now().Unix()%100000)
			objectURI := "/sap/bc/adt/ddic/dataelements/" + strings.ToLower(dtelName)

			err := sys.Client.CreateObject(ctx, "DTEL", dtelName, "$TMP",
				"adtler#16 multi-system integration test", "")

			if err == nil {
				// DDIC create succeeded — must be an S/4-class release.
				// Clean up so the test is idempotent.
				t.Logf("[%s] DTEL create succeeded — system supports DDIC ADT REST", sys.Name)
				t.Cleanup(func() {
					if delErr := sys.Client.DeleteObject(context.Background(), objectURI, "", ""); delErr != nil {
						t.Logf("[%s] cleanup of %s failed: %v", sys.Name, dtelName, delErr)
					}
				})
				return
			}

			// DDIC create failed. The fix in adtler#16 says: regardless of
			// whether SAP returned 404 or 415, the user MUST see the friendly
			// hint, not the raw HTTP error.
			msg := err.Error()
			t.Logf("[%s] DTEL create error: %v", sys.Name, err)

			if !strings.Contains(msg, "not available on this SAP system") {
				t.Errorf("[%s] expected the friendly DDIC-unavailable hint, got: %v", sys.Name, err)
			}
			if !strings.Contains(msg, "DTEL") {
				t.Errorf("[%s] error should mention object type DTEL, got: %v", sys.Name, err)
			}
			// The friendly hint must not contain the raw status text — that
			// would mean the error fell through to checkResponse instead of
			// the guard at object.go:131.
			if strings.Contains(msg, "ExceptionUnsupportedMediaType") || strings.Contains(msg, "Nicht unterstützter Medientyp") {
				t.Errorf("[%s] raw 415 message leaked through the guard: %v", sys.Name, err)
			}
		})
	}
}
