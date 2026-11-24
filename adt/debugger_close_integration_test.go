//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestDebugSessionClose_MultiSystem_Integration calls Close on a DebugSession
// and checks that the logoff is accepted and that the DebugSession works again
// on its next call (issue #162). It does not show that SAP released any
// state: that would need a wedged session. It touches no fixture and takes no
// lock. StopListener is the cheapest call that opens a session; it uses its own
// IDE ID, so it cannot stop the listener of a real debug session of the same
// user.
func TestDebugSessionClose_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run("system", func(t *testing.T) {
			dbg := adt.NewDebugSession(sys.Client, sys.Config.User, "adtler-close-test")
			// Also end the session when the test fails before the explicit Close.
			t.Cleanup(func() { _ = dbg.Close(context.Background()) })

			if err := dbg.StopListener(ctx); err != nil {
				t.Fatalf("StopListener before Close failed: %v", err)
			}
			if err := dbg.Close(ctx); err != nil {
				t.Fatalf("Close failed: %v", err)
			}
			if err := dbg.StopListener(ctx); err != nil {
				t.Errorf("StopListener on the new session after Close failed: %v", err)
			}
		})
	}
}
