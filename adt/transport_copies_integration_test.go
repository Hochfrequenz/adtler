//go:build integration && transport

package adt_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestCopyTransportObjects_Integration fills a transport of copies from a
// workbench request and from a package (#267), on every configured system.
//
// It branches on what the library reports, never on the system's name. A
// system whose transport organizer does not advertise the
// addobjectsfrompackage relation (ECC, SAP_BASIS 750) must refuse both calls
// with ErrorNotSupported before any write — on such a system every PUT goes
// to a legacy change-owner handler, which fails without a target user and
// returns an error about the owner instead of rejecting the action.
// That branch is decided with an empty source request, before any object is
// created, so it leaves nothing behind that this client could not remove.
//
// On a supporting system the result is verified by reading the target back:
// SAP answers 200 for both actions even when nothing was added.
//
// The source request is modifiable, so its program sits in its task, not
// at request level. Copying only the request's own entries yields a
// CORR MERG note and no program; the program showing up in the copy is what
// proves CopyTransportObjects also copied the task.
func TestCopyTransportObjects_Integration(t *testing.T) {
	ctx := context.Background()

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			c := sys.Client

			// The expected branch comes from the system's release, not from
			// the gate under test: a gate that wrongly refused would
			// otherwise pass as "unsupported".
			want := expectAddObjectsSupport(t, c)

			source := createDisposableTransport(t, c, "K", "adtler #267 copy source")
			target := createDisposableTransport(t, c, "T", "adtler #267 transport of copies")

			// 1. Decide the branch with an empty source: nothing created yet.
			err := c.CopyTransportObjects(ctx, source, target, true)
			if adt.ClassifyError(err) == adt.ErrorNotSupported {
				t.Logf("[1] CopyTransportObjects refused (not supported): %v", err)
				if want == addObjectsSupported {
					t.Fatalf("[1] refused on a release that has the action")
				}
				if err := c.AddPackageToTransport(ctx, testPackage, target, false); adt.ClassifyError(err) != adt.ErrorNotSupported {
					t.Fatalf("[1] AddPackageToTransport: want ErrorNotSupported on the same system, got %v", err)
				}
				objs, err := c.GetTransportObjects(ctx, target)
				if err != nil {
					t.Fatalf("[1] GetTransportObjects(target): %v", err)
				}
				if len(objs) != 0 {
					t.Fatalf("[1] target holds %d entries although both calls were refused", len(objs))
				}
				return
			}
			if err != nil {
				t.Fatalf("[1] CopyTransportObjects with an empty source: %v", err)
			}
			if want == addObjectsUnsupported {
				t.Fatalf("[1] accepted on a release that lacks the action")
			}

			// 2. Record a program in the source request.
			objName := timestampedName("Z_TOC_")
			objectURI := "/sap/bc/adt/programs/programs/" + objName
			if err := c.CreateObject(ctx, "PROG", objName, testPackage, "adtler #267 transport of copies", source); err != nil {
				t.Fatalf("[2] CreateObject: %v", err)
			}
			t.Cleanup(func() { cleanupTransportEntries(t, c, source) })
			t.Cleanup(func() {
				if err := c.DeleteObject(context.Background(), objectURI, "", source); err != nil {
					t.Logf("[cleanup] delete program: %v (manual cleanup may be needed)", err)
				}
			})

			// 3. Copy the request into the transport of copies.
			if err := c.CopyTransportObjects(ctx, source, target, true); err != nil {
				t.Fatalf("[3] CopyTransportObjects: %v", err)
			}
			if !transportLists(t, c, target, "PROG", objName) {
				t.Fatalf("[3] the program is not in the transport of copies after CopyTransportObjects")
			}
			t.Logf("[3] program copied into the transport of copies")

			// 4. Fill a second transport of copies from the package.
			byPackage := createDisposableTransport(t, c, "T", "adtler #267 package copy")
			if err := c.AddPackageToTransport(ctx, testPackage, byPackage, false); err != nil {
				t.Fatalf("[4] AddPackageToTransport: %v", err)
			}
			if !transportLists(t, c, byPackage, "PROG", objName) {
				t.Fatalf("[4] the program is not in the transport of copies after AddPackageToTransport")
			}
			t.Logf("[4] package content added to the transport of copies")
		})
	}
}

type addObjectsExpectation int

const (
	addObjectsEither addObjectsExpectation = iota
	addObjectsSupported
	addObjectsUnsupported
)

// expectAddObjectsSupport derives from the SAP_BASIS release whether the
// transport organizer must offer addobjectsfrompackage. SAP_BASIS 750 has
// neither action in IF_CTS_ADT_TM_CONSTANTS and SAP_BASIS 816 has both. The
// handler dates the package action "2105", which on-premise first ships
// with SAP_BASIS 756, so 754 and older must refuse, 756 and newer must
// accept, and 755 is left open.
func expectAddObjectsSupport(t *testing.T, c adt.Client) addObjectsExpectation {
	t.Helper()
	res, err := c.RunQuery(context.Background(), "SELECT release FROM cvers WHERE component = 'SAP_BASIS'", 1)
	if err != nil {
		t.Fatalf("CVERS query for SAP_BASIS: %v", err)
	}
	if len(res.Rows) == 0 || len(res.Rows[0]) == 0 {
		t.Fatalf("CVERS has no SAP_BASIS row")
	}
	release, err := strconv.Atoi(strings.TrimSpace(res.Rows[0][0]))
	if err != nil {
		t.Fatalf("SAP_BASIS release is not numeric: %v", err)
	}
	t.Logf("SAP_BASIS %d", release)
	switch {
	case release >= 756:
		return addObjectsSupported
	case release <= 754:
		return addObjectsUnsupported
	default:
		return addObjectsEither
	}
}

// createDisposableTransport creates a request in testPackage and deletes it
// again when the test ends.
func createDisposableTransport(t *testing.T, c adt.Client, category, description string) string {
	t.Helper()
	number, err := c.CreateTransport(context.Background(), category, "DUM", description, testPackage)
	if err != nil {
		t.Fatalf("CreateTransport(%s): %v", category, err)
	}
	t.Cleanup(func() {
		if err := c.DeleteTransport(context.Background(), number); err != nil {
			t.Logf("[cleanup] delete %s request: %v (manual cleanup may be needed)", category, err)
		}
	})
	return number
}

// cleanupTransportEntries removes every task-level entry from request, so
// that DeleteTransport is not refused for "locked objects". It runs after
// the objects themselves were deleted.
func cleanupTransportEntries(t *testing.T, c adt.Client, request string) {
	t.Helper()
	ctx := context.Background()
	objs, err := c.GetTransportObjects(ctx, request)
	if err != nil {
		t.Logf("[cleanup] read entries: %v", err)
		return
	}
	for _, o := range objs {
		if o.Task == "" {
			continue
		}
		if err := c.RemoveFromTransport(ctx, o.Task, request, o.PgmID, o.Type, o.Name, o.WBType, o.Position); err != nil {
			t.Logf("[cleanup] remove %s entry: %v", o.Type, err)
		}
	}
}

// transportLists reports whether request lists the R3TR object objType/name.
func transportLists(t *testing.T, c adt.Client, request, objType, name string) bool {
	t.Helper()
	objs, err := c.GetTransportObjects(context.Background(), request)
	if err != nil {
		t.Fatalf("GetTransportObjects: %v", err)
	}
	t.Logf("request holds %d entries", len(objs))
	for _, o := range objs {
		if o.PgmID == "R3TR" && o.Type == objType && o.Name == name {
			return true
		}
	}
	return false
}
