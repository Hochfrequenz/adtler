//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestSetSource_OOCreateWrite_Integration is the regression guard for
// aibap.mcp#443: creating a class or interface and writing its source must
// succeed. Before the fix, the write failed 403 ExceptionResourceNoAccess
// ("currently editing") because the lock handle was delivered as a header, but
// OO handlers read the ?lockHandle= query param — the header-first attempt is
// treated as unlocked and the write is rejected. Now trySetSource retries with
// query delivery on that 403. Verified on HF S/4 Mandant 100.
func TestSetSource_OOCreateWrite_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	tr, err := client.CreateTransport(ctx, "K", "", "aibap #443 OO create-write guard", testPackage)
	if err != nil {
		t.Fatalf("CreateTransport: %v", err)
	}

	cases := []struct{ typ, name, uri, src string }{
		{"CLAS", "ZCL_ADT_MCP_OOGUARD", "/sap/bc/adt/oo/classes/zcl_adt_mcp_ooguard",
			"CLASS zcl_adt_mcp_ooguard DEFINITION PUBLIC FINAL CREATE PUBLIC.\n  PUBLIC SECTION.\nENDCLASS.\nCLASS zcl_adt_mcp_ooguard IMPLEMENTATION.\nENDCLASS.\n"},
		{"INTF", "ZIF_ADT_MCP_OOGUARD", "/sap/bc/adt/oo/interfaces/zif_adt_mcp_ooguard",
			"INTERFACE zif_adt_mcp_ooguard PUBLIC.\n  METHODS noop.\nENDINTERFACE.\n"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.typ, func(t *testing.T) {
			if err := client.CreateObject(ctx, c.typ, c.name, testPackage, "aibap #443 guard", tr); err != nil {
				if _, e := client.GetObjectInfo(ctx, c.uri); e != nil {
					t.Fatalf("CreateObject(%s): %v", c.typ, err)
				}
				t.Logf("%s exists, reusing", c.name)
			}
			t.Cleanup(func() { _ = client.DeleteObject(context.Background(), c.uri, "", tr) })

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
				t.Fatalf("SetSource on %s failed (regression of #443): %v", c.typ, err)
			}
			t.Logf("#443 OK: %s create->write succeeded", c.typ)
		})
	}
}
