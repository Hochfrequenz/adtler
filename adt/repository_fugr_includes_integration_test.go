//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestGetObjectInfo_FUGRInclude_MultiSystem_Integration regression-tests
// adtler#17 against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Before the fix, acceptHeaderForURI returned the function group vendor type
// (functions.groups.v3+xml) for both the bare FUGR URI AND its include
// sub-resources. R/3's lenient handler accepted it; S/4's strict handler
// rejected the include URIs with HTTP 406 ExceptionResourceNotAcceptable
// and explicitly named functions.fincludes.v2+xml as the only supported
// representation. Single-system R/3-only test rigs missed the bug.
//
// This test creates a fresh function group per system, derives the auto-
// generated TOP include URI from the SAP convention L<fg>TOP, and calls
// GetObjectInfo on it. The fix is validated when the call returns a 200
// with a parsed include record (Type starts with "FUGR/I"). A 406 means
// the FUGR-include special case in acceptHeaderForURI has regressed.
//
// Cleans up the FUGR after the test on each system.
func TestGetObjectInfo_FUGRInclude_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			const fgName = "Z_ADT_MCP_FG_INC"
			fgURI := "/sap/bc/adt/functions/groups/" + strings.ToLower(fgName)
			// SAP auto-generates the TOP include as L<fg>TOP. The ADT URI
			// nests it under the parent function group:
			//   /sap/bc/adt/functions/groups/<fg>/includes/<lfgtop>
			topInclude := strings.ToLower("L" + fgName + "TOP")
			includeURI := fgURI + "/includes/" + topInclude

			// Create the function group fresh. If it already exists from a
			// previous aborted run, reuse it.
			err := sys.Client.CreateObject(ctx, "FUGR", fgName, "$TMP",
				"adtler#17 multi-system integration test", "")
			if err != nil {
				if _, infoErr := sys.Client.GetObjectInfo(ctx, fgURI); infoErr != nil {
					t.Fatalf("[%s] CreateObject FUGR %s failed and group does not exist: %v", sys.Name, fgName, err)
				}
				t.Logf("[%s] FUGR %s already exists, reusing", sys.Name, fgName)
			} else {
				t.Logf("[%s] created FUGR %s", sys.Name, fgName)
			}
			t.Cleanup(func() {
				if delErr := sys.Client.DeleteObject(context.Background(), fgURI, "", ""); delErr != nil {
					t.Logf("[%s] FUGR cleanup failed: %v", sys.Name, delErr)
				}
			})

			// The actual regression test: GetObjectInfo on the include URI
			// must return the include's metadata, NOT 406.
			info, err := sys.Client.GetObjectInfo(ctx, includeURI)
			if err != nil {
				t.Fatalf("[%s] GetObjectInfo(%s): %v — likely the FUGR-include Accept header is wrong", sys.Name, includeURI, err)
			}
			if info.Type == "" {
				t.Errorf("[%s] expected non-empty Type for include, got %+v", sys.Name, info)
			}
			if !strings.HasPrefix(info.Type, "FUGR/I") {
				t.Errorf("[%s] expected Type starting with FUGR/I (function group include), got %q", sys.Name, info.Type)
			}
			t.Logf("[%s] FUGR include %s parsed: Type=%s Name=%s", sys.Name, includeURI, info.Type, info.Name)
		})
	}
}
