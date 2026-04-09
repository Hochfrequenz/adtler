//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestSyntaxCheck_FalsePositive_MultiSystem_Integration regression-tests
// adtler#11 against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Three sub-tests per system:
//
//  1. nonexistent_object — SyntaxCheck on a URI that doesn't exist. Before
//     the fix this returned the misleading "REPORT/PROGRAM statement is
//     missing" syntax error. After the fix it returns a clear "object does
//     not exist" error. Validated by the probe: both R/3 (German "fehlt")
//     and S/4 (English "missing") returned the same false-positive pattern.
//
//  2. active_report — SyntaxCheck on an existing, activated program
//     (RSPARAM — exists on every release). Before the fix, if RSPARAM had
//     no inactive version (which is the normal state for a standard SAP
//     program that's never been edited), SyntaxCheck returned the same
//     false-positive. After the fix it retries with version="active" and
//     returns clean results (or real syntax errors).
//
//  3. fixture_report — SyntaxCheck on the test fixture Z_ADT_MCP_TEST_REPORT
//     which HAS an inactive version (created by setupFixtures in TestMain).
//     This validates the happy path isn't broken by the retry logic.
func TestSyntaxCheck_FalsePositive_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {

			t.Run("nonexistent_object", func(t *testing.T) {
				const fakeURI = "/sap/bc/adt/programs/programs/z_adt_mcp_syntaxcheck_probe_xxx"
				_, err := sys.Client.SyntaxCheck(ctx, fakeURI)
				if err == nil {
					t.Fatalf("[%s] expected error for non-existent object, got nil", sys.Name)
				}
				msg := err.Error()
				t.Logf("[%s] error: %v", sys.Name, err)

				if strings.Contains(msg, "REPORT") || strings.Contains(msg, "PROGRAM") {
					if strings.Contains(msg, "missing") || strings.Contains(msg, "fehlt") {
						t.Errorf("[%s] the misleading 'REPORT/PROGRAM missing' false positive leaked through; "+
							"expected a 'does not exist' error instead. got: %v", sys.Name, err)
					}
				}
				if !strings.Contains(msg, "does not exist") && !strings.Contains(msg, "existiert nicht") && !strings.Contains(msg, "not found") {
					t.Errorf("[%s] error should indicate the object doesn't exist, got: %v", sys.Name, err)
				}
			})

			t.Run("active_report_RSPARAM", func(t *testing.T) {
				// RSPARAM is a standard activated program with no inactive version.
				// Before the fix, this triggered the false positive.
				msgs, err := sys.Client.SyntaxCheck(ctx, "/sap/bc/adt/programs/programs/rsparam")
				if err != nil {
					t.Fatalf("[%s] SyntaxCheck(RSPARAM): %v", sys.Name, err)
				}
				// RSPARAM is a valid, compilable program. Any returned message
				// should be a warning at most, not an error about REPORT missing.
				for _, m := range msgs {
					if m.Type == "E" && m.Line == 1 && m.Column == 0 &&
						(strings.Contains(m.Text, "REPORT") || strings.Contains(m.Text, "PROGRAM")) {
						t.Errorf("[%s] false-positive leaked through for RSPARAM: %+v", sys.Name, m)
					}
				}
				t.Logf("[%s] RSPARAM: %d messages (no false positive)", sys.Name, len(msgs))
			})

			t.Run("fixture_with_inactive", func(t *testing.T) {
				// Z_ADT_MCP_TEST_REPORT has an inactive version from setupFixtures.
				// The fix should NOT retry for this object — the inactive check
				// is the correct path. We just verify no panic/error.
				msgs, err := sys.Client.SyntaxCheck(ctx, testReportURI)
				if err != nil {
					// The fixture may not exist on all systems. Skip rather
					// than fail if GetObjectInfo returns 404.
					if strings.Contains(err.Error(), "does not exist") ||
						strings.Contains(err.Error(), "404") {
						t.Skipf("[%s] test fixture %s not found on this system", sys.Name, testReportURI)
					}
					t.Fatalf("[%s] SyntaxCheck(%s): %v", sys.Name, testReportURI, err)
				}
				t.Logf("[%s] %s: %d messages", sys.Name, testReportURI, len(msgs))
			})
		})
	}
}
