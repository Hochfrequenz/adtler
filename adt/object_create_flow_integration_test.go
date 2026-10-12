//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// TestCreateObject_ThenLockAndWrite_MultiSystem_Integration is the end-to-end
// regression test for adtler#4 (mcp-server-abap#282). Before the Logout
// workaround, this flow was COMPLETELY BROKEN on S/4:
//
//	CreateObject → LockObject → SetSource → UnlockObject → DeleteObject
//
// The LockObject step would fail with 423 InvalidLockHandle because
// CreateObject left a session-bound ESRDIRE enqueue that the next call
// (in a different SAP session) couldn't see.
//
// After the workaround (Logout after CreateObject), the enqueue is released
// and the full chain works.
func TestCreateObject_ThenLockAndWrite_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			// Unique name per system + per run to avoid collisions.
			name := fmt.Sprintf("Z_ADT_MCP_FLOW_%d", time.Now().Unix()%100000)
			uri := "/sap/bc/adt/programs/programs/" + name

			// 1. Create
			err := sys.Client.CreateObject(ctx, "PROG", name, "$TMP",
				"adtler#4 create-flow integration test", "")
			if err != nil {
				t.Fatalf("[%s] CreateObject: %v", sys.Name, err)
			}
			t.Logf("[%s] created %s", sys.Name, name)

			// Register cleanup. It deletes without taking a lock first, because
			// on S/4 the caller's own lock blocks the delete and the old
			// lock-then-delete cleanup silently left a program behind in $TMP
			// on every run (adtler#187). A program that cannot be removed
			// fails the test instead of leaking.
			t.Cleanup(func() { removeProgramIfPresent(t, sys.Client, uri) })

			// 2. Lock — THIS IS THE STEP THAT FAILED BEFORE THE FIX.
			lockHandle, err := sys.Client.LockObject(ctx, uri)
			if err != nil {
				t.Fatalf("[%s] LockObject after CreateObject: %v — "+
					"this is the exact failure from adtler#4 (session-bound ESRDIRE enqueue)", sys.Name, err)
			}
			t.Logf("[%s] locked with handle %s", sys.Name, lockHandle[:min(len(lockHandle), 20)]+"...")

			// 3. Get ETag for SetSource.
			src, err := sys.Client.GetSource(ctx, uri)
			if err != nil {
				_ = sys.Client.UnlockObject(ctx, uri, lockHandle)
				t.Fatalf("[%s] GetSource: %v", sys.Name, err)
			}

			// 4. Write source.
			source := fmt.Sprintf("REPORT %s.\nWRITE: / 'adtler#4 flow test'.\n", name)
			_, err = sys.Client.SetSource(ctx, uri, source, lockHandle, "", src.ETag)
			if err != nil {
				_ = sys.Client.UnlockObject(ctx, uri, lockHandle)
				t.Fatalf("[%s] SetSource: %v", sys.Name, err)
			}
			t.Logf("[%s] source written successfully", sys.Name)

			// 5. Unlock.
			err = sys.Client.UnlockObject(ctx, uri, lockHandle)
			if err != nil {
				t.Fatalf("[%s] UnlockObject: %v", sys.Name, err)
			}
			t.Logf("[%s] FULL CHAIN OK: create → lock → write → unlock", sys.Name)
		})
	}
}
