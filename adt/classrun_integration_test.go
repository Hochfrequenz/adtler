//go:build integration

package adt_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// classrunFixture is the global class in Z_ADT_MCP_TEST that implements
// IF_OO_ADT_CLASSRUN and writes classrunFixtureOutput to the console.
const (
	classrunFixture       = "ZCL_ADT_MCP_CLASSRUN_TST"
	classrunFixtureOutput = "CLASSRUN_OK"
	classrunThrowFixture  = "ZCL_ADT_MCP_CLASSRUN_ERR"
)

// classrunClassURI returns the ADT class URI for a classrun fixture name.
func classrunClassURI(name string) string {
	return "/sap/bc/adt/oo/classes/" + strings.ToLower(name)
}

// TestRunClass_Integration runs a real classrun class on every whitelisted
// system (R/3 and S/4 via eachSystem) and asserts the known console string
// comes back. This also exercises the classrun framework on each system —
// the endpoint handler CL_OO_ADT_RES_CLASSRUN is present on both HFQ/ECC and
// S4U (spec open verification point #2, resolved).
//
// The fixture-existence pre-check uses GetObjectInfo, NOT a 404 from RunClass:
// the handler returns HTTP 200 with an error string for a missing/invalid
// class (verified against CL_OO_ADT_RES_CLASSRUN), so a missing fixture would
// otherwise fail the ConsoleOutput assertion instead of skipping cleanly.
func TestRunClass_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := sys.Client.GetObjectInfo(ctx, classrunClassURI(classrunFixture)); err != nil {
				var adtErr *adt.ADTError
				if errors.As(err, &adtErr) && adtErr.StatusCode == 404 {
					t.Skipf("classrun fixture %s not present on %s (deliver it to Z_ADT_MCP_TEST first): %v",
						classrunFixture, sys.Name, err)
				}
				t.Fatalf("GetObjectInfo pre-check for %s on %s failed: %v", classrunFixture, sys.Name, err)
			}
			result, err := sys.Client.RunClass(ctx, classrunFixture)
			if err != nil {
				t.Fatalf("RunClass on %s failed: %v", sys.Name, err)
			}
			if !strings.Contains(result.ConsoleOutput, classrunFixtureOutput) {
				t.Errorf("%s: console output %q does not contain %q",
					sys.Name, result.ConsoleOutput, classrunFixtureOutput)
			}
			t.Logf("%s classrun output: %q", sys.Name, result.ConsoleOutput)
		})
	}
}

// freshClassrunOutput is the marker a freshly created probe class writes.
const freshClassrunOutput = "RC106_FRESH_OK"

// classrunSource returns a minimal IF_OO_ADT_CLASSRUN implementation that
// writes marker to the console. Built with double-quoted strings (not a Go
// backtick literal) per the CLAUDE.md ABAP-fixture rule.
func classrunSource(className, marker string) string {
	l := strings.ToLower(className)
	return "CLASS " + l + " DEFINITION PUBLIC FINAL CREATE PUBLIC.\n" +
		"  PUBLIC SECTION.\n" +
		"    INTERFACES if_oo_adt_classrun.\n" +
		"ENDCLASS.\n\n" +
		"CLASS " + l + " IMPLEMENTATION.\n" +
		"  METHOD if_oo_adt_classrun~main.\n" +
		"    out->write( '" + marker + "' ).\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
}

// createTmpClassrunClass creates a fresh $TMP class that will implement
// IF_OO_ADT_CLASSRUN on the given client and registers cleanup to delete it.
// Give it a body with setClassrunSourceAndActivate. Returns the object URI.
func createTmpClassrunClass(t *testing.T, client adt.Client, name string) string {
	t.Helper()
	ctx := context.Background()
	uri := classrunClassURI(name)
	if err := client.CreateObject(ctx, "CLAS", name, "$TMP", "issue106 classrun regression probe", ""); err != nil {
		if _, ie := client.GetObjectInfo(ctx, uri); ie != nil {
			t.Fatalf("CreateObject %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		if err := client.DeleteObject(context.Background(), uri, "", ""); err != nil {
			t.Logf("cleanup delete %s failed: %v", name, err)
		}
	})
	return uri
}

// setClassrunSourceAndActivate replaces the class source with one that writes
// marker and re-activates it, all over ADT REST on the given (reused) client.
func setClassrunSourceAndActivate(t *testing.T, client adt.Client, uri, name, marker string) {
	t.Helper()
	ctx := context.Background()
	lock, err := client.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("LockObject %s (%s): %v", name, marker, err)
	}
	src, err := client.GetSource(ctx, uri)
	if err != nil {
		_ = client.UnlockObject(ctx, uri, lock)
		t.Fatalf("GetSource %s (%s): %v", name, marker, err)
	}
	if _, err := client.SetSource(ctx, uri, classrunSource(name, marker), lock, "", src.ETag); err != nil {
		_ = client.UnlockObject(ctx, uri, lock)
		t.Fatalf("SetSource %s (%s): %v", name, marker, err)
	}
	_ = client.UnlockObject(ctx, uri, lock)
	res, err := client.ActivateObjects(ctx, []string{uri})
	if err != nil {
		t.Fatalf("ActivateObjects %s (%s): %v", name, marker, err)
	}
	if !res.Success {
		t.Fatalf("activation of %s (%s) failed: %d messages", name, marker, len(res.Messages))
	}
}

