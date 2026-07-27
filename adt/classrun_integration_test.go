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

// freshClassrunSource returns a minimal IF_OO_ADT_CLASSRUN implementation that
// writes freshClassrunOutput. Built with double-quoted strings (not a Go
// backtick literal) per the CLAUDE.md ABAP-fixture rule.
func freshClassrunSource(className string) string {
	l := strings.ToLower(className)
	return "CLASS " + l + " DEFINITION PUBLIC FINAL CREATE PUBLIC.\n" +
		"  PUBLIC SECTION.\n" +
		"    INTERFACES if_oo_adt_classrun.\n" +
		"ENDCLASS.\n\n" +
		"CLASS " + l + " IMPLEMENTATION.\n" +
		"  METHOD if_oo_adt_classrun~main.\n" +
		"    out->write( '" + freshClassrunOutput + "' ).\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
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
			// never-executed class (defect-1 precondition), not a fixture whose
			// load some earlier run already generated.
			name := fmt.Sprintf("ZCL_RC106_FRESH_%d", time.Now().Unix()%100000)
			uri := classrunClassURI(name)

			if err := sys.Client.CreateObject(ctx, "CLAS", name, "$TMP",
				"issue106 defect-1 regression probe", ""); err != nil {
				if _, ie := sys.Client.GetObjectInfo(ctx, uri); ie != nil {
					t.Fatalf("CreateObject %s on %s: %v", name, sys.Name, err)
				}
			}
			t.Cleanup(func() {
				if err := sys.Client.DeleteObject(context.Background(), uri, "", ""); err != nil {
					t.Logf("%s: cleanup delete %s failed: %v", sys.Name, name, err)
				}
			})

			lock, err := sys.Client.LockObject(ctx, uri)
			if err != nil {
				t.Fatalf("LockObject %s on %s: %v", name, sys.Name, err)
			}
			src, err := sys.Client.GetSource(ctx, uri)
			if err != nil {
				_ = sys.Client.UnlockObject(ctx, uri, lock)
				t.Fatalf("GetSource %s on %s: %v", name, sys.Name, err)
			}
			if _, err := sys.Client.SetSource(ctx, uri, freshClassrunSource(name), lock, "", src.ETag); err != nil {
				_ = sys.Client.UnlockObject(ctx, uri, lock)
				t.Fatalf("SetSource %s on %s: %v", name, sys.Name, err)
			}
			_ = sys.Client.UnlockObject(ctx, uri, lock)

			res, err := sys.Client.ActivateObjects(ctx, []string{uri})
			if err != nil {
				t.Fatalf("ActivateObjects %s on %s: %v", name, sys.Name, err)
			}
			if !res.Success {
				t.Fatalf("%s: activation of %s failed: %d messages", sys.Name, name, len(res.Messages))
			}

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
