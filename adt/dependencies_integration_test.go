//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestGetObjectDependencies_Integration validates the dependency engine against
// live SAP on every whitelisted system: a PROG lookup via D010TAB (the
// Z_ADT_MCP_TEST_REPORT fixture) and a DDIC BFS on the standard SCARR table.
func TestGetObjectDependencies_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys

		t.Run(sys.Name+"/PROG", func(t *testing.T) {
			res, err := sys.Client.GetObjectDependencies(ctx, "PROG", "Z_ADT_MCP_TEST_REPORT", 200, 3)
			if err != nil {
				t.Fatalf("GetObjectDependencies(PROG) error: %v", err)
			}
			if res.ObjectType != "PROG" || res.ObjectName != "Z_ADT_MCP_TEST_REPORT" {
				t.Errorf("header mismatch: got type=%q name=%q", res.ObjectType, res.ObjectName)
			}
			if res.Count != len(res.Dependencies) {
				t.Errorf("Count %d != len(Dependencies) %d", res.Count, len(res.Dependencies))
			}
			// The fixture report may legitimately have zero DDIC dependencies;
			// the assertion is that the D010TAB lookup + classification run
			// cleanly, not that the set is non-empty.
			for _, d := range res.Dependencies {
				t.Logf("  %s (%s)", d.Name, d.UseType)
			}
		})

		t.Run(sys.Name+"/TABL_SCARR", func(t *testing.T) {
			res, err := sys.Client.GetObjectDependencies(ctx, "TABL", "SCARR", 200, 3)
			if err != nil {
				t.Fatalf("GetObjectDependencies(TABL SCARR) error: %v", err)
			}
			if res.Count == 0 {
				t.Fatal("expected SCARR to have DDIC dependencies (fields → data elements); got none")
			}
			// SCARR's fields reference data elements, so at least one DATA_ELEMENT
			// is expected from the DD03L BFS.
			hasDataElement := false
			for _, d := range res.Dependencies {
				if d.UseType == adt.UseTypeDataElement {
					hasDataElement = true
				}
				t.Logf("  %s (%s)", d.Name, d.UseType)
			}
			if !hasDataElement {
				t.Errorf("expected at least one DATA_ELEMENT dependency for SCARR, got: %+v", res.Dependencies)
			}
		})
	}
}
