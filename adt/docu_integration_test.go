//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestGetABAPDoc_MultiSystem_Integration regression-tests adtler#18.
//
// The probe showed:
//   - R/3: query=DATA on /sap/public/bc/abap/docu works → returns docs
//   - S/4: /sap/public/bc/abap/docu returns 403 (ICF node inactive)
//
// The test asserts:
//   - On systems where the public servlet is available: keyword-specific
//     content is returned (DATA signals present, homepage markers absent)
//   - On systems where the servlet returns 403: a clear error mentioning
//     SICF activation is returned (not the old homepage fallback)
func TestGetABAPDoc_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			text, err := sys.Client.GetABAPDoc(ctx, "DATA")

			// 403 path: the public docu servlet is not active on this system.
			// The fix should return a clear error, not silently return homepage.
			if err != nil {
				msg := err.Error()
				t.Logf("[%s] GetABAPDoc(DATA) error: %v", sys.Name, err)
				if strings.Contains(msg, "SICF") || strings.Contains(msg, "not available") {
					t.Logf("[%s] correctly reported documentation unavailable on this system", sys.Name)
					return // expected path for systems like S/4 with inactive ICF node
				}
				t.Fatalf("[%s] unexpected error (not the SICF message): %v", sys.Name, err)
			}

			t.Logf("[%s] response length: %d bytes", sys.Name, len(text))
			if len(text) > 500 {
				t.Logf("[%s] first 500 bytes: %s", sys.Name, text[:500])
			} else {
				t.Logf("[%s] full text: %s", sys.Name, text)
			}

			// Homepage markers — should NOT appear in keyword-specific docs.
			homepageMarkers := []string{
				"ABAP - Dictionary",
				"ABAP - Core Data Services",
				"ABAP - RAP Business Objects",
				"Programming Guidelines",
			}
			for _, marker := range homepageMarkers {
				if strings.Contains(text, marker) {
					t.Errorf("[%s] response contains homepage marker %q", sys.Name, marker)
				}
			}

			// Keyword signals — at least one should be present.
			signals := []string{"DATA", "Daten", "variable", "Variable", "ABAP"}
			matched := false
			for _, s := range signals {
				if strings.Contains(text, s) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("[%s] response has no DATA keyword signals %v. Length=%d",
					sys.Name, signals, len(text))
			}
		})
	}
}
