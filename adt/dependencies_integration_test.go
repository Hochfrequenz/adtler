//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestGetObjectDependencies_PROG_Integration and TestGetObjectDependencies_TABL_Integration
// exercise GetObjectDependencies against every system in SAP_INTEGRATION_SYSTEMS.
//
// PROG lookup uses the D010TAB flat dependency table (populated by the ABAP activator),
// so it returns the full dependency set without recursion. TABL lookup performs an
// iterative BFS over the DDIC catalog tables (DD03L→DTEL/CHECKTABLE, DD04L→DOMA,
// DD01L→ENTITYTAB). Both paths must work identically on R/3 and S/4.
func TestGetObjectDependencies_PROG_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			result, err := sys.Client.GetObjectDependencies(ctx, "PROG", "Z_ADT_MCP_TEST_REPORT", 0, 3)
			if err != nil {
				t.Fatalf("GetObjectDependencies PROG: %v", err)
			}
			if result.ObjectType != "PROG" {
				t.Errorf("ObjectType: got %q, want \"PROG\"", result.ObjectType)
			}
			if result.ObjectName != "Z_ADT_MCP_TEST_REPORT" {
				t.Errorf("ObjectName: got %q, want \"Z_ADT_MCP_TEST_REPORT\"", result.ObjectName)
			}
			if result.Count != len(result.Dependencies) {
				t.Errorf("Count %d != len(Dependencies) %d", result.Count, len(result.Dependencies))
			}
			// Z_ADT_MCP_TEST_REPORT uses cl_abap_unit_assert (or similar), so the
			// activator populates D010TAB with at least one DDIC entry. An empty
			// result indicates a broken query path, not a sparse fixture.
			if len(result.Dependencies) == 0 {
				t.Errorf("[%s] expected at least one dependency for Z_ADT_MCP_TEST_REPORT (D010TAB should be populated after activation)", sys.Name)
			}
			for _, d := range result.Dependencies {
				if d.Name == "" {
					t.Error("dependency has empty Name")
				}
				if d.UseType == "" {
					t.Errorf("dependency %q has empty UseType", d.Name)
				}
			}
			t.Logf("[%s] %d dependencies", sys.Name, len(result.Dependencies))
			for i, d := range result.Dependencies {
				if i < 15 {
					t.Logf("  %s (%s)", d.Name, d.UseType)
				}
			}
			if len(result.Warnings) > 0 {
				t.Logf("[%s] warnings: %v", sys.Name, result.Warnings)
			}
		})
	}
}

func TestGetObjectDependencies_TABL_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// SCARR is the standard SAP carrier table (SFLIGHT demo). It has
			// DTEL/DOMA dependencies via DD03L and is present on every system.
			result, err := sys.Client.GetObjectDependencies(ctx, "TABL", "SCARR", 0, 2)
			if err != nil {
				t.Fatalf("GetObjectDependencies TABL: %v", err)
			}
			if result.ObjectType != "TABL" {
				t.Errorf("ObjectType: got %q, want \"TABL\"", result.ObjectType)
			}
			if result.ObjectName != "SCARR" {
				t.Errorf("ObjectName: got %q, want \"SCARR\"", result.ObjectName)
			}
			if result.Count != len(result.Dependencies) {
				t.Errorf("Count %d != len(Dependencies) %d", result.Count, len(result.Dependencies))
			}
			if len(result.Dependencies) == 0 {
				t.Error("expected at least one dependency for SCARR (standard SAP table with DTEL/DOMA fields)")
			}
			// All UseType values that classifyDDICObjects / tabclassToUseType can
			// return. VIEW comes from DD02L TABCLASS="VIEW" and TADIR OBJECT="VIEW".
			// TABLE_TYPE comes from TADIR OBJECT="TTYP". UNKNOWN is the fallback
			// for any SAP-internal type not in the mapping tables.
			validUseTypes := map[string]bool{
				"DATA_ELEMENT": true,
				"TABLE":        true,
				"STRUCTURE":    true,
				"DOMAIN":       true,
				"VIEW":         true,
				"TABLE_TYPE":   true,
				"UNKNOWN":      true,
			}
			for _, d := range result.Dependencies {
				if !validUseTypes[d.UseType] {
					t.Errorf("dependency %q: unexpected UseType %q", d.Name, d.UseType)
				}
			}
			t.Logf("[%s] SCARR: %d dependencies", sys.Name, len(result.Dependencies))
			for i, d := range result.Dependencies {
				if i < 15 {
					t.Logf("  %s (%s)", d.Name, d.UseType)
				}
			}
			if len(result.Warnings) > 0 {
				t.Logf("[%s] warnings: %v", sys.Name, result.Warnings)
			}
		})
	}
}
