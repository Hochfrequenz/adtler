//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestSetSource_DDLS_Integration is the regression guard for aibap.mcp#383's
// DDLS symptom: before the fix, writing a CDS view's source via SetSource failed
// with 403 ExceptionResourceNoAccess ("currently editing") because adtler
// delivered the lock handle as a header (DDLS needs it as a ?lockHandle= query
// param, and the header-first path 400/403s without triggering the 423 retry).
// After the fix, the locked write succeeds.
func TestSetSource_DDLS_Integration(t *testing.T) {
	client := newIntegrationClient(t) // default = HF S/4 Mandant 100
	ctx := context.Background()

	const name = "Z_ADT_MCP_CDS383"
	const uri = "/sap/bc/adt/ddic/ddl/sources/z_adt_mcp_cds383"
	const pkg = "Z_ADT_MCP_TEST"
	cds := "@EndUserText.label: 'aibap 383 regression'\n" +
		"define root view entity Z_ADT_MCP_CDS383\n  as select from t000\n{\n  key mandt as Client\n}\n"

	tr, err := client.CreateTransport(ctx, "K", "", "aibap #383 DDLS regression", pkg)
	if err != nil {
		t.Fatalf("CreateTransport: %v", err)
	}
	if err := client.CreateObject(ctx, "DDLS", name, pkg, "aibap #383 DDLS regression", tr); err != nil {
		if _, e := client.GetObjectInfo(ctx, uri); e != nil {
			t.Fatalf("CreateObject(DDLS): %v", err)
		}
		t.Log("DDLS exists, reusing")
	}
	t.Cleanup(func() { _ = client.DeleteObject(context.Background(), uri, "", tr) })

	lockHandle, err := client.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	defer func() { _ = client.UnlockObject(context.Background(), uri, lockHandle) }()

	src, err := client.GetSource(ctx, uri)
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}

	// The write that returned 403 before the fix.
	if _, err := client.SetSource(ctx, uri, cds, lockHandle, tr, src.ETag); err != nil {
		t.Fatalf("SetSource on DDLS failed (regression of #383): %v", err)
	}
	t.Log("SetSource on DDLS with lock: OK")
}
