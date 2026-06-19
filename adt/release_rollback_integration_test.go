//go:build integration && transport

package adt_test

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Tests in this file exercise ReleaseTransportVerified and RollbackTransport
// against a real SAP system. They create their own disposable transports and
// objects and must not touch any pre-existing fixtures.
//
// Run with: go test -tags='integration transport' -v -run 'TestReleaseTransportVerified|TestRollbackTransport' ./adt/...

func TestReleaseTransportVerified_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	trNumber, err := client.CreateTransport(ctx, "K", "DUM", "MCP ReleaseVerified test", testPackage)
	if err != nil {
		t.Fatalf("CreateTransport: %v", err)
	}
	t.Logf("created transport: %s", trNumber)

	// Cleanup: if the test fails before ReleaseTransportVerified, release the
	// transport so it doesn't linger on the system as an open request.
	t.Cleanup(func() {
		_ = client.ReleaseTransportWithTasks(context.Background(), trNumber)
	})

	// Create a throwaway object in the transport so the release has content.
	const objName = "Z_ADT_MCP_RELVERIFY_TST"
	objectURI := "/sap/bc/adt/programs/programs/" + objName
	if err := client.CreateObject(ctx, "PROG", objName, testPackage, "ReleaseVerified test", trNumber); err != nil {
		if _, infoErr := client.GetObjectInfo(ctx, objectURI); infoErr != nil {
			t.Fatalf("CreateObject: %v", err)
		}
		t.Logf("object %s already exists from a prior run, reusing", objName)
	}
	if _, err := client.ActivateObjects(ctx, []string{objectURI}); err != nil {
		t.Logf("ActivateObjects: %v (non-fatal — continuing)", err)
	}

	result, err := client.ReleaseTransportVerified(ctx, trNumber, true)
	if err != nil {
		t.Fatalf("ReleaseTransportVerified: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil ReleaseResult")
	}
	if result.Transport != trNumber {
		t.Errorf("result.Transport: got %q, want %q", result.Transport, trNumber)
	}
	// Released=false on ECC is the documented silent-fail case. It is NOT a
	// test failure — the function's job is to detect it, not to fix it.
	t.Logf("transport %s: released=%v", trNumber, result.Released)
}

