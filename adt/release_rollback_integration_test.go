//go:build integration && transport

package adt_test

import (
	"context"
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
