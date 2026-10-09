//go:build integration

package adt_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// verifyTempPrefix is the name prefix of the throwaway programs VerifySource
// creates in $TMP. The search below uses it with a wildcard.
const verifyTempPrefix = "Z_ADTLER_VERIFY_"

// requireProgramGone fails the test unless reading the program answers "not
// found". Any other error, such as an authorization failure, must not pass for
// "gone", so the answer is classified rather than just checked for non-nil.
func requireProgramGone(t *testing.T, client adt.Client, uri string) {
	t.Helper()
	_, err := client.GetObjectInfo(context.Background(), uri)
	if err == nil {
		t.Fatalf("the program %s is still there after the delete", uri)
	}
	if kind := adt.ClassifyError(err); kind != adt.ErrorNotFound {
		t.Fatalf("reading the deleted program should answer not found, got kind %s: %v", kind, err)
	}
}

// removeProgramIfPresent is the cleanup half of the tests below. It deletes
// without a lock, because a lock taken first is what blocks the delete on
// S/4HANA (adtler#187), and it fails the test if a program that is still there
// cannot be removed, so a leftover in $TMP is never silent.
func removeProgramIfPresent(t *testing.T, client adt.Client, uri string) {
	t.Helper()
	ctx := context.Background()
	if _, err := client.GetObjectInfo(ctx, uri); err != nil {
		return // already gone (the happy path), or never created
	}
	if err := client.DeleteObject(ctx, uri, "", ""); err != nil {
		t.Errorf("cleanup could not delete the test program %s, remove it by hand: %v", uri, err)
	}
}

// createUnactivatedTempProgram creates a program in $TMP, writes source into
// it and leaves it inactive: the state VerifySource and the create-flow test
// leave behind. The cleanup removes it again if the test did not.
func createUnactivatedTempProgram(t *testing.T, sys integrationSystem, label string) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("Z_ADTLER_%s_%06d", label, rand.Intn(1000000)) //nolint:gosec // throwaway name, not security-sensitive
	uri := "/sap/bc/adt/programs/programs/" + name

	if err := sys.Client.CreateObject(ctx, "PROG", name, "$TMP", "adtler#187 delete test", ""); err != nil {
		t.Fatalf("[%s] CreateObject: %v", sys.Name, err)
	}
	t.Cleanup(func() { removeProgramIfPresent(t, sys.Client, uri) })

	lh, err := sys.Client.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("[%s] LockObject: %v", sys.Name, err)
	}
	src, err := sys.Client.GetSource(ctx, uri)
	if err != nil {
		_ = sys.Client.UnlockObject(ctx, uri, lh)
		t.Fatalf("[%s] GetSource: %v", sys.Name, err)
	}
	if _, err := sys.Client.SetSource(ctx, uri, "REPORT "+name+".\n", lh, "", src.ETag); err != nil {
		_ = sys.Client.UnlockObject(ctx, uri, lh)
		t.Fatalf("[%s] SetSource: %v", sys.Name, err)
	}
	if err := sys.Client.UnlockObject(ctx, uri, lh); err != nil {
		t.Fatalf("[%s] UnlockObject: %v", sys.Name, err)
	}
	return uri
}

// TestDeleteObject_AfterLockObject_MultiSystem_Integration is the live
// regression test for adtler#187, first failure mode. The sequence is exactly
// the one the DeleteObject signature invites: LockObject, then DeleteObject
// with the handle.
//
// Before the fix, SAP S/4HANA refused the delete with 403
// ExceptionResourceNoAccess ("is currently editing"), because the delete runs
// in another session than the caller's lock, and left an orphaned lock behind.
// SAP ERP 6.0 (R/3) deleted the program either way. After the fix DeleteObject
// releases the caller's lock first, and both systems must delete it.
//
// What to look for in the output: both subtests PASS. A 403 mentioning
// "currently editing" means the lock release is broken again. A 412
// ExceptionPreconditionFailed means the second failure mode below is still
// open.
func TestDeleteObject_AfterLockObject_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			uri := createUnactivatedTempProgram(t, sys, "DELLOCK")

			lh, err := sys.Client.LockObject(ctx, uri)
			if err != nil {
				t.Fatalf("[%s] LockObject: %v", sys.Name, err)
			}
			if err := sys.Client.DeleteObject(ctx, uri, lh, ""); err != nil {
				t.Fatalf("[%s] DeleteObject with the lock handle: %v", sys.Name, err)
			}
			requireProgramGone(t, sys.Client, uri)
			t.Logf("[%s] lock-then-delete removed the program", sys.Name)
		})
	}
}

