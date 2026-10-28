//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestConfiguredLanguageBecomesOriginalLanguage_MultiSystem_Integration is
// the regression test for #247 (Hochfrequenz/aibap.mcp#592): an object
// created through adtler must get the configured Language as its original
// language (TADIR MASTERLANG), not the user's SU01 logon default.
//
// It creates one $TMP program under Language "DE" and one under "EN" and
// checks each one's MASTERLANG. Whatever the user's SU01 default is, at most
// one of the two matches it, so without the fix (no language header, both
// objects get the default) one sub-test always fails.
func TestConfiguredLanguageBecomesOriginalLanguage_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			for _, lang := range []string{"DE", "EN"} {
				lang := lang
				t.Run(lang, func(t *testing.T) {
					cfg := sys.Config
					cfg.Language = lang
					client := adt.NewClient(cfg)

					name := fmt.Sprintf("Z_ADT_LANG_%s_%d", lang, time.Now().UnixNano()%100000)
					uri := "/sap/bc/adt/programs/programs/" + name
					if err := client.CreateObject(ctx, "PROG", name, "$TMP", "adtler#247 logon language test", ""); err != nil {
						t.Fatalf("CreateObject: %v", err)
					}
					// DeleteObject locks optimistically itself; a separate
					// LockObject here would make S/4 refuse the delete as
					// "currently editing".
					t.Cleanup(func() {
						if err := client.DeleteObject(context.Background(), uri, "", ""); err != nil {
							t.Logf("cleanup delete failed: %v", err)
						}
					})

					res, err := client.RunQuery(ctx, fmt.Sprintf(
						"SELECT MASTERLANG FROM TADIR WHERE PGMID = 'R3TR' AND OBJECT = 'PROG' AND OBJ_NAME = '%s'", name), 1)
					if err != nil {
						t.Fatalf("RunQuery TADIR: %v", err)
					}
					if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
						t.Fatalf("expected one TADIR row with one column, got %d rows", len(res.Rows))
					}
					// Data preview may render the language as the one-letter SAP
					// code ("D") or through the ISOLA conversion exit ("DE"); the
					// first letter is the same for DE and EN either way.
					got := strings.ToUpper(strings.TrimSpace(res.Rows[0][0]))
					if got == "" || got[0] != lang[0] {
						t.Errorf("MASTERLANG = %q, want %s — the session did not log on in the configured language", got, lang)
					}
				})
			}
		})
	}
}
