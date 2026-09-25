//go:build integration && transport

package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRemoveFromTransport_Integration exercises RemoveFromTransport against
// every configured system, and branches on what the library reports about
// the system rather than on the system's name.
//
// Removing an object from a transport request reached SAP's ADT only in
// AS ABAP 7.53 SP00 / ABAP Platform 1809. On older releases the endpoint
// does not exist and the PUT this library used to send was absorbed by a
// legacy change-owner handler, so RemoveFromTransport now derives the
// system's capability from the atom relations the server advertises and
// blocks the write when it is confirmed unsupported (see
// adt.RemoveObjectSupport and ensureRemoveObjectSupported, adtler#125).
// Both outcomes are correct behaviour, so this test asserts whichever one
// matches the capability the client cached:
//
//   - Supported: the object is removed and is gone from the transport's
//     object list afterwards.
//   - Unsupported: RemoveFromTransport returns an *adt.ADTError of type
//     adt.ExceptionTypeRemoveObjectUnsupported without sending the PUT, and
//     the object is still in the transport afterwards.
//
// The capability is a cached side effect of any successful transport read
// (see cacheRemoveObjectSupport), so the GetTransportObjects call in step 3
// is what populates it; it is then read through the test-only
// adt.TestClient hook (adt/export_internal_test.go), never through the
// exported Client surface.
//
// A capability that is still Unknown at that point fails the test without
// calling RemoveFromTransport: the gate deliberately fails open on Unknown,
// so calling it would send exactly the legacy-handler PUT this test must
// not provoke on a system that could not be classified.
//
// Build tag `integration && transport`: this test creates a real transport
// request and a real program, and releases the transport afterwards.
func TestRemoveFromTransport_Integration(t *testing.T) {
	ctx := context.Background()

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			testClient, ok := sys.Client.(adt.TestClient)
			if !ok {
				t.Fatalf("client %T does not implement adt.TestClient", sys.Client)
			}

			// 1. Create a transport to hold the test object.
			trNumber, err := sys.Client.CreateTransport(ctx, "K", "DUM",
				"adtler #125 RemoveFromTransport test", testPackage)
			if err != nil {
				t.Fatalf("[1] CreateTransport: %v", err)
			}
			t.Logf("[1] created transport: %s", trNumber)
			t.Cleanup(func() {
				if _, err := sys.Client.ReleaseTransportWithTasks(context.Background(), trNumber); err != nil {
					t.Logf("[cleanup] release transport %s: %v (manual cleanup may be needed)", trNumber, err)
				} else {
					t.Logf("[cleanup] released transport %s", trNumber)
				}
			})

			// 2. Create a program assigned to that transport. The name is
			// timestamped so an aborted earlier run cannot collide with this
			// one — the object is deleted again in cleanup, before the
			// transport is released.
			objName := timestampedName("Z_RMFTR_")
			objectURI := "/sap/bc/adt/programs/programs/" + objName
			if err := sys.Client.CreateObject(ctx, "PROG", objName, testPackage,
				"adtler #125 RemoveFromTransport test", trNumber); err != nil {
				t.Fatalf("[2] CreateObject(%s): %v", objName, err)
			}
			t.Logf("[2] created %s in %s", objName, testPackage)
			t.Cleanup(func() {
				if err := sys.Client.DeleteObject(context.Background(), objectURI, "", trNumber); err != nil {
					t.Logf("[cleanup] delete %s: %v (manual cleanup may be needed)", objName, err)
				} else {
					t.Logf("[cleanup] deleted %s", objName)
				}
			})

			// 3. Resolve the task the object was recorded under, and the
			// object entry itself. This read is also what populates the
			// cached removeobject capability.
			taskNumber := trNumber
			tasks, err := sys.Client.GetTransportTasks(ctx, trNumber)
			if err != nil {
				t.Fatalf("[3] GetTransportTasks: %v", err)
			}
			if len(tasks) > 0 {
				taskNumber = tasks[0]
			} else {
				t.Logf("[3] no tasks reported for %s — using the request number itself", trNumber)
			}
			t.Logf("[3] task: %s", taskNumber)

			objects, err := sys.Client.GetTransportObjects(ctx, trNumber)
			if err != nil {
				t.Fatalf("[3] GetTransportObjects: %v", err)
			}
			var wbType, position string
			found := false
			for _, o := range objects {
				if o.Name == objName {
					wbType, position, found = o.WBType, o.Position, true
					t.Logf("[3] found: pgmid=%s type=%s name=%s wbtype=%s pos=%s",
						o.PgmID, o.Type, o.Name, o.WBType, o.Position)
				}
			}

			// 4. Branch on the capability the client derived from the server's
			// own atom relations — never on sys.Name.
			support := testClient.RemoveObjectSupportForTest()
			t.Logf("[4] RemoveObjectSupport = %v", support)

			switch support {
			case adt.RemoveObjectSupportSupported:
				if !found {
					t.Fatalf("[4] object %s not found in transport %s", objName, trNumber)
				}
				if err := sys.Client.RemoveFromTransport(ctx, taskNumber, trNumber,
					"R3TR", "PROG", objName, wbType, position); err != nil {
					t.Fatalf("[4] RemoveFromTransport: %v", err)
				}
				t.Logf("[4] RemoveFromTransport succeeded")

				// 5. The object must be gone from the transport.
				objects, err = sys.Client.GetTransportObjects(ctx, trNumber)
				if err != nil {
					t.Fatalf("[5] GetTransportObjects: %v", err)
				}
				for _, o := range objects {
					if o.Name == objName {
						t.Fatalf("[5] object %s still in transport after removal", objName)
					}
				}
				t.Logf("[5] verified: %s removed from transport %s", objName, trNumber)

			case adt.RemoveObjectSupportUnsupported:
				if !found {
					// Not fatal: the gate assertion below is the point of
					// this branch and does not depend on the entry's
					// wbtype/position. Still an error — the object was just
					// recorded in this transport, so it should be listed.
					t.Errorf("[4] object %s not found in transport %s", objName, trNumber)
				}
				err := sys.Client.RemoveFromTransport(ctx, taskNumber, trNumber,
					"R3TR", "PROG", objName, wbType, position)
				if err == nil {
					t.Fatal("[4] expected RemoveFromTransport to be blocked by the removeobject capability gate, got nil error")
				}
				t.Logf("[4] RemoveFromTransport returned (expected): %v", err)

				var adtErr *adt.ADTError
				if !errors.As(err, &adtErr) {
					t.Fatalf("[4] expected *adt.ADTError, got %T: %v", err, err)
				}
				if adtErr.Type != adt.ExceptionTypeRemoveObjectUnsupported {
					t.Fatalf("[4] expected error type %q, got %q (message: %s)",
						adt.ExceptionTypeRemoveObjectUnsupported, adtErr.Type, adtErr.Message)
				}
				if kind := adt.ClassifyError(err); kind != adt.ErrorNotSupported {
					t.Errorf("[4] ClassifyError = %v, want %v", kind, adt.ErrorNotSupported)
				}

				// 5. The gate returns before the PUT is sent, so the object
				// must still be in the transport — nothing reached the
				// legacy change-owner handler.
				objects, err = sys.Client.GetTransportObjects(ctx, trNumber)
				if err != nil {
					t.Fatalf("[5] GetTransportObjects: %v", err)
				}
				stillThere := false
				for _, o := range objects {
					if o.Name == objName {
						stillThere = true
					}
				}
				if found && !stillThere {
					t.Errorf("[5] object %s disappeared from transport %s although the gate blocked the removal", objName, trNumber)
				}
				t.Logf("[5] verified: %s still in transport %s, no write was sent", objName, trNumber)

			default:
				t.Fatalf("[4] RemoveObjectSupport is still %v after reading transport %s — "+
					"not calling RemoveFromTransport, because the gate fails open on an "+
					"unknown capability and would send the legacy-handler PUT", support, trNumber)
			}
		})
	}
}
