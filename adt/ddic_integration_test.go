//go:build integration

package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestGetTableFields_MultiSystem_Integration regression-tests adtler#10
// against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Two cases per system:
//
//  1. Existing standard table T001 — must return a populated field slice
//     (Bukrs is the key, plus a known set of administrative columns). T001
//     exists on every R/3 and S/4 release since the dawn of time, so this
//     also serves as a smoke test for the underlying RunQuery / DD03L flow.
//
//  2. A made-up table name — must return a typed *adt.TableNotFoundError
//     and NOT return (nil, nil), which is the silent shape that motivated
//     this fix in the first place.
//
// A failure on either case means GetTableFields has regressed on the
// affected system. The "not found" assertion is the one that would have
// caught the original bug, where empty DD03L results were swallowed.
func TestGetTableFields_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			t.Run("existing_T001", func(t *testing.T) {
				fields, err := sys.Client.GetTableFields(ctx, "T001")
				if err != nil {
					t.Fatalf("[%s] GetTableFields(T001): %v", sys.Name, err)
				}
				if len(fields) == 0 {
					t.Fatalf("[%s] expected populated fields for T001, got 0", sys.Name)
				}
				// T001 is "Company Codes". BUKRS is the primary key on every release.
				foundBukrs := false
				for _, f := range fields {
					if f.Name == "BUKRS" {
						foundBukrs = true
						if !f.IsKey {
							t.Errorf("[%s] T001.BUKRS should be marked as key", sys.Name)
						}
						break
					}
				}
				if !foundBukrs {
					t.Errorf("[%s] T001 should have a BUKRS column, got fields: %+v", sys.Name, fields)
				}
				t.Logf("[%s] T001 has %d fields", sys.Name, len(fields))
			})

			t.Run("nonexistent_table", func(t *testing.T) {
				const fakeName = "Z_DOES_NOT_EXIST_MULTISYS_999"
				fields, err := sys.Client.GetTableFields(ctx, fakeName)
				if err == nil {
					t.Fatalf("[%s] expected error for missing table, got fields=%v", sys.Name, fields)
				}
				if fields != nil {
					t.Errorf("[%s] fields should be nil on error, got %v", sys.Name, fields)
				}
				var notFound *adt.TableNotFoundError
				if !errors.As(err, &notFound) {
					t.Fatalf("[%s] expected *TableNotFoundError, got %T: %v", sys.Name, err, err)
				}
				if notFound.TableName != fakeName {
					t.Errorf("[%s] TableName: got %q, want %q", sys.Name, notFound.TableName, fakeName)
				}
				t.Logf("[%s] missing table correctly returned: %v", sys.Name, err)
			})
		})
	}
}
