//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestSourceURISuffix_Integration is the live half of adtler#210: GetSource,
// and the methods that build further sub-paths, must return the same result
// for an object's bare URI and for its ".../source/main" URI. Before the fix
// the suffixed form produced ".../source/main/source/main" and SAP answered
// 404.
//
// Fixtures are discovered, never hardcoded: the test searches the system for
// objects of each type and uses the first one whose bare URI reads. Only
// counts and types are logged, never object names, and every error passes
// through redact first.
func TestSourceURISuffix_Integration(t *testing.T) {
	types := []string{
		"PROG/P",  // program
		"CLAS/OC", // class
		"INTF/OI", // interface
		"FUGR/FF", // function module
		"FUGR/I",  // function group include
	}
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			for _, adtType := range types {
				t.Run(adtType, func(t *testing.T) {
					obj, bare := findReadableSource(t, ctx, sys, adtType)
					clean := func(err error) string { return redact(err.Error(), sys.Config.Host, obj.Name) }
					suffixed := obj.URI + "/source/main"

					got, err := sys.Client.GetSource(ctx, suffixed)
					if err != nil {
						t.Fatalf("GetSource with the source URI: %s", clean(err))
					}
					if got.Source != bare.Source {
						t.Errorf("GetSource: source differs between bare and source URI (%d vs %d bytes)",
							len(bare.Source), len(got.Source))
					}
					if got.ETag != bare.ETag {
						t.Errorf("GetSource: ETag differs between bare and source URI")
					}

					t.Run("GetVersionHistory", func(t *testing.T) {
						want, err := sys.Client.GetVersionHistory(ctx, obj.URI)
						if err != nil {
							t.Skipf("no version history through the bare URI either, nothing to compare: %s", clean(err))
						}
						have, err := sys.Client.GetVersionHistory(ctx, suffixed)
						if err != nil {
							t.Fatalf("GetVersionHistory with the source URI: %s", clean(err))
						}
						if len(have) != len(want) {
							t.Errorf("GetVersionHistory: %d versions with the source URI, %d with the bare URI", len(have), len(want))
						}
					})

					if adtType == "CLAS/OC" {
						t.Run("GetClassDefinition", func(t *testing.T) {
							want, err := sys.Client.GetClassDefinition(ctx, obj.URI)
							if err != nil {
								t.Fatalf("GetClassDefinition with the bare URI: %s", clean(err))
							}
							have, err := sys.Client.GetClassDefinition(ctx, suffixed)
							if err != nil {
								t.Fatalf("GetClassDefinition with the source URI: %s", clean(err))
							}
							if have.Source != want.Source {
								t.Errorf("GetClassDefinition: definition differs between bare and source URI (%d vs %d bytes)",
									len(want.Source), len(have.Source))
							}
						})
					}
				})
			}
		})
	}
}

// findReadableSource returns the first object of adtType whose bare URI
// GetSource can read, together with that read. It fails when SAP answers the
// search with an error or none of the candidates is readable, and skips only
// when the system holds no such object.
func findReadableSource(t *testing.T, ctx context.Context, sys integrationSystem, adtType string) (adt.ObjectInfo, *adt.SourceResult) {
	t.Helper()
	results := searchFixtures(t, ctx, sys, adtType)
	t.Logf("search returned %d %s candidate(s)", len(results), adtType)
	var lastErr string
	for _, r := range results {
		if r.URI == "" {
			continue
		}
		src, err := sys.Client.GetSource(ctx, r.URI)
		if err == nil {
			return r, src
		}
		lastErr = redact(err.Error(), sys.Config.Host, r.Name)
	}
	if lastErr == "" {
		t.Skipf("no %s object on this system", adtType)
	}
	t.Fatalf("none of the %d %s candidates is readable through its bare URI; last error: %s",
		len(results), adtType, lastErr)
	return adt.ObjectInfo{}, nil
}
