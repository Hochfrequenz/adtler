//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestGetCompletions_MultiSystem_Integration exercises GetCompletions against
// real SAP systems. SAP returns proposals in asXML envelope form
// (<asx:abap>...<SCC_COMPLETION>...) when Accept: application/vnd.sap.as+xml
// is sent. The line/column must be encoded in the URI fragment
// (...#start=L,C) — the handler's URI mapper extracts the position from
// there, not from separate query params.
//
// A nil result with no error is acceptable (endpoint responded but had no
// proposals for this position). An error means the request shape regressed.
//
// The test uses Z_ADT_MCP_TEST_REPORT's actual source with "sy-" appended.
// On S/4 this program has executable code; on R/3 it's a bare REPORT.
func TestGetCompletions_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			const uri = "/sap/bc/adt/programs/programs/z_adt_mcp_test_report"
			src, err := sys.Client.GetSource(ctx, uri)
			if err != nil {
				t.Skipf("[%s] Z_ADT_MCP_TEST_REPORT not available: %v", sys.Name, err)
			}

			// Append "sy-" as a completion trigger.
			source := src.Source + "\nsy-"
			lines := len(splitLines(source))

			items, err := sys.Client.GetCompletions(ctx, uri, source, lines, 4)
			if err != nil {
				t.Fatalf("[%s] GetCompletions returned error: %v — "+
					"namespace parsing may be broken", sys.Name, err)
			}
			if items == nil {
				t.Logf("[%s] GetCompletions returned nil (no completions) — "+
					"endpoint responded but had no proposals for this position. "+
					"This is the known behaviour from the Wave 2 probes.", sys.Name)
			} else {
				t.Logf("[%s] GetCompletions returned %d items!", sys.Name, len(items))
				for i, item := range items {
					if i < 5 {
						t.Logf("[%s]   [%d] text=%q desc=%q", sys.Name, i, item.Text, item.Description)
					}
				}
			}
		})
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
