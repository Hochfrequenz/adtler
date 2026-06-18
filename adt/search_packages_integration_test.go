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
			// Narrowed to the fixture-package prefix so the "found" assertion
			// can't flake on a system with >maxResults Z* packages (the test
			// package sorts late). Still validates the DEVC/K type constraint.
			results, err := sys.Client.SearchPackages(ctx, testPackage+"*", 50)
			if err != nil {
				t.Fatalf("SearchPackages failed: %v", err)
			}
			if len(results) == 0 {
				t.Fatalf("SearchPackages(%q) returned no results", testPackage+"*")
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
				t.Errorf("expected the %s fixture package in the results", testPackage)
			}
			t.Logf("SearchPackages(%q) returned %d package(s)", testPackage+"*", len(results))
		})
	}
}