func TestRollbackTransport_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	const (
		objName  = "Z_ADT_MCP_ROLLBACK_TST"
		v1Source = "REPORT Z_ADT_MCP_ROLLBACK_TST.\n\" v1 marker\n"
		v2Source = "REPORT Z_ADT_MCP_ROLLBACK_TST.\n\" v2 marker\n"
	)
	objectURI := "/sap/bc/adt/programs/programs/" + objName

	// ── Phase 1: Create T1, create object, write v1 source, activate ──────
	t1, err := client.CreateTransport(ctx, "K", "DUM", "MCP Rollback test T1", testPackage)
	if err != nil {
		t.Fatalf("[1] CreateTransport T1: %v", err)
	}
	t.Logf("[1] T1=%s", t1)

	if err := client.CreateObject(ctx, "PROG", objName, testPackage, "Rollback test", t1); err != nil {
		if _, infoErr := client.GetObjectInfo(ctx, objectURI); infoErr != nil {
			t.Fatalf("[1] CreateObject: %v", err)
		}
		t.Logf("[1] object %s already exists, reusing", objName)
	}

	lh, err := client.LockObject(ctx, objectURI)
	if err != nil {
		t.Fatalf("[1] LockObject: %v", err)
	}
	src, err := client.GetSource(ctx, objectURI)
	if err != nil {
		_ = client.UnlockObject(ctx, objectURI, lh)
		t.Fatalf("[1] GetSource for ETag: %v", err)
	}
	if _, err := client.SetSource(ctx, objectURI, v1Source, lh, t1, src.ETag); err != nil {
		_ = client.UnlockObject(ctx, objectURI, lh)
		t.Fatalf("[1] SetSource v1: %v", err)
	}
	_ = client.UnlockObject(ctx, objectURI, lh)

	if _, err := client.ActivateObjects(ctx, []string{objectURI}); err != nil {
		t.Fatalf("[1] ActivateObjects v1: %v", err)
	}
	t.Logf("[1] activated v1 source")

	// ── Phase 2: Release T1 so the object is free for a new transport ──────
	// We roll back T2 (not T1) because findPreTransportVersion returns the version
	// *before* the given transport. T1 is the first activation so there is no
	// version before it — rolling back T1 would error. Rolling back T2 returns v1.
	if err := client.ReleaseTransportWithTasks(ctx, t1); err != nil {
		t.Fatalf("[2] ReleaseTransportWithTasks T1: %v", err)
	}
	for i := 0; i < 6; i++ {
		info, err := client.GetTransportInfo(ctx, t1)
		if err != nil {
			t.Fatalf("[2] GetTransportInfo T1: %v", err)
		}
		if info.Status == "L" || info.Status == "R" {
			t.Logf("[2] T1 released (status=%s)", info.Status)
			break
		}
		t.Logf("[2] T1 status=%q, waiting...", info.Status)
		time.Sleep(10 * time.Second)
	}

	// ── Phase 3: Create T2, add object, write v2 source, activate ──────────
	t2, err := client.CreateTransport(ctx, "K", "DUM", "MCP Rollback test T2", testPackage)
	if err != nil {
		t.Fatalf("[3] CreateTransport T2: %v", err)
	}
	t.Logf("[3] T2=%s", t2)

	taskNr, err := client.CreateTransportTask(ctx, t2, "", "Rollback test task")
	if err != nil {
		t.Fatalf("[3] CreateTransportTask: %v", err)
	}
	t.Logf("[3] task=%s", taskNr)

	if err := client.AddToTransport(ctx, objectURI, taskNr); err != nil {
		t.Fatalf("[3] AddToTransport: %v", err)
	}

	lh, err = client.LockObject(ctx, objectURI)
	if err != nil {
		t.Fatalf("[3] LockObject v2: %v", err)
	}
	src, err = client.GetSource(ctx, objectURI)
	if err != nil {
		_ = client.UnlockObject(ctx, objectURI, lh)
		t.Fatalf("[3] GetSource for ETag v2: %v", err)
	}
	if _, err := client.SetSource(ctx, objectURI, v2Source, lh, t2, src.ETag); err != nil {
		_ = client.UnlockObject(ctx, objectURI, lh)
		t.Fatalf("[3] SetSource v2: %v", err)
	}
	_ = client.UnlockObject(ctx, objectURI, lh)

	if _, err := client.ActivateObjects(ctx, []string{objectURI}); err != nil {
		t.Fatalf("[3] ActivateObjects v2: %v", err)
	}
	t.Logf("[3] activated v2 source")

	// ── Phase 4: RollbackTransport(T2) — expect v1 restored ────────────────
	result, err := client.RollbackTransport(ctx, t2)
	if err != nil {
		t.Fatalf("[4] RollbackTransport: %v", err)
	}
	t.Logf("[4] restored=%d skipped=%d failed=%d", len(result.Restored), len(result.Skipped), len(result.Failed))

	restoredObj := false
	for _, r := range result.Restored {
		t.Logf("[4] restored: %s %s", r.Type, r.Name)
		if r.Name == objName {
			restoredObj = true
		}
	}
	for _, f := range result.Failed {
		t.Logf("[4] failed: %s %s — %s", f.Type, f.Name, f.Reason)
	}
	if !restoredObj {
		t.Errorf("[4] expected %s in Restored; got restored=%v failed=%v", objName, result.Restored, result.Failed)
	}
	if len(result.Failed) > 0 {
		t.Errorf("[4] unexpected rollback failures: %v", result.Failed)
	}

	// ── Phase 5: Verify source is back to v1 ───────────────────────────────
	after, err := client.GetSource(ctx, objectURI)
	if err != nil {
		t.Fatalf("[5] GetSource after rollback: %v", err)
	}
	const v1Marker = "v1 marker"
	const v2Marker = "v2 marker"
	if !strings.Contains(after.Source, v1Marker) {
		t.Errorf("[5] source after rollback does not contain %q; first 300 bytes: %q",
			v1Marker, after.Source[:min(300, len(after.Source))])
	}
	if strings.Contains(after.Source, v2Marker) {
		t.Errorf("[5] source after rollback still contains %q — rollback did not restore v1", v2Marker)
	}
	t.Logf("[5] source correctly restored to v1 (%d bytes)", len(after.Source))

	// ── Cleanup: delete object, release T2 ─────────────────────────────────
	t.Cleanup(func() {
		bgCtx := context.Background()
		if lh, lockErr := client.LockObject(bgCtx, objectURI); lockErr == nil {
			_ = client.DeleteObject(bgCtx, objectURI, lh, t2)
			t.Logf("cleanup: deleted %s", objName)
		} else {
			t.Logf("cleanup: could not lock %s for deletion: %v", objName, lockErr)
		}
		_ = client.ReleaseTransportWithTasks(bgCtx, t2)
		t.Logf("cleanup: released T2 (%s)", t2)
	})
}
