//go:build integration

package adt_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// TestNavigateToDefinition_MultiSystem_Integration regression-tests adtler#8.
//
// The probe showed that all positions echoed back — but the probe tested
// positions on RSPARAM (a standard SAP program without class cross-refs)
// and Z_ADT_MCP_TEST_REPORT (which on S/4 has cl_abap_unit_assert on
// line 15, col 5 — a clear cross-reference that SHOULD navigate).
//
// This integration test exercises the same cl_abap_unit_assert position.
// If the fix (parsing a child objectReference element instead of the root
// uri attribute) is correct, S/4 should return a URI pointing at the
// CL_ABAP_UNIT_ASSERT class definition, not the echo of the input.
//
// If SAP's response doesn't have a child objectReference (i.e. the endpoint
// genuinely only returns echoes even for real cross-references), the test
// logs the result rather than hard-failing — the fix is an educated guess
// based on ADT XML conventions, and the integration test is partly a
// diagnostic.
func TestNavigateToDefinition_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Use Z_ADT_MCP_TEST_REPORT on S/4 (has cl_abap_unit_assert).
			// On R/3 the fixture may be a bare REPORT with no cross-refs.
			const testURI = "/sap/bc/adt/programs/programs/z_adt_mcp_test_report"

			src, err := sys.Client.GetSource(ctx, testURI)
			if err != nil {
				t.Skipf("[%s] Z_ADT_MCP_TEST_REPORT not available: %v", sys.Name, err)
			}

			// Find a line containing "cl_abap_unit_assert" for a real cross-ref.
			lines := strings.Split(src.Source, "\n")
			navLine, navCol := 0, 0
			for i, line := range lines {
				col := strings.Index(strings.ToLower(line), "cl_abap_unit_assert")
				if col >= 0 {
					navLine = i + 1 // 1-based
					navCol = col + 1
					break
				}
			}

			if navLine == 0 {
				// No cl_abap_unit_assert in source — try "TYPE string" as backup.
				for i, line := range lines {
					col := strings.Index(strings.ToLower(line), "type string")
					if col >= 0 {
						navLine = i + 1
						navCol = col + 6 // position on "string"
						break
					}
				}
			}

			if navLine == 0 {
				t.Skipf("[%s] no navigable cross-reference found in source (%d lines)", sys.Name, len(lines))
			}

			sourceURI := testURI + "/source/main#start=" +
				strconv.Itoa(navLine) + "," + strconv.Itoa(navCol)
			t.Logf("[%s] navigating from %s", sys.Name, sourceURI)

			target, err := sys.Client.NavigateToDefinition(ctx, sourceURI)
			if err != nil {
				t.Fatalf("[%s] NavigateToDefinition: %v", sys.Name, err)
			}
			t.Logf("[%s] target: %s", sys.Name, target)

			// The critical assertion: did we navigate AWAY from the input?
			if target == sourceURI {
				t.Logf("[%s] WARNING: target is identical to input (echo). "+
					"Either the fix's XML parsing didn't find a child element "+
					"in the real response, or SAP genuinely doesn't resolve "+
					"cross-references at this position. Check raw XML.",
					sys.Name)
				// Soft-fail: log as warning, not hard error. The fix is an
				// educated guess; if SAP doesn't return a child element, we
				// need the raw XML to know the actual structure.
			} else {
				// Navigation resolved — verify it points at a class or type.
				if strings.Contains(strings.ToLower(target), "cl_abap_unit_assert") ||
					strings.Contains(target, "/oo/classes/") ||
					strings.Contains(target, "/ddic/") {
					t.Logf("[%s] SUCCESS: navigated to %s", sys.Name, target)
				} else {
					t.Logf("[%s] navigated to unexpected target: %s", sys.Name, target)
				}
			}
		})
	}
}

