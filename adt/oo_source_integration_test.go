//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestSetSource_OOCreateWrite_Integration is the regression guard for
// aibap.mcp#443: writing the source of a class or interface must succeed.
// Before the fix the write failed 403 ExceptionResourceNoAccess ("currently
// editing") because the lock handle was delivered as a header, but OO handlers
// read the ?lockHandle= query param — so the header-first attempt was treated as
// unlocked. trySetSource now retries with query delivery on that 403. Verified
// on HF S/4 Mandant 100.
//
// The fixtures are persistent (created once, reused thereafter, never deleted):
// deleting can leave a dangling CTS registration (#442) that makes a later
// create collide, so we reuse instead of churn. Any lock is released after each
// run. If a fixture is both absent AND un-creatable (a pre-existing dangling
// registration), the subtest skips — that is #442, not the #443 delivery bug
// this guards.
func TestSetSource_OOCreateWrite_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	tr, err := client.CreateTransport(ctx, "K", "", "aibap #443 OO write guard", testPackage)
	if err != nil {
		t.Fatalf("CreateTransport: %v", err)
	}

	cases := []struct{ typ, name, uri, src string }{
		{"CLAS", "ZCL_ADT_MCP_OOWRITE", "/sap/bc/adt/oo/classes/zcl_adt_mcp_oowrite",
			"CLASS zcl_adt_mcp_oowrite DEFINITION PUBLIC FINAL CREATE PUBLIC.\n  PUBLIC SECTION.\nENDCLASS.\nCLASS zcl_adt_mcp_oowrite IMPLEMENTATION.\nENDCLASS.\n"},
		{"INTF", "ZIF_ADT_MCP_OOWRITE", "/sap/bc/adt/oo/interfaces/zif_adt_mcp_oowrite",
			"INTERFACE zif_adt_mcp_oowrite PUBLIC.\n  METHODS noop.\nENDINTERFACE.\n"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.typ, func(t *testing.T) {
			if err := client.CreateObject(ctx, c.typ, c.name, testPackage, "aibap #443 guard", tr); err != nil {
				if _, e := client.GetObjectInfo(ctx, c.uri); e != nil {
					// Not creatable and not present → pre-existing dangling CTS
					// registration (#442), unrelated to the #443 delivery fix.
					t.Skipf("%s absent and CreateObject failed (likely #442 dangling registration): %v", c.name, err)
				}
				t.Logf("%s exists, reusing", c.name)
			}

			h, err := client.LockObject(ctx, c.uri)
			if err != nil {
				t.Fatalf("LockObject: %v", err)
			}
			defer func() { _ = client.UnlockObject(context.Background(), c.uri, h) }()

			src, err := client.GetSource(ctx, c.uri)
			if err != nil {
				t.Fatalf("GetSource: %v", err)
			}
			// The write that returned 403 "currently editing" before the fix.
			if _, err := client.SetSource(ctx, c.uri, c.src, h, tr, src.ETag); err != nil {
				if strings.Contains(err.Error(), "locked in request") {
					t.Skipf("%s has a dangling transport registration (#442), cannot exercise #443: %v", c.name, err)
				}
				t.Fatalf("SetSource on %s failed (regression of #443): %v", c.typ, err)
			}
			t.Logf("#443 OK: %s write succeeded", c.typ)
		})
	}
}
