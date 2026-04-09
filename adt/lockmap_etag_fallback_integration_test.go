//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestFetchETag_NonProgObjects_MultiSystem_Integration validates that
// FetchETag works for object types where GetSource fails (CLAS, DTEL, DOMA).
// This is the fallback path ResolveETag uses since adtler#9 / adtler#14.
//
// For each system: call FetchETag on a known existing object of each type
// and assert a non-empty ETag is returned.
func TestFetchETag_NonProgObjects_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()

	// Test URIs — these must exist on the test systems. We use well-known
	// SAP standard objects that exist on every R/3 and S/4 release.
	tests := []struct {
		name string
		uri  string
	}{
		{"CLAS_CL_ABAP_CHAR_UTILITIES", "/sap/bc/adt/oo/classes/cl_abap_char_utilities"},
		{"INTF_IF_BADI_INTERFACE", "/sap/bc/adt/oo/interfaces/if_badi_interface"},
		{"PROG_RSPARAM", "/sap/bc/adt/programs/programs/rsparam"},
	}

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					etag, err := sys.Client.(interface {
						FetchETag(context.Context, string) (string, error)
					}).FetchETag(ctx, tc.uri)
					if err != nil {
						t.Fatalf("[%s] FetchETag(%s): %v", sys.Name, tc.uri, err)
					}
					if etag == "" {
						t.Errorf("[%s] FetchETag(%s) returned empty ETag", sys.Name, tc.uri)
					}
					t.Logf("[%s] %s ETag=%s", sys.Name, tc.name, etag)
				})
			}
		})
	}
}

// TestResolveETag_CLASFallback_MultiSystem_Integration validates the full
// ResolveETag path for a CLAS object: GetSource fails → FetchETag fallback
// succeeds. This is the exact bug path from adtler#9 / mcp-server-abap#284.
func TestResolveETag_CLASFallback_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			lockMap := adt.NewLockMap()
			// CL_ABAP_CHAR_UTILITIES exists on every SAP release.
			const classURI = "/sap/bc/adt/oo/classes/cl_abap_char_utilities"
			key := adt.LockKey(sys.Name, classURI)

			etag, err := lockMap.ResolveETag(ctx, sys.Client, key, classURI)
			if err != nil {
				t.Fatalf("[%s] ResolveETag for CLAS: %v — "+
					"GetSource likely failed and FetchETag fallback didn't work", sys.Name, err)
			}
			if etag == "" {
				t.Errorf("[%s] ResolveETag returned empty ETag for CLAS", sys.Name)
			}
			t.Logf("[%s] CLAS ETag resolved: %s", sys.Name, etag)
		})
	}
}
