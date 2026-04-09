//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestEachSystem_Smoke_Integration is a fix-independent smoke test for the
// eachSystem helper itself. Before any fix branch test relies on this
// helper, this test confirms:
//   - JSON config loads from the expected paths
//   - At least one system is returned
//   - SystemInfo() returns sensible host/client values per system
//   - Each returned client can authenticate and round-trip a basic ADT
//     metadata read against a standard SAP report (RSPARAM) — a code path
//     that depends on NO Wave 1 fix, so a failure here means the helper
//     itself is broken, not any fix that builds on top.
//
// Run with:
//   SAP_INTEGRATION_SYSTEMS="<r3-key>,<s4-key>" \
//     go test -tags=integration -v -run TestEachSystem_Smoke_Integration ./adt/...
func TestEachSystem_Smoke_Integration(t *testing.T) {
	systems := eachSystem(t)
	if len(systems) == 0 {
		t.Fatal("eachSystem returned an empty slice — it should t.Skip instead")
	}
	t.Logf("eachSystem yielded %d system(s)", len(systems))

	ctx := context.Background()
	for _, sys := range systems {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			host, sapClient := sys.Client.SystemInfo()
			if host == "" {
				t.Errorf("[%s] SystemInfo returned empty host", sys.Name)
			}
			t.Logf("[%s] host=%s client=%s", sys.Name, host, sapClient)

			// RSPARAM is the standard SAP "Display Profile Parameters" report.
			// It exists on every R/3 and S/4 release. GetObjectInfo uses
			// code paths unaffected by any Wave 1 fix, so this isolates
			// helper failures from fix failures.
			info, err := sys.Client.GetObjectInfo(ctx, "/sap/bc/adt/programs/programs/RSPARAM")
			if err != nil {
				t.Errorf("[%s] GetObjectInfo(RSPARAM) failed: %v", sys.Name, err)
				return
			}
			if info.Name == "" {
				t.Errorf("[%s] GetObjectInfo returned empty Name", sys.Name)
				return
			}
			if info.Type != "PROG/P" {
				t.Errorf("[%s] expected Type PROG/P, got %q", sys.Name, info.Type)
			}
			t.Logf("[%s] sanity OK: name=%s type=%s description=%q",
				sys.Name, info.Name, info.Type, info.Description)
		})
	}
}
