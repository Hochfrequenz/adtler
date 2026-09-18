//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// discoveryMaxRows bounds the SUI_TM_MM_APP scan used to discover a live
// CAT_ID fixture. Generous enough to find a populated catalog without
// scanning the whole table.
const discoveryMaxRows = 500

// allowedUIADUseTypes are the five use-type constants introduced for UIAC
// (Fiori catalog) / UIAD (app entry) dependency resolution. A UIAD
// dependency's use type must be one of these.
var allowedUIADUseTypes = map[string]bool{
	adt.UseTypeUIApp:           true,
	adt.UseTypeTransaction:     true,
	adt.UseTypeWebDynproApp:    true,
	adt.UseTypeWebClientTarget: true,
	adt.UseTypeUI5App:          true,
}

// TestUIACUIADDependencies_MultiSystem_Integration exercises
// GetObjectDependencies for the UIAC and UIAD object types added for
// issue #139. It never hardcodes a catalog or app id — both repositories
// are public, and the real catalog/app names in this landscape are
// internal data. Instead it discovers a live CAT_ID at runtime by querying
// SUI_TM_MM_APP through the client's own query path, then derives an app
// id from whatever the UIAC lookup returns.
//
// The R/3 (ECC) system carries SUI_TM_MM_APP rows but has no UIAC objects,
// so it may have nothing populated to discover; this test t.Skips there
// rather than failing, since there is nothing to exercise.
func TestUIACUIADDependencies_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			catID := discoverCatID(t, ctx, sys.Client)
			if catID == "" {
				t.Skip("no populated CAT_ID found in SUI_TM_MM_APP on this system — nothing to exercise")
			}

			uiacResult, err := sys.Client.GetObjectDependencies(ctx, "UIAC", catID, 0, 1)
			if err != nil {
				t.Fatalf("[%s] GetObjectDependencies(UIAC): %v", sys.Name, err)
			}
			if uiacResult == nil {
				t.Fatalf("[%s] GetObjectDependencies(UIAC) returned nil result", sys.Name)
			}
			t.Logf("[%s] UIAC catalog lookup resolved %d app dependencies", sys.Name, uiacResult.Count)

			for _, dep := range uiacResult.Dependencies {
				if dep.UseType != adt.UseTypeUIApp {
					t.Errorf("[%s] UIAC dependency use type: got %q, want %q", sys.Name, dep.UseType, adt.UseTypeUIApp)
				}
			}

			if len(uiacResult.Dependencies) == 0 {
				t.Skip("discovered catalog has no app entries — nothing further to exercise for UIAD")
			}

			appID := uiacResult.Dependencies[0].Name
			uiadResult, err := sys.Client.GetObjectDependencies(ctx, "UIAD", appID, 0, 1)
			if err != nil {
				t.Fatalf("[%s] GetObjectDependencies(UIAD): %v", sys.Name, err)
			}
			if uiadResult == nil {
				t.Fatalf("[%s] GetObjectDependencies(UIAD) returned nil result", sys.Name)
			}
			t.Logf("[%s] UIAD app entry lookup resolved %d launch-target dependencies, %d warnings",
				sys.Name, uiadResult.Count, len(uiadResult.Warnings))

			if len(uiadResult.Dependencies) == 0 {
				if len(uiadResult.Warnings) == 0 {
					t.Errorf("[%s] UIAD app entry has no dependencies and no warning explaining why", sys.Name)
				}
				return
			}
			for _, dep := range uiadResult.Dependencies {
				if !allowedUIADUseTypes[dep.UseType] {
					t.Errorf("[%s] UIAD dependency use type: got %q, want one of the five UIAC/UIAD use types", sys.Name, dep.UseType)
				}
			}
		})
	}
}

// discoverCatID finds a non-blank CAT_ID in SUI_TM_MM_APP by scanning up to
// discoveryMaxRows rows, ordered for determinism. It returns "" when the
// system has no populated CAT_ID within that scan — the caller treats that
// as "nothing to work with" and skips rather than fails, which is the
// correct outcome on a system that carries no UIAC objects at all (e.g.
// R/3/ECC). This single-column query is checked by column name before
// positional row access, matching this package's other RunQuery-based
// integration tests (see query_integration_test.go).
func discoverCatID(t *testing.T, ctx context.Context, client adt.Client) string {
	t.Helper()
	qr, err := client.RunQuery(ctx, "SELECT CAT_ID FROM SUI_TM_MM_APP ORDER BY CAT_ID", discoveryMaxRows)
	if err != nil {
		t.Fatalf("discovering CAT_ID fixture: %v", err)
	}
	if qr == nil || len(qr.Columns) == 0 || qr.Columns[0].Name != "CAT_ID" {
		return ""
	}
	for _, row := range qr.Rows {
		if len(row) == 0 {
			continue
		}
		if v := strings.TrimSpace(row[0]); v != "" {
			return v
		}
	}
	return ""
}
