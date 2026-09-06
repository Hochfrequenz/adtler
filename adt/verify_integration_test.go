//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestVerifySource_Integration exercises the $TMP create→set→syntax-check→delete
// round-trip against live SAP on every whitelisted system. The temp program is
// created and removed by VerifySource itself, so no fixture or transport is
// needed.
func TestVerifySource_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Valid source: a self-contained report. The program name in the
			// REPORT statement need not match the (random) temp program name;
			// the ADT syntax check does not hard-error on that.
			validSrc := "REPORT zz_adtler_verify_probe.\n" +
				"DATA lv_x TYPE i.\n" +
				"lv_x = 1.\n" +
				"WRITE lv_x.\n"
			valid, msgs, err := sys.Client.VerifySource(ctx, validSrc)
			if err != nil {
				t.Fatalf("VerifySource(valid) error: %v", err)
			}
			if !valid {
				t.Errorf("expected valid=true for clean source, got messages: %+v", msgs)
			}

			// Invalid source: references an unknown type → a syntax error.
			invalidSrc := "REPORT zz_adtler_verify_probe.\n" +
				"DATA lv_bad TYPE z_adtler_no_such_type_xyz.\n"
			valid2, msgs2, err := sys.Client.VerifySource(ctx, invalidSrc)
			if err != nil {
				t.Fatalf("VerifySource(invalid) error: %v", err)
			}
			if valid2 {
				t.Errorf("expected valid=false for broken source, got valid=true (msgs: %+v)", msgs2)
			}
			hasError := false
			for _, m := range msgs2 {
				if m.Type == "E" {
					hasError = true
				}
				t.Logf("  [%s] %s (line %d)", m.Type, m.Text, m.Line)
			}
			if !hasError {
				t.Errorf("expected at least one E (error) message for broken source, got: %+v", msgs2)
			}
		})
	}
}
