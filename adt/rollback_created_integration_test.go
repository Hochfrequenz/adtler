//go:build integration && transport

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRollbackTransport_CreatedObject_Integration rolls back a request that
// created and activated a program (#268), on every configured system.
//
// Without options the program must be reported in Created, not in Failed,
// and must still exist. With DeleteCreatedObjects it must be reported in
// Deleted, and its object URI must answer 404 afterwards.
//
// Nothing here needs a released request, so the test runs on ECC as well as
// on S/4. The changed-and-created combination in one request is covered by
// TestRollbackTransport_Integration, which needs a release.
func TestRollbackTransport_CreatedObject_Integration(t *testing.T) {
	ctx := context.Background()

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			c := sys.Client
			_ = c.Logout(ctx)

			request := createDisposableTransport(t, c, "adtler #268 rollback of a created object")

			// 1. Create and activate a program in the request.
			objName := timestampedName("Z_RBC_")
			objectURI := "/sap/bc/adt/programs/programs/" + objName
			if err := c.CreateObject(ctx, "PROG", objName, testPackage, "adtler #268 rollback created", request); err != nil {
				t.Fatalf("[1] CreateObject: %v", err)
			}
			t.Cleanup(func() { cleanupTransportEntries(t, c, request) })
			t.Cleanup(func() {
				if _, err := c.GetObjectInfo(context.Background(), objectURI); adt.ClassifyError(err) == adt.ErrorNotFound {
					return
				}
				if err := c.DeleteObject(context.Background(), objectURI, "", request); err != nil {
					t.Logf("[cleanup] delete program: %v (manual cleanup may be needed)", err)
				}
			})
			writeAndActivate(t, c, objectURI, "REPORT "+objName+".\n\" created by the request\n", request)

			versions, err := c.GetVersionHistory(ctx, objectURI)
			if err != nil {
				t.Fatalf("[1] GetVersionHistory: %v", err)
			}
			t.Logf("[1] program created and activated, %d version-history entries", len(versions))

			// 2. Without options: reported as created, left in place.
			result, err := c.RollbackTransport(ctx, request)
			if err != nil {
				t.Fatalf("[2] RollbackTransport: %v", err)
			}
			logRollback(t, "[2]", request, result)
			if !rollbackLists(result.Created, objName) {
				t.Errorf("[2] program not in Created")
			}
			if len(result.Failed) != 0 || len(result.Deleted) != 0 {
				t.Errorf("[2] failed=%d deleted=%d, want both 0", len(result.Failed), len(result.Deleted))
			}
			if _, err := c.GetObjectInfo(ctx, objectURI); err != nil {
				t.Fatalf("[2] program gone after a rollback without DeleteCreatedObjects: %v", err)
			}

			// 3. With DeleteCreatedObjects: deleted.
			_ = c.Logout(ctx)
			result, err = c.RollbackTransportWithOptions(ctx, request, adt.RollbackOptions{DeleteCreatedObjects: true})
			if err != nil {
				t.Fatalf("[3] RollbackTransportWithOptions: %v", err)
			}
			logRollback(t, "[3]", request, result)
			if !rollbackLists(result.Deleted, objName) {
				t.Errorf("[3] program not in Deleted")
			}
			if len(result.Failed) != 0 || len(result.Created) != 0 {
				t.Errorf("[3] failed=%d created=%d, want both 0", len(result.Failed), len(result.Created))
			}
			if _, err := c.GetObjectInfo(ctx, objectURI); adt.ClassifyError(err) != adt.ErrorNotFound {
				t.Errorf("[3] GetObjectInfo after deletion: want ErrorNotFound, got %v", err)
			}
		})
	}
}

// writeAndActivate writes source to the program or class at objectURI under
// request and activates it.
func writeAndActivate(t *testing.T, c adt.Client, objectURI, source, request string) {
	t.Helper()
	ctx := context.Background()
	// ETag before the lock: on SAP_BASIS 750 a GET between LockObject and
	// SetSource ends the stateful session, and a class's lock with it.
	src, err := c.GetSource(ctx, objectURI)
	if err != nil {
		t.Fatalf("GetSource for ETag: %s", redactError(err, "", request))
	}
	lh, err := c.LockObject(ctx, objectURI)
	if err != nil {
		t.Fatalf("LockObject: %s", redactError(err, "", request))
	}
	if _, err := c.SetSource(ctx, objectURI, source, lh, request, src.ETag); err != nil {
		_ = c.UnlockObject(ctx, objectURI, lh)
		t.Fatalf("SetSource: %s", redactError(err, "", request))
	}
	_ = c.UnlockObject(ctx, objectURI, lh)
	res, err := c.ActivateObjects(ctx, []string{objectURI})
	if err != nil {
		t.Fatalf("ActivateObjects: %s", redactError(err, "", request))
	}
	if !res.Success {
		t.Fatalf("activation failed: %s", redact(fmt.Sprint(res.Messages), "", request))
	}
}

func rollbackLists(entries []adt.RollbackEntry, name string) bool {
	for _, e := range entries {
		if e.Name == name {
			return true
		}
	}
	return false
}

// logRollback logs the partition sizes and the reasons of failed and created
// entries, with request replaced by a placeholder so the log can be posted.
func logRollback(t *testing.T, step, request string, r *adt.RollbackResult) {
	t.Helper()
	t.Logf("%s restored=%d skipped=%d failed=%d created=%d deleted=%d",
		step, len(r.Restored), len(r.Skipped), len(r.Failed), len(r.Created), len(r.Deleted))
	redact := func(s string) string { return strings.ReplaceAll(s, request, "<request>") }
	for _, f := range r.Failed {
		t.Logf("%s failed: %s — %s", step, f.Type, redact(f.Reason))
	}
	for _, c := range r.Created {
		t.Logf("%s created: %s — %s", step, c.Type, redact(c.Reason))
	}
}
