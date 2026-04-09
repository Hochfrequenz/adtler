//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestGetABAPDoc_MultiSystem_Integration regression-tests adtler#18 against
// every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Before the fix, GetABAPDoc returned the ABAP documentation homepage
// (~4-9KB of generic HTML) regardless of the keyword on both R/3 and S/4.
// After the fix it should return keyword-specific content. The test
// validates this by checking for keyword-relevant text in the response
// and asserting the homepage markers are absent.
func TestGetABAPDoc_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// DATA is one of the most fundamental ABAP keywords. Its
			// documentation page is guaranteed to exist on every release and
			// should contain phrases like "variable", "declaration", "TYPE".
			text, err := sys.Client.GetABAPDoc(ctx, "DATA")
			if err != nil {
				t.Fatalf("[%s] GetABAPDoc(DATA): %v", sys.Name, err)
			}
			t.Logf("[%s] response length: %d bytes", sys.Name, len(text))
			if len(text) > 500 {
				t.Logf("[%s] first 500 bytes: %s", sys.Name, text[:500])
			} else {
				t.Logf("[%s] full text: %s", sys.Name, text)
			}

			// The homepage (the old broken response) has these characteristic
			// markers — content-type navigation links, not keyword docs.
			homepageMarkers := []string{
				"ABAP - Dictionary",
				"ABAP - Core Data Services",
				"ABAP - RAP Business Objects",
				"Programming Guidelines",
			}
			for _, marker := range homepageMarkers {
				if strings.Contains(text, marker) {
					t.Errorf("[%s] response contains homepage marker %q — the fix may have regressed "+
						"and returned the homepage instead of keyword-specific docs", sys.Name, marker)
				}
			}

			// The DATA keyword documentation should mention at least one of
			// these terms. Loose matching because the exact wording differs
			// by SAP release and language (German on R/3, English on S/4).
			dataKeywordSignals := []string{
				"DATA",   // the keyword itself
				"Daten",  // German "data"
				"variable", // English doc typically mentions variables
				"Variable", // German
				"ABAP",     // documentation always references ABAP somewhere
			}
			matched := false
			for _, signal := range dataKeywordSignals {
				if strings.Contains(text, signal) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("[%s] response doesn't contain any expected DATA keyword signals %v — "+
					"the response may not be keyword-specific. Length=%d",
					sys.Name, dataKeywordSignals, len(text))
			}
		})
	}
}
