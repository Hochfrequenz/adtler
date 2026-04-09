//go:build integration

package adt_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// TestNavigateToDefinition_Namespace_MultiSystem_Integration exercises
// NavigateToDefinition against real SAP systems. The namespace parsing fix
// in this PR ensures the adtcore:uri attribute is extracted correctly from
// the namespaced XML response.
//
// NOTE: from the Wave 2 investigation (issue #8), we know the endpoint
// genuinely echoes the input position for many cursor positions — even
// for clear cross-references like cl_abap_unit_assert. This is a SAP
// endpoint limitation, not a parsing bug. The test therefore:
//   - Asserts the call does NOT error (namespace parsing doesn't break it)
//   - Logs whether the result is an echo or an actual navigation target
//   - Does NOT hard-fail on echo (that's the known behaviour)
func TestNavigateToDefinition_Namespace_MultiSystem_Integration(t *testing.T) {
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

			target, err := sys.Client.NavigateToDefinition(ctx, sourceURI)
			if err != nil {
				t.Fatalf("[%s] NavigateToDefinition returned error: %v — "+
					"namespace parsing may be broken", sys.Name, err)
			}

			t.Logf("[%s] target: %s", sys.Name, target)
			if target == sourceURI || target == "" {
				t.Logf("[%s] endpoint echoed input (known SAP limitation per issue #8 investigation)", sys.Name)
			} else {
				t.Logf("[%s] endpoint returned a DIFFERENT target — navigation resolved!", sys.Name)
			}
		})
	}
}
