//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestSearchPackages_Integration confirms SearchPackages constrains the search
// to packages (DEVC) on every whitelisted system and finds the test package.
func TestSearchPackages_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			results, err := sys.Client.SearchPackages(ctx, "Z*", 50)
			if err != nil {
				t.Fatalf("SearchPackages failed: %v", err)
			}
			if len(results) == 0 {
				t.Fatal("SearchPackages(\"Z*\") returned no results")
			}
			foundTestPackage := false
			for _, r := range results {
				// Every result must be a package object type (DEVC/K).
				if !strings.HasPrefix(r.Type, "DEVC") {
					t.Errorf("non-package result: %s (type %q)", r.Name, r.Type)
				}
				if r.Name == testPackage {
					foundTestPackage = true
				}
			}
			if !foundTestPackage {
				t.Errorf("expected the %s fixture package in Z* results", testPackage)
			}
			t.Logf("SearchPackages(\"Z*\") returned %d packages", len(results))
		})
	}
}
