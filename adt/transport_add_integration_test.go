//go:build integration && transport

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestAddToTransport_Integration adds a program to a task with the
// transport organizer's addobject user action (#154), on every configured
// system.
//
// Before #154 AddToTransport posted to …/abaptransportcomponents, which no
// release implements: both SAP_BASIS 750 and 816 answered 400 "user action
// abaptransportcomponents is not supported". The expected branch comes from
// the SAP_BASIS release, not from the gate under test, so a gate that wrongly
// refused cannot pass as "unsupported":
//
//   - SAP_BASIS 750 has no add action. AddToTransport must refuse with
//     ErrorNotSupported before any write, because every PUT there is a
//     change-owner request. That branch creates no object, so it leaves
//     nothing behind that this client could not remove.
//   - SAP_BASIS 816 has it. The program is created in the request (which
//     records it in the task), its entry is removed again, and
//     AddToTransport must put it back. Reading the task back is the proof;
//     the call's error alone is not, because S/4 checks the Accept header
//     only after it has added the entry.
func TestAddToTransport_Integration(t *testing.T) {
	ctx := context.Background()

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			c := sys.Client
			release := sapBasisRelease(t, c)

			request := createDisposableTransport(t, c, "adtler #154 add object")
			tasks, err := c.GetTransportTasks(ctx, request)
			if err != nil || len(tasks) == 0 {
				t.Fatalf("GetTransportTasks: %d tasks, err %v", len(tasks), err)
			}
			task := tasks[0]

			objName := timestampedName("Z_ADD154_")

			// 1. Decide the branch before anything is created.
			if release <= 750 {
				err := c.AddToTransport(ctx, addPgmID, addObjType, objName, task)
				if adt.ClassifyError(err) != adt.ErrorNotSupported {
					t.Fatalf("[1] SAP_BASIS %d: want ErrorNotSupported before any write, got %v", release, err)
				}
				t.Logf("[1] refused as not supported: %v", err)
				objs, err := c.GetTransportObjects(ctx, request)
				if err != nil {
					t.Fatalf("[1] GetTransportObjects: %v", err)
				}
				if len(objs) != 0 {
					t.Fatalf("[1] request holds %d entries although the call was refused", len(objs))
				}
				return
			}
			if release < 816 {
				t.Skipf("SAP_BASIS %d: only 750 and 816 were measured", release)
			}

			// 2. Create a program in the request; SAP records it in the task.
			objectURI := "/sap/bc/adt/programs/programs/" + objName
			if err := c.CreateObject(ctx, addObjType, objName, testPackage, "adtler #154 add object", request); err != nil {
				t.Fatalf("[2] CreateObject: %v", err)
			}
			t.Cleanup(func() { cleanupTransportEntries(t, c, request) })
			t.Cleanup(func() {
				if err := c.DeleteObject(context.Background(), objectURI, "", request); err != nil {
					t.Logf("[cleanup] delete program: %v (manual cleanup may be needed)", err)
				}
			})

			// 3. Remove its entry, so that adding it is a real change.
			cleanupTransportEntries(t, c, request)
			if transportLists(t, c, request, addObjType, objName) {
				t.Fatalf("[3] the program is still in the request after its entry was removed")
			}

			// 4. Add it back.
			if err := c.AddToTransport(ctx, addPgmID, addObjType, objName, task); err != nil {
				t.Fatalf("[4] AddToTransport: %v", err)
			}
			objs, err := c.GetTransportObjects(ctx, request)
			if err != nil {
				t.Fatalf("[4] GetTransportObjects: %v", err)
			}
			found := false
			for _, o := range objs {
				if o.PgmID == addPgmID && o.Type == addObjType && o.Name == objName && o.Task == task {
					found = true
				}
			}
			if !found {
				t.Fatalf("[4] the program is not in the task after AddToTransport (%d entries in the request)", len(objs))
			}
			t.Logf("[4] program added to the task")
		})
	}
}
