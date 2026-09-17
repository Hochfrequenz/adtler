//go:build integration

// Integration test for adtler#125
// (https://github.com/Hochfrequenz/adtler/issues/125): confirm that
// RemoveFromTransport's capability gate (ensureRemoveObjectSupported) blocks
// the write on R/3 with the typed adt.ExceptionTypeRemoveObjectUnsupported
// error, using the real fixture arguments from
// https://github.com/Hochfrequenz/aibap.mcp/issues/493.
//
// SAFETY: this test must never run against S/4 (S4U) — removal is supported
// there, so a write would actually happen. The gate blocks the call before
// any HTTP PUT is sent, so no write happens on HFQ (or on S4U) even if the
// gate were to fail open, but the restriction to HFQ is enforced structurally
// here too (skip on any system name other than HFQ), not merely relied upon
// via the gate.
package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRemoveFromTransport_ECCGateBlocksWrite_Integration is the removeobject
// capability gate's regression test. It runs RemoveFromTransport against R/3
// (HFQ) only, using the exact task/parent/object/wbtype/position from
// aibap.mcp#493, and asserts on the typed error the gate
// (ensureRemoveObjectSupported) returns — never on any side effect on the SAP
// side, since none is expected either way.
func TestRemoveFromTransport_ECCGateBlocksWrite_Integration(t *testing.T) {
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
			if sys.Name != "HFQ" {
				t.Skipf("this test only ever runs against HFQ (R/3) — never against S4U; got %q", sys.Name)
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
