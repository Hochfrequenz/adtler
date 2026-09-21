//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

func TestGetIncludeSource_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	includes := []string{"testclasses", "definitions", "implementations"}
	for _, include := range includes {
		t.Run(include, func(t *testing.T) {
			result, err := client.GetIncludeSource(ctx, testClassURI, include)
			if err != nil {
				if strings.Contains(err.Error(), "404") {
					t.Skipf("include %s not available: %v", include, err)
				}
				t.Fatalf("GetIncludeSource(%s): %v", include, err)
			}
			t.Logf("%s: %d bytes, etag=%s", include, len(result.Source), result.ETag)
		})
	}
}

func TestCreateAndWriteTestInclude_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	// Lock the class (stateful session — required for include operations).
	lockHandle, err := client.LockObject(ctx, testClassURI)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	defer func() { _ = client.UnlockObject(ctx, testClassURI, lockHandle) }()

	// Create test include — requires stateful lock on the class.
	t.Log("Creating test include...")
	err = client.CreateTestInclude(ctx, testClassURI, lockHandle, "")
	if err != nil {
		t.Fatalf("CreateTestInclude: %v", err)
	}
	t.Log("CreateTestInclude: OK")

	// Write test class source to the new include.
	testSource := "CLASS lcl_mcp_test DEFINITION FOR TESTING RISK LEVEL HARMLESS DURATION SHORT.\n" +
		"  PRIVATE SECTION.\n" +
		"    METHODS test_pass FOR TESTING.\n" +
		"ENDCLASS.\n\n" +
		"CLASS lcl_mcp_test IMPLEMENTATION.\n" +
		"  METHOD test_pass.\n" +
		"    cl_abap_unit_assert=>assert_equals( act = 1 exp = 1 ).\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
	_, err = client.SetIncludeSource(ctx, testClassURI, "testclasses", testSource, lockHandle, "", "")
	if err != nil {
		t.Fatalf("SetIncludeSource(testclasses): %v", err)
	}
	t.Log("SetIncludeSource(testclasses): OK")

	// Read back and verify.
	result, err := client.GetIncludeSource(ctx, testClassURI, "testclasses")
	if err != nil {
		t.Fatalf("GetIncludeSource(testclasses): %v", err)
	}
	if !strings.Contains(result.Source, "lcl_mcp_test") {
		t.Errorf("expected lcl_mcp_test in source, got: %s", result.Source)
	}
	t.Logf("testclasses: %d bytes, etag=%s", len(result.Source), result.ETag)
}

// TestSetIncludeSource_WithGetDerivedETag_Integration is the regression guard for
// aibap.mcp#436. It reproduces the exact failing pattern the MCP wrapper produced:
// GET the include (capturing its ETag), then write it back passing that ETag AND a
// lock handle. Before the fix this returned 412 ExceptionPreconditionFailed because
// the GET-derived ETag never matches SAP's class-level write precondition. After the
// fix, SetIncludeSource omits If-Match when a lock handle is present and the write
// succeeds. The pre-fix path (etag="") is already covered by the test above; this one
// specifically feeds a non-empty GET-derived ETag, which is what actually broke.
func TestSetIncludeSource_WithGetDerivedETag_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	lockHandle, err := client.LockObject(ctx, testClassURI)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	defer func() { _ = client.UnlockObject(ctx, testClassURI, lockHandle) }()

	const include = "testclasses"
	before, err := client.GetIncludeSource(ctx, testClassURI, include)
	if err != nil {
		t.Fatalf("GetIncludeSource(%s): %v", include, err)
	}
	if before.ETag == "" {
		t.Fatalf("GetIncludeSource returned empty ETag — cannot exercise the #436 path")
	}
	t.Logf("GET-derived ETag: %s", before.ETag)

	// Write the same source back, feeding the GET-derived ETag + lock handle.
	// This is the call that returned 412 before the fix.
	if _, err := client.SetIncludeSource(ctx, testClassURI, include, before.Source, lockHandle, "", before.ETag); err != nil {
		t.Fatalf("SetIncludeSource with GET-derived ETag failed (regression of #436): %v", err)
	}
	t.Log("SetIncludeSource with GET-derived ETag + lock: OK")
}
