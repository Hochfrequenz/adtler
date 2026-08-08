//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

func TestSearchObjects_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	// Search for the test report by exact name.
	results, err := client.SearchObjects(ctx, "Z_ADT_MCP_TEST_REPORT", "", 10)
	if err != nil {
		t.Fatalf("SearchObjects failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchObjects returned no results for Z_ADT_MCP_TEST_REPORT")
	}

	found := false
	for _, r := range results {
		t.Logf("  %s (%s) — %s", r.Name, r.Type, r.URI)
		if r.Name == "Z_ADT_MCP_TEST_REPORT" {
			found = true
		}
	}
	if !found {
		t.Error("Z_ADT_MCP_TEST_REPORT not found in search results")
	}
}

func TestSearchObjects_Wildcard(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	// Wildcard search limited to 5 results.
	results, err := client.SearchObjects(ctx, "Z_ADT_MCP_TEST*", "", 5)
	if err != nil {
		t.Fatalf("SearchObjects wildcard failed: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchObjects wildcard returned no results")
	}
	t.Logf("wildcard search returned %d results", len(results))
	for _, r := range results {
		t.Logf("  %s (%s)", r.Name, r.Type)
	}
}

func TestSearchObjects_NoResults(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	results, err := client.SearchObjects(ctx, "Z_DEFINITELY_DOES_NOT_EXIST_99", "", 5)
	if err != nil {
		t.Fatalf("SearchObjects failed: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}

// TestSearchPackages_Integration confirms that SearchPackages filters search
// results to the DEVC/K object type on every configured system. SearchPackages
// is a thin wrapper over SearchObjects with objectType=ObjectTypePackage; this
// test verifies that the objectType constraint reaches the wire on both R/3 and S/4.
func TestSearchPackages_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			results, err := sys.Client.SearchPackages(ctx, "Z*", 20)
			if err != nil {
				t.Fatalf("SearchPackages: %v", err)
			}
			if len(results) == 0 {
				t.Skip("no Z* packages found on this system — cannot assert type constraint")
			}
			for _, r := range results {
				if r.Type != adt.ObjectTypePackage {
					t.Errorf("result %q has Type %q, want %q (SearchPackages must filter to DEVC/K only)", r.Name, r.Type, adt.ObjectTypePackage)
				}
			}
			t.Logf("[%s] SearchPackages Z*: %d results (all DEVC/K)", sys.Name, len(results))
		})
	}
}
