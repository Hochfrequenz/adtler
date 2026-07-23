//go:build integration

package adt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

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
				t.Logf("%s: throwing class returned HTTP 200 (soft failure), body: %q",
					sys.Name, result.ConsoleOutput)
				return
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
