//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// browseCandidateQueries are the package name patterns tried, in order, when
// the test looks for a package that has content. Customer namespaces come
// first because they are small; the bare wildcard is the last resort.
var browseCandidateQueries = []string{"Z*", "Y*", "*"}

// browseCandidateLimit bounds how many packages one pattern may offer.
const browseCandidateLimit = 10

// browseCrossCheckLimit bounds how many of the browsed objects are compared
// with GetObjectInfo, to keep the number of requests small.
const browseCrossCheckLimit = 5

// findBrowsablePackage asks the system for packages and returns the first one
// whose BrowsePackage result is not empty, together with that result. It
// returns an empty name when the system offers none.
func findBrowsablePackage(ctx context.Context, t *testing.T, client adt.Client, host string) (string, []adt.ObjectInfo) {
	t.Helper()
	for _, query := range browseCandidateQueries {
		pkgs, err := client.SearchPackages(ctx, query, browseCandidateLimit)
		if err != nil {
			t.Fatalf("SearchPackages: %s", redact(err.Error(), host, ""))
		}
		for _, p := range pkgs {
			objects, err := client.BrowsePackage(ctx, p.Name)
			if err != nil {
				// A package the user may not browse is not a candidate; plain
				// BrowsePackage failures are covered by TestBrowsePackage_Integration.
				continue
			}
			if len(objects) > 0 {
				return p.Name, objects
			}
		}
	}
	return "", nil
}

// TestBrowsePackage_PackageName_MultiSystem_Integration covers adtler#152 on
// every system in the SAP_INTEGRATION_SYSTEMS whitelist: BrowsePackage must
// name the browsed package on every object it returns, and that must agree
// with what GetObjectInfo reports for the same URI.
//
// The test finds a package that has content through the system's own search,
// so it does not depend on a particular fixture object, and it skips on a system
// without one. It logs counts only, never object or package names.
//
// What to look for in the -v output, per system: "objects without package"
// must be 0. "cross-checked" is how many objects were also read through
// GetObjectInfo, "agreeing" how many of those carried the same package;
// "no package in object document" counts objects whose own document names no
// package (the same open question as for function modules in adtler#169) and
// are neither agreeing nor disagreeing.
func TestBrowsePackage_PackageName_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			pkg, objects := findBrowsablePackage(ctx, t, sys.Client, sys.Config.Host)
			if pkg == "" {
				t.Skip("no package with content found on this system")
			}

			missing := 0
			for _, o := range objects {
				if o.PackageName == "" {
					missing++
				}
				if o.PackageName != strings.ToUpper(pkg) {
					t.Errorf("an object of type %s carries a package that is not the browsed one (empty: %t)", o.Type, o.PackageName == "")
				}
			}
			described := 0
			for _, o := range objects {
				if o.Description != "" {
					described++
				}
			}
			t.Logf("objects browsed: %d, objects without package: %d, objects with a description: %d", len(objects), missing, described)

			checked, agreeing, undocumented := 0, 0, 0
			for _, o := range objects {
				if checked >= browseCrossCheckLimit {
					break
				}
				if strings.HasPrefix(o.Type, "DEVC") {
					// A sub-package's own document names itself as its package
					// (measured on both releases), not the package it is
					// listed in, so the two answers differ by design.
					continue
				}
				info, err := sys.Client.GetObjectInfo(ctx, o.URI)
				if err != nil {
					continue // some node kinds have no readable object document
				}
				checked++
				switch info.PackageName {
				case "":
					undocumented++
				case o.PackageName:
					agreeing++
				default:
					t.Errorf("an object of type %s: BrowsePackage and GetObjectInfo name different packages", o.Type)
				}
			}
			t.Logf("cross-checked: %d, agreeing: %d, no package in object document: %d", checked, agreeing, undocumented)
		})
	}
}
