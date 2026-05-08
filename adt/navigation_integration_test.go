//go:build integration

package adt_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// TestNavigateToDefinition_MultiSystem_Integration exercises
// NavigateToDefinition against real SAP systems. The fix posts the source
// code as the request body — CL_SEDI_ADT_RES_NAVIGATION reads the body via
// get_handler_for_plain_text and combines it with the cursor position from
// the URI fragment to compute the target. Without the body the handler
// returns an empty response, which earlier looked like an "echo" of the
// input URI.
//
// The test asserts that a clear cross-reference (cl_abap_unit_assert or
// TYPE string) resolves to a *different* URI than the cursor position —
// echoing back means the fix regressed. When no cross-reference exists in
// the test fixture (the R/3 stub is a bare REPORT), the test skips.
func TestNavigateToDefinition_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			const uri = "/sap/bc/adt/programs/programs/z_adt_mcp_test_report"
			src, err := sys.Client.GetSource(ctx, uri)
			if err != nil {
				t.Skipf("[%s] Z_ADT_MCP_TEST_REPORT not available: %v", sys.Name, err)
			}

			// Find a line with a cross-reference (cl_abap_unit_assert or TYPE string).
			lines := strings.Split(src.Source, "\n")
			navLine, navCol := 0, 0
			for i, line := range lines {
				col := strings.Index(strings.ToLower(line), "cl_abap_unit_assert")
				if col >= 0 {
					navLine = i + 1
					navCol = col + 1
					break
				}
			}
			if navLine == 0 {
				for i, line := range lines {
					col := strings.Index(strings.ToLower(line), "type string")
					if col >= 0 {
						navLine = i + 1
						navCol = col + 6
						break
					}
				}
			}
			if navLine == 0 {
				t.Skipf("[%s] no navigable cross-reference in source (%d lines)", sys.Name, len(lines))
			}

			sourceURI := uri + "/source/main#start=" +
				strconv.Itoa(navLine) + "," + strconv.Itoa(navCol)
			t.Logf("[%s] navigating from %s", sys.Name, sourceURI)

			target, err := sys.Client.NavigateToDefinition(ctx, sourceURI, src.Source)
			if err != nil {
				t.Fatalf("[%s] NavigateToDefinition returned error: %v", sys.Name, err)
			}
			if target == "" || target == sourceURI {
				t.Fatalf("[%s] endpoint echoed input (target=%q) — "+
					"the body-as-source fix regressed; the SAP handler "+
					"likely received an empty body", sys.Name, target)
			}
			t.Logf("[%s] resolved to %s", sys.Name, target)
		})
	}
}