// TestRunClass_FreshClass_Integration is the issue #106 defect-1 regression
// test. It drives the whole create -> set source -> activate -> run lifecycle
// over pure ADT REST in ONE long-lived client (the worn-session path that MCP
// takes), then RunClass, and asserts the class's REAL output comes back on
// BOTH systems:
//
//   - HFQ (ECC/R3): classrun always worked here because activation regenerates
//     a persistent runtime load. This arm is the REGRESSION GUARD — the
//     fresh-session fix must not break the system that was already fine.
//   - S4U (S/4): activation does not regenerate the load, so pre-fix RunClass
//     in the reused session soft-failed with "does not implement ...main...".
//     Post-fix RunClass runs the classrun on an isolated fresh session and
//     returns the real output. This arm is the FIX.
//
// Asserting identical correct output on both systems is the point: same
// lifecycle, same assertion, green everywhere only once the fix is in.
func TestRunClass_FreshClass_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			// A fresh, uniquely-named $TMP class so the run really exercises a
			// never-executed class (defect-1 precondition).
			name := fmt.Sprintf("ZCL_RC106_FRESH_%d", time.Now().UnixNano()%10000000000000)
			uri := createTmpClassrunClass(t, sys.Client, name)
			setClassrunSourceAndActivate(t, sys.Client, uri, name, freshClassrunOutput)

			// Same client that just created + activated the class. Pre-fix this
			// soft-fails on S/4; post-fix RunClass uses a fresh session.
			result, err := sys.Client.RunClass(ctx, name)
			if err != nil {
				t.Fatalf("RunClass %s on %s failed: %v", name, sys.Name, err)
			}
			if !strings.Contains(result.ConsoleOutput, freshClassrunOutput) {
				t.Errorf("%s: RunClass output %q does not contain %q — the fresh-session fix did not generate the load",
					sys.Name, result.ConsoleOutput, freshClassrunOutput)
			}
			t.Logf("%s: fresh-class RunClass output: %q", sys.Name, result.ConsoleOutput)
		})
	}
}

// TestRunClass_ReactivatedClass_Integration is the issue #106 defect-2
// regression test. Over ONE reused client it changes a class's source and
// re-activates it repeatedly, and asserts each RunClass returns the CURRENT
// version's output, never a stale previously-generated one.
//
// Pre-fix, S/4 kept serving the first generated version to the reused session
// (defect 2); the fresh-session fix makes each RunClass compile the current
// active source, so every version cycle returns its own output on BOTH systems
// (HFQ regression guard, S4U the fix). Defect 2 is thus resolved by the same
// Option-C change as defect 1.
func TestRunClass_ReactivatedClass_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			name := fmt.Sprintf("ZCL_RC106_REACT_%d", time.Now().UnixNano()%10000000000000)
			uri := createTmpClassrunClass(t, sys.Client, name)

			// Change source + re-activate between runs, all on the same client.
			for _, marker := range []string{"RC106_REACT_ONE", "RC106_REACT_TWO", "RC106_REACT_THREE"} {
				setClassrunSourceAndActivate(t, sys.Client, uri, name, marker)
				result, err := sys.Client.RunClass(ctx, name)
				if err != nil {
					t.Fatalf("%s: RunClass after activating %s failed: %v", sys.Name, marker, err)
				}
				if !strings.Contains(result.ConsoleOutput, marker) {
					t.Errorf("%s: DEFECT 2 — after activating %s, RunClass returned %q (stale)",
						sys.Name, marker, strings.TrimSpace(result.ConsoleOutput))
				}
				t.Logf("%s: after activating %s -> %q", sys.Name, marker, strings.TrimSpace(result.ConsoleOutput))
			}
		})
	}
}

// TestRunClass_ThrowingClass confirms how an uncaught runtime exception in
// main() is signalled — spec open verification point #1. Verified against
// CL_OO_ADT_RES_CLASSRUN: main() runs inside a TRY that catches only
// cx_sy_create_object_error, so a genuine uncaught runtime exception (e.g.
// cx_sy_zerodivide) propagates and the ADT REST framework turns it into a
// non-2xx HTTP error -> *adt.ADTError. "Soft" failures (missing S_DEVELOP
// auth, class does not implement the interface) instead come back as HTTP 200
// with the error text in the body.
//
// The test therefore EXPECTS an ADTError for the throwing fixture, and logs
// the observed status/body so the exact status code can be pinned. If the
// fixture instead returns 200, that means it failed "softly" rather than
// throwing — adjust the fixture, not the client.
func TestRunClass_ThrowingClass(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			if _, err := sys.Client.GetObjectInfo(ctx, classrunClassURI(classrunThrowFixture)); err != nil {
				t.Skipf("throwing fixture %s not present on %s: %v",
					classrunThrowFixture, sys.Name, err)
			}
			result, err := sys.Client.RunClass(ctx, classrunThrowFixture)
			if err == nil {
				t.Fatalf("%s: expected RunClass to fail for throwing fixture, got HTTP 200 body: %q",
					sys.Name, result.ConsoleOutput)
			}
			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("%s: expected *adt.ADTError, got %T: %v", sys.Name, err, err)
			}
			t.Logf("%s: throwing class surfaced as ADTError, status %d: %s",
				sys.Name, adtErr.StatusCode, adtErr.Message)
			if adtErr.StatusCode < 500 {
				t.Logf("%s: NOTE status %d is below 5xx — record it and tighten this assertion",
					sys.Name, adtErr.StatusCode)
			}
		})
	}
}
