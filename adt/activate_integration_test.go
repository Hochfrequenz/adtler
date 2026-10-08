//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestActivateObjects_Clean_Integration exercises ActivateObjects (which now
// always re-checks GetInactiveObjects on apparent success — see
// Hochfrequenz/aibap.mcp#500) against a real, already-consistent object on
// both R/3 and S/4. It cannot reproduce the exact SAP-side silent-no-op this
// fix targets (a namespaced CLAS in a transportable package on ECC), since
// the shared Z_ADT_MCP_TEST fixture package has no namespaced objects to
// exercise that precondition. That failure path is instead covered by the
// httptest-mocked tests in activate_silent_failure_test.go, which reproduce
// the mechanism directly. This test's job is only to confirm the added
// verification step does not regress the common, genuinely-successful
// activation path on either system.
func TestActivateObjects_Clean_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			result, err := sys.Client.ActivateObjects(ctx, []string{testReportURI})
			if err != nil {
				t.Fatalf("ActivateObjects failed: %v", err)
			}
			t.Logf("success=%v messages=%d", result.Success, len(result.Messages))
			if !result.Success {
				for _, m := range result.Messages {
					t.Logf("  [%s] %s (uri=%s)", m.Type, m.Text, m.ObjectURI)
				}
				t.Error("expected success for an already-active object")
			}
		})
	}
}

func TestGetInactiveObjects_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	objects, err := client.GetInactiveObjects(ctx)
	if err != nil {
		t.Fatalf("GetInactiveObjects: %v", err)
	}
	t.Logf("got %d inactive objects", len(objects))
	for i, o := range objects {
		if i >= 10 {
			t.Logf("  ... and %d more", len(objects)-10)
			break
		}
		t.Logf("  [%d] %s (%s) %s", i, o.Name, o.Type, o.URI)
	}
}

func TestActivateObjects_WithErrors_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	// Write invalid source, activate, check for errors, then restore.
	lockHandle, err := client.LockObject(ctx, testReportURI)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	t.Cleanup(func() {
		// Restore valid source and activate.
		lh, err := client.LockObject(context.Background(), testReportURI)
		if err != nil {
			return
		}
		src, _ := client.GetSource(context.Background(), testReportURI)
		validSource := "REPORT z_adt_mcp_test_report.\nWRITE: / 'Hello from MCP integration test'.\n"
		client.SetSource(context.Background(), testReportURI, validSource, lh, "", src.ETag)
		client.UnlockObject(context.Background(), testReportURI, lh)
		client.ActivateObjects(context.Background(), []string{testReportURI})
	})

	src, err := client.GetSource(ctx, testReportURI)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}

	invalidSource := "REPORT z_adt_mcp_test_report.\nTHIS IS NOT VALID ABAP.\n"
	_, err = client.SetSource(ctx, testReportURI, invalidSource, lockHandle, "", src.ETag)
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	_ = client.UnlockObject(ctx, testReportURI, lockHandle)

	// Activate should now report errors.
	result, err := client.ActivateObjects(ctx, []string{testReportURI})
	if err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	t.Logf("success=%v messages=%d", result.Success, len(result.Messages))
	for _, m := range result.Messages {
		t.Logf("  [%s] %s (uri=%s)", m.Type, m.Text, m.ObjectURI)
	}
	if result.Success {
		t.Error("expected Success=false for invalid ABAP source")
	}
	if len(result.Messages) == 0 {
		t.Error("expected at least one error message")
	}
}

// newActiveClassPool70 creates a $TMP class named prefix plus a timestamp,
// registers its deletion as cleanup, and gives it an active baseline source
// without a test-classes include. It returns the class name and its
// lower-case object URI.
func newActiveClassPool70(t *testing.T, c adt.Client, prefix string) (name, uri string) {
	t.Helper()
	ctx := context.Background()
	name = fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()%10000000000)
	uri = "/sap/bc/adt/oo/classes/" + strings.ToLower(name)

	if err := c.CreateObject(ctx, "CLAS", name, "$TMP", "adtler class-pool fixture", ""); err != nil {
		t.Fatalf("CreateObject %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := c.DeleteObject(context.Background(), uri, "", ""); err != nil {
			t.Logf("cleanup delete %s failed: %v", name, err)
		}
	})
	// S/4 leaves a session-bound ESRDIRE enqueue after CreateObject.
	_ = c.Logout(ctx)

	lock, err := c.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	src, err := c.GetSource(ctx, uri)
	if err == nil {
		_, err = c.SetSource(ctx, uri, classPool70Source(name, "baseline"), lock, "", src.ETag)
	}
	_ = c.UnlockObject(ctx, uri, lock)
	if err != nil {
		t.Fatalf("baseline source: %v", err)
	}
	if res, err := c.ActivateObjects(ctx, []string{uri}); err != nil || !res.Success {
		t.Fatalf("baseline activation: result=%+v err=%v", res, err)
	}
	return name, uri
}

