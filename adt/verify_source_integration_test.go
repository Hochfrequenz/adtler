//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestVerifySource_ValidABAP_Integration and TestVerifySource_BrokenABAP_Integration
// exercise the full VerifySource round-trip (create $TMP program, set source, syntax
// check, delete) against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// VerifySource is the portable way to syntax-check free-standing source on both R/3
// and S/4; the checkruns inline-body path is not supported on either release (see
// mcp-server-abap#126). Both tests must pass on R/3 and S/4 to confirm that the
// create/lock/set-source/unlock/check/delete sequence is functionally equivalent
// across systems.
func TestVerifySource_ValidABAP_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			valid, msgs, err := sys.Client.VerifySource(ctx, "REPORT zdummy.")
			if err != nil {
				t.Fatalf("VerifySource: %v", err)
			}
			if !valid {
				t.Errorf("expected valid=true for minimal valid ABAP, got false; messages: %v", msgs)
			}
			for _, m := range msgs {
				if m.Type == "E" {
					t.Errorf("unexpected E-type message: %s (line %d)", m.Text, m.Line)
				}
			}
			t.Logf("[%s] valid=%v, %d messages", sys.Name, valid, len(msgs))
		})
	}
}

func TestVerifySource_BrokenABAP_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// IF without ENDIF is a hard syntax error (E-type) on all SAP releases.
			// A type-conflict (e.g. DATA x TYPE i. x = 'abc'.) can be a W-type
			// warning on some releases, so we use the structural syntax error instead.
			valid, msgs, err := sys.Client.VerifySource(ctx, "REPORT zdummy.\nIF 1 = 1.")
			if err != nil {
				t.Fatalf("VerifySource: %v", err)
			}
			if valid {
				t.Errorf("expected valid=false for broken ABAP (missing ENDIF), got true; messages: %v", msgs)
			}
			hasError := false
			for _, m := range msgs {
				if m.Type == "E" {
					hasError = true
				}
				t.Logf("[%s] %s line=%d col=%d: %s", sys.Name, m.Type, m.Line, m.Column, m.Text)
			}
			if !hasError {
				t.Errorf("expected at least one E-type message for broken ABAP, got none in: %v", msgs)
			}
		})
	}
}
