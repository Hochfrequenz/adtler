//go:build integration

package adt_test

import (
	"context"
	"testing"
)

func TestGetCompletions_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	source := "REPORT z_adt_mcp_test_report.\nWRITE "
	line := 2
	column := 6

	completions, err := client.GetCompletions(ctx, testReportURI, source, line, column)
	if err != nil {
		t.Fatalf("GetCompletions failed: %v", err)
	}
	// Canary for the URI-shape / asXML regression. `WRITE ` at column 6
	// is a high-confidence trigger that SAP has proposals for: empirical
	// runs return ~20 keywords/built-ins on both R/3 and S/4. An empty
	// list here means the request shape regressed, not a system quirk.
	if len(completions) == 0 {
		t.Fatalf("GetCompletions returned 0 items for `WRITE ` cursor — " +
			"likely a regression in URI fragment encoding or asXML parsing; " +
			"see CL_CC_ADT_RES_BASE->determine_input_data")
	}
	t.Logf("got %d completions", len(completions))

	for i, c := range completions {
		if i >= 5 {
			t.Logf("  ... and %d more", len(completions)-5)
			break
		}
		t.Logf("  [%d] %s", i, c.Text)
	}
}