// TestActivateObjects_ClassPoolSubIncludes_Integration is the regression guard
// for #70: activating a class pool after writing its test-classes include and
// its main source must leave no part of the class inactive, so that the unit
// tests run afterwards actually execute (#70 reported 0 passed / 0 failed /
// 0 errors because the test-classes include had stayed inactive).
//
// It activates through each URI a caller may plausibly hold for the class —
// the class itself, its main source and its test-classes include — and
// asserts after every round that GetInactiveObjects lists nothing under the
// class and that the single test method passes. A fresh $TMP class is created
// per system and deleted afterwards.
func TestActivateObjects_ClassPoolSubIncludes_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			c := sys.Client
			name, uri := newActiveClassPool70(t, c, "ZCL_ADT70")

			rounds := []struct {
				label         string
				activationURI string
			}{
				{"class", uri},
				{"source-main", uri + "/source/main"},
				{"testclasses-include", uri + "/includes/testclasses"},
			}
			for i, r := range rounds {
				writeClassPool70(t, c, uri, name, r.label, i == 0)

				pending := inactiveUnder(t, c, uri)
				if !slices.ContainsFunc(pending, equalFold(uri)) ||
					!slices.ContainsFunc(pending, equalFold(uri+"/includes/testclasses")) {
					t.Fatalf("%s: precondition: expected the class and its test-classes include to be inactive after the write, got %v",
						r.label, pending)
				}

				res, err := c.ActivateObjects(ctx, []string{r.activationURI})
				if err != nil {
					t.Fatalf("%s: ActivateObjects(%s): %v", r.label, r.activationURI, err)
				}
				if !res.Success {
					for _, m := range res.Messages {
						t.Logf("  [%s] %s (uri=%s)", m.Type, m.Text, m.ObjectURI)
					}
					t.Fatalf("%s: ActivateObjects(%s) reported failure", r.label, r.activationURI)
				}

				if left := inactiveUnder(t, c, uri); len(left) > 0 {
					t.Fatalf("%s: still inactive after activating %s: %v", r.label, r.activationURI, left)
				}

				tr, err := c.RunUnitTests(ctx, uri, 60)
				if err != nil {
					t.Fatalf("%s: RunUnitTests: %v", r.label, err)
				}
				if tr.Passed != 1 || tr.Failed != 0 || tr.Errors != 0 {
					t.Fatalf("%s: RunUnitTests = %d passed / %d failed / %d errors, want 1/0/0",
						r.label, tr.Passed, tr.Failed, tr.Errors)
				}
				t.Logf("%s: activated via %s, %d entries cleared, unit tests 1/0/0", r.label, r.activationURI, len(pending))
			}
		})
	}
}

// classPool70Source is the main source of the #70 test class. marker lands in
// a comment so every round produces a genuinely changed (inactive) version.
func classPool70Source(name, marker string) string {
	lname := strings.ToLower(name)
	return "CLASS " + lname + " DEFINITION PUBLIC FINAL CREATE PUBLIC.\n" +
		"  PUBLIC SECTION.\n" +
		"    METHODS get RETURNING VALUE(rv) TYPE i.\n" +
		"ENDCLASS.\n\n" +
		"CLASS " + lname + " IMPLEMENTATION.\n" +
		"  METHOD get.\n" +
		"    \" " + marker + "\n" +
		"    rv = 1.\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
}

// classPool70TestSource is the test-classes include of the #70 test class: one
// test method that passes only if it runs against the class.
func classPool70TestSource(name, marker string) string {
	return "\" " + marker + "\n" +
		"CLASS ltc_get DEFINITION FOR TESTING RISK LEVEL HARMLESS DURATION SHORT.\n" +
		"  PRIVATE SECTION.\n" +
		"    METHODS returns_one FOR TESTING.\n" +
		"ENDCLASS.\n\n" +
		"CLASS ltc_get IMPLEMENTATION.\n" +
		"  METHOD returns_one.\n" +
		"    cl_abap_unit_assert=>assert_equals( act = NEW " + strings.ToLower(name) + "( )->get( ) exp = 1 ).\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
}

// writeClassPool70 writes the test-classes include (creating it first when
// createInclude is set) and a changed main source under one lock, leaving the
// class with pending inactive versions. It always unlocks before failing.
func writeClassPool70(t *testing.T, c adt.Client, uri, name, marker string, createInclude bool) {
	t.Helper()
	ctx := context.Background()
	lock, err := c.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("%s: LockObject: %v", marker, err)
	}
	if createInclude {
		err = c.CreateTestInclude(ctx, uri, lock, "")
	}
	if err == nil {
		_, err = c.SetIncludeSource(ctx, uri, "testclasses", classPool70TestSource(name, marker), lock, "", "")
	}
	var src *adt.SourceResult
	if err == nil {
		src, err = c.GetSource(ctx, uri)
	}
	if err == nil {
		_, err = c.SetSource(ctx, uri, classPool70Source(name, marker), lock, "", src.ETag)
	}
	_ = c.UnlockObject(ctx, uri, lock)
	if err != nil {
		t.Fatalf("%s: writing class pool: %v", marker, err)
	}
}

// inactiveUnder returns the GetInactiveObjects URIs that are uri itself or
// nested under it. SAP lists them lower-case and with "#type=…" fragments for
// method and source-unit entries, hence the case-insensitive prefix match with
// a boundary check.
func inactiveUnder(t *testing.T, c adt.Client, uri string) []string {
	t.Helper()
	objs, err := c.GetInactiveObjects(context.Background())
	if err != nil {
		t.Fatalf("GetInactiveObjects: %v", err)
	}
	prefix := strings.ToLower(uri)
	var out []string
	for _, o := range objs {
		u := strings.ToLower(o.URI)
		if u == prefix || (strings.HasPrefix(u, prefix) && strings.ContainsRune("/#?", rune(u[len(prefix)]))) {
			out = append(out, o.URI)
		}
	}
	return out
}

// equalFold returns a predicate for slices.ContainsFunc that matches want
// case-insensitively.
func equalFold(want string) func(string) bool {
	return func(u string) bool { return strings.EqualFold(u, want) }
}
