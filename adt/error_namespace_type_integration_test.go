//go:build integration

package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestADTError_NamespaceTypeExtraction_MultiSystem verifies issue #43:
// when a real SAP system returns an <exc:exception> envelope, parseADTError
// populates ADTError.Namespace and ADTError.Type from <namespace id="…"/>
// and <type id="…"/>.
//
// We don't hardcode the exact Type ID because it can differ slightly
// between R/3 and S/4 (and between operations). Instead the test asserts
// that BOTH fields are non-empty and logs the observed values per system,
// so we accumulate empirical knowledge of the ID space.
//
// The trigger is LockObject on a non-existent class URI, which reliably
// produces an exception envelope on both R/3 and S/4.
func TestADTError_NamespaceTypeExtraction_MultiSystem(t *testing.T) {
	ctx := context.Background()
	// Deliberately bogus URI — no SAP system will have this object.
	const fakeURI = "/sap/bc/adt/oo/classes/zcl_adt_mcp_exc_test_xxx"

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			_, err := sys.Client.LockObject(ctx, fakeURI)
			if err == nil {
				t.Fatalf("[%s] expected LockObject on non-existent URI to fail, got nil", sys.Name)
			}

			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("[%s] expected *adt.ADTError, got %T: %v", sys.Name, err, err)
			}

			t.Logf("[%s] StatusCode=%d Namespace=%q Type=%q Message=%q",
				sys.Name, adtErr.StatusCode, adtErr.Namespace, adtErr.Type, adtErr.Message)

			if adtErr.Namespace == "" {
				t.Errorf("[%s] expected non-empty Namespace, got %q (full error: %v)",
					sys.Name, adtErr.Namespace, err)
			}
			if adtErr.Type == "" {
				t.Errorf("[%s] expected non-empty Type, got %q (full error: %v)",
					sys.Name, adtErr.Type, err)
			}
			if adtErr.Message == "" {
				t.Errorf("[%s] expected non-empty Message, got empty", sys.Name)
			}
		})
	}
}
