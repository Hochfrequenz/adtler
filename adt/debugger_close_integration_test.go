//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestDebugSessionClose_MultiSystem_Integration ends a DebugSession's own SAP
// session and checks that the DebugSession opens a new one on its next call
// (issue #162). It touches no fixture and takes no lock: StopListener only
// asks SAP to drop this user's listener, which is a no-op when none runs.
func TestDebugSessionClose_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run("system", func(t *testing.T) {
			dbg := adt.NewDebugSession(sys.Client, sys.Config.User)
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