// TestDeleteObject_NeverActivatedProgram_MultiSystem_Integration covers
// adtler#187, second failure mode, without any lock involved: a program that
// was created and written but never activated has only an inactive version.
// On SAP S/4HANA the delete of such a program was refused with 412
// ExceptionPreconditionFailed ("Client ETag ... does not match the object
// ETag"), 42 times in the run that found it.
//
// What to look for in the output: both subtests PASS. A 412 on S/4HANA means
// the ETag problem is still open; read the message, it names both ETags, and
// compare the part after the leading digits.
func TestDeleteObject_NeverActivatedProgram_MultiSystem_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			uri := createUnactivatedTempProgram(t, sys, "DELINACT")

			if err := sys.Client.DeleteObject(context.Background(), uri, "", ""); err != nil {
				var adtErr *adt.ADTError
				if errors.As(err, &adtErr) && adtErr.StatusCode == 412 {
					t.Fatalf("[%s] DeleteObject of a never-activated program answered 412 (ETag mismatch): %v", sys.Name, err)
				}
				t.Fatalf("[%s] DeleteObject: %v", sys.Name, err)
			}
			requireProgramGone(t, sys.Client, uri)
			t.Logf("[%s] never-activated program deleted without a lock", sys.Name)
		})
	}
}

// TestVerifySource_LeavesNoProgramBehind_MultiSystem_Integration is the live
// regression test for the leak in adtler#187: every VerifySource call on S/4HANA
// left its temporary program in $TMP, because the cleanup locked the program
// and discarded the delete error.
//
// It counts the programs named like VerifySource's temporary ones before and
// after two calls, one valid and one broken, and fails if any new one remains.
// Object names are never logged, only counts. Before the fix this fails on
// S/4HANA with two new leftovers (and VerifySource itself now also returns an
// error naming the program when the cleanup fails).
//
// What to look for in the output: both subtests PASS and report "0 new
// leftovers". The search needs the new objects to be visible to the quick
// search; a skipped subtest means the search itself answered an error.
func TestVerifySource_LeavesNoProgramBehind_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			leftovers := func() map[string]string { // name -> URI
				found, err := sys.Client.SearchObjects(ctx, verifyTempPrefix+"*", "PROG/P", 1000)
				if err != nil {
					t.Skipf("[%s] cannot list leftovers via quick search: %v", sys.Name, err)
				}
				m := make(map[string]string, len(found))
				for _, o := range found {
					m[o.Name] = o.URI
				}
				return m
			}
			before := leftovers()

			for _, src := range []string{"REPORT zdummy.", "REPORT zdummy.\nIF 1 = 1."} {
				if _, _, err := sys.Client.VerifySource(ctx, src); err != nil {
					t.Errorf("[%s] VerifySource: %v", sys.Name, err)
				}
			}

			var added []string
			for name, uri := range leftovers() {
				if _, existed := before[name]; !existed {
					added = append(added, uri)
				}
			}
			for _, uri := range added {
				uri := uri
				t.Cleanup(func() { removeProgramIfPresent(t, sys.Client, uri) })
			}
			if len(added) > 0 {
				t.Fatalf("[%s] VerifySource left %d new temporary program(s) in $TMP", sys.Name, len(added))
			}
			t.Logf("[%s] %d existing leftovers before, 0 new leftovers after", sys.Name, len(before))
		})
	}
}
