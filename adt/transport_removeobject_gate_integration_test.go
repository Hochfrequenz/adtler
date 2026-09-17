//go:build integration

// Integration test for adtler#125
// (https://github.com/Hochfrequenz/adtler/issues/125): confirm that
// RemoveFromTransport's capability gate (ensureRemoveObjectSupported) blocks
// the write on the ECC system with the typed
// adt.ExceptionTypeRemoveObjectUnsupported error, using the real fixture
// arguments from https://github.com/Hochfrequenz/aibap.mcp/issues/493.
//
// SAFETY: this test must never run against the S/4 system — removal is
// supported there, so a write would actually happen. The gate blocks the
// call before any HTTP PUT is sent, so no write happens on the ECC system
// (or on the S/4 system) even if the gate were to fail open, but the
// restriction to the ECC system is enforced structurally here too (skip on
// any system name other than the one configured locally for it), not merely
// relied upon via the gate.
package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRemoveFromTransport_ECCGateBlocksWrite_Integration is the removeobject
// capability gate's regression test. It runs RemoveFromTransport against the
// ECC system only, using the exact task/parent/object/wbtype/position from
// aibap.mcp#493, and asserts on the typed error the gate
// (ensureRemoveObjectSupported) returns — never on any side effect on the SAP
// side, since none is expected either way.
func TestRemoveFromTransport_ECCGateBlocksWrite_Integration(t *testing.T) {
	// taskNumber/parentNumber/objectName are live, environment-specific
	// identifiers that must already exist on the real ECC system this test
	// runs against — a synthetic value would not correspond to anything
	// there. They are the same reproducer arguments aibap.mcp#493 recorded.
	const (
		taskNumber   = "HFQK902953"
		parentNumber = "HFQK902952"
		pgmID        = "R3TR"
		objectType   = "CLAS"
		objectName   = "ZCL_LOCKREPRO_2"
		wbType       = "CLAS/OC"
		position     = "000001"
	)

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// sys.Name is compared against the real system name configured
			// locally for the ECC system — this is a safety allowlist, not a
			// value that generalizes to any other reader's own config.
			if sys.Name != "HFQ" {
				t.Skipf("this test only ever runs against the ECC system — never against the S/4 system; got %q", sys.Name)
			}

			ctx := context.Background()
			err := sys.Client.RemoveFromTransport(ctx, taskNumber, parentNumber, pgmID, objectType, objectName, wbType, position)
			if err == nil {
				t.Fatal("expected RemoveFromTransport to be blocked by the removeobject capability gate, got nil error")
			}
			t.Logf("%s: RemoveFromTransport returned (expected): %v", sys.Name, err)

			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
			}
			if adtErr.Type != adt.ExceptionTypeRemoveObjectUnsupported {
				t.Fatalf("expected Type %q, got %q (message: %s)",
					adt.ExceptionTypeRemoveObjectUnsupported, adtErr.Type, adtErr.Message)
			}
			t.Logf("%s: gate error text: %s", sys.Name, adtErr.Error())
		})
	}
}
