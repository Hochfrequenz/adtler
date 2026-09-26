//go:build integration && transport

package adt_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
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
	// Clear any stale S/4 ESRDIRE session locks left by a previous failed run.
	_ = client.Logout(ctx)

	const (
		objName  = "Z_ADT_MCP_ROLLBACK_B"
		v1Source = "REPORT Z_ADT_MCP_ROLLBACK_B.\n\" v1 marker\n"
		v2Source = "REPORT Z_ADT_MCP_ROLLBACK_B.\n\" v2 marker\n"
	)
	objectURI := "/sap/bc/adt/programs/programs/" + objName

	// ── Phase 1: Create T1, create object, write v1 source, activate ──────
	t1, err := client.CreateTransport(ctx, "K", "DUM", "MCP Rollback test T1", testPackage)
	if err != nil {
		t.Fatalf("[1] CreateTransport T1: %v", err)
	}
	t.Logf("[1] T1=%s", t1)
	t.Cleanup(func() {
		if r, releaseErr := client.ReleaseTransportVerified(context.Background(), t1, true); releaseErr != nil {
			t.Logf("cleanup: ReleaseTransportVerified(%s) failed: %v", t1, releaseErr)
		} else if !r.Released {
			t.Logf("cleanup: %s still modifiable after release (ECC silent-release-failure) — release via SE09 or the next run will find the fixture locked", t1)
		}
	})

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
	//
	// ReleaseTransportWithTasks reports success whenever the release endpoint
	// returns 2xx — but ECC has a documented silent-failure mode where the
	// request stays modifiable despite that 2xx (see transport.go's
	// ReleaseTransportVerified doc). Use the verified call and fail loudly
	// instead of racing into Phase 3 against an object still locked in T1.
	releaseResult, err := client.ReleaseTransportVerified(ctx, t1, true)
	if err != nil {
		t.Fatalf("[2] ReleaseTransportVerified T1: %v", err)
	}
	t1Free := releaseResult.Released
	if t1Free {
		t.Logf("[2] T1 released")
	}

	// ECC's silent-release-failure (documented on ReleaseTransportVerified in
	// transport.go): release itself is not impossible on this system, just not
	// reachable via ADT. Give a manual release (e.g. via SE09) a window to land
	// before falling back further — matching the poll pattern
	// TestReleaseTransport_Integration already uses for background-job
	// completion, just wider, since a human has to act within it.
	const manualReleasePollInterval = 10 * time.Second
	const manualReleasePollAttempts = 6 // ~60s
	for i := 0; !t1Free && i < manualReleasePollAttempts; i++ {
		t.Logf("[2] T1 (%s) still modifiable after ADT release — release it manually now if you want Phase 3/4 to run for real; polling for up to %s", t1, manualReleasePollInterval*manualReleasePollAttempts)
		time.Sleep(manualReleasePollInterval)
		info, infoErr := client.GetTransportInfo(ctx, t1)
		if infoErr != nil {
			continue
		}
		// Require a terminal released status specifically — this system has an
		// intermediate non-modifiable state ("O", release started/cofile export
		// in progress) where the object is still locked, so "!= D" alone is not
		// enough to prove Phase 3 can proceed. "R" is this system's actual
		// terminal status (observed live); TestReleaseTransport_Integration
		// already treats "L" or "R" as released for the same reason.
		if info.Status == adt.TransportStatusReleased || info.Status == "R" {
			t1Free = true
			t.Logf("[2] T1 released (manually, observed after %d poll(s), status=%q)", i+1, info.Status)
		} else {
			t.Logf("[2] T1 status=%q (not yet released), continuing to poll", info.Status)
		}
	}

	if !t1Free {
		// The object still needs freeing from T1 for Phase 3, so try
		// RemoveFromTransport as the only other ADT-only path before giving up.
		// This is not the #149 fallback-swallows-a-never-worked-request pattern:
		// we only reach here after a positive, typed signal (Released == false,
		// confirmed by polling) that release genuinely didn't happen, and if
		// RemoveFromTransport also can't run, we t.Skip — visibly, not a silent
		// pass.
		tasks, taskErr := client.GetTransportTasks(ctx, t1)
		if taskErr != nil {
			t.Fatalf("[2] T1 (%s) still modifiable after release, and GetTransportTasks failed: %v", t1, taskErr)
		}
		removed := false
		var lastErr error
		for _, task := range tasks {
			if rmErr := client.RemoveFromTransport(ctx, task, t1, "R3TR", "PROG", objName, "PROG/P", ""); rmErr != nil {
				lastErr = rmErr
				continue
			}
			removed = true
			break
		}
		if !removed {
			var adtErr *adt.ADTError
			if errors.As(lastErr, &adtErr) && adtErr.Type == adt.ExceptionTypeRemoveObjectUnsupported {
				t.Skipf("[2] T1 (%s) still modifiable after release (ECC silent-release-failure), and this system's ADT predates the remove-object operation (AS ABAP 7.53 SP00 / ABAP Platform 1809) — neither ADT-only path can free %s from T1 on this system, and no manual release landed within the poll window. Release T1 manually (SE09) and rerun, or run against a newer system.", t1, objName)
			}
			t.Fatalf("[2] T1 (%s) still modifiable after release, and RemoveFromTransport could not free %s: %v", t1, objName, lastErr)
		}
		t.Logf("[2] T1 still modifiable, but %s freed from it via RemoveFromTransport", objName)
	}
	// On S/4, ReleaseTransportWithTasks leaves a session-bound lock on the transport
	// organizer. Logout clears it so the next CreateTransport can proceed.
	_ = client.Logout(ctx)

	// ── Phase 3: Create T2, add object, write v2 source, activate ──────────
	t2, err := client.CreateTransport(ctx, "K", "DUM", "MCP Rollback test T2", testPackage)
	if err != nil {
		t.Fatalf("[3] CreateTransport T2: %v", err)
	}
	t.Logf("[3] T2=%s", t2)
	t.Cleanup(func() {
		bgCtx := context.Background()
		// Remove the object from all of T2's tasks before releasing T2.
		// On S/4, releasing a transport that contains an object permanently locks
		// that object (until the transport is imported to a target system). By
		// removing it from the task list first, we leave the object free for the
		// next test run without deleting it.
		if tasks, taskErr := client.GetTransportTasks(bgCtx, t2); taskErr == nil {
			for _, task := range tasks {
				if rmErr := client.RemoveFromTransport(bgCtx, task, t2, "R3TR", "PROG", objName, "PROG/P", ""); rmErr != nil {
					t.Logf("cleanup: RemoveFromTransport(%s, %s, %s): %v", task, t2, objName, rmErr)
				} else {
					t.Logf("cleanup: removed %s from task %s", objName, task)
				}
			}
		}
		if r, releaseErr := client.ReleaseTransportVerified(bgCtx, t2, true); releaseErr != nil {
			t.Logf("cleanup: ReleaseTransportVerified(%s) failed: %v", t2, releaseErr)
		} else if !r.Released {
			t.Logf("cleanup: %s still modifiable after release (ECC silent-release-failure) — release via SE09 or the next run will find the fixture locked", t2)
		} else {
			t.Logf("cleanup: released T2 (%s)", t2)
		}
		_ = client.Logout(bgCtx)
	})

	// No explicit AddToTransport needed: SetSource records the change in T2 automatically.
	// (AddToTransport's /abaptransportcomponents path is not supported on S/4.)
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
}
