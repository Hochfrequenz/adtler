//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestDeletePackage_MultiSystem_Integration is the live regression test for
// adtler#150: DeleteObject could not delete a package, because SAP S/4HANA
// answered the If-Match header with 412 ExceptionPreconditionFailed: the ETag a
// GET returns never equals the one the server compares a DELETE against.
//
// The test creates a local package with a unique name, deletes it with
// DeleteObject, and then reads it again, which must answer "not found". The
// name is random per run, so nothing is reused from an earlier run, and a 412
// on the delete fails the test instead of being worked around.
//
// The package endpoint is an S/4 collection and does not exist on SAP ERP 6.0
// (R/3), where the subtest skips.
//
// What to look for in the output: on S/4HANA the subtest PASSes and logs that
// the package was created and deleted. A failure with "412
// (ExceptionPreconditionFailed)" means the DELETE without a precondition was
// refused too. If the test fails, the package it created is still there and the
// failure message names it, so remove it with SE80 or SE21.
//
// Why this test cannot pass without the fix: before it, the only DELETE that
// DeleteObject sent carried an ETag, which the server refuses for a package
// (that is the whole of #150, measured on 2026-10-09 by reverting the fix). A
// read-back that still finds the package, or any non-404 answer, also fails
// the test.
func TestDeletePackage_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			name := fmt.Sprintf("$ZADTLER_DEL_%06d", rand.Intn(1000000)) //nolint:gosec // throwaway name, not security-sensitive
			uri := "/sap/bc/adt/packages/" + strings.ReplaceAll(strings.ToLower(name), "$", "%24")

			err := sys.Client.CreatePackage(ctx, name, "adtler#150 delete test", sys.Config.User, "LOCAL", "", "")
			if endpointUnavailable(err) {
				t.Skipf("[%s] /sap/bc/adt/packages is not available on this release", sys.Name)
			}
			if err != nil {
				t.Fatalf("[%s] CreatePackage: %v", sys.Name, err)
			}
			t.Cleanup(func() {
				if _, err := sys.Client.GetObjectInfo(context.Background(), uri); err != nil {
					return // gone, which is the happy path
				}
				if err := sys.Client.DeleteObject(context.Background(), uri, "", ""); err != nil {
					t.Errorf("[%s] the test package %s is left behind and cannot be deleted, remove it by hand: %v", sys.Name, name, err)
				}
			})
			if _, err := sys.Client.GetObjectInfo(ctx, uri); err != nil {
				t.Fatalf("[%s] the package just created cannot be read back: %v", sys.Name, err)
			}

			if err := sys.Client.DeleteObject(ctx, uri, "", ""); err != nil {
				t.Fatalf("[%s] DeleteObject of package %s: %v", sys.Name, name, err)
			}

			_, err = sys.Client.GetObjectInfo(ctx, uri)
			if err == nil {
				t.Fatalf("[%s] package %s is still there after DeleteObject", sys.Name, name)
			}
			if kind := adt.ClassifyError(err); kind != adt.ErrorNotFound {
				t.Fatalf("[%s] reading the deleted package should answer not found, got kind %s: %v", sys.Name, kind, err)
			}
			t.Logf("[%s] created and deleted a local package", sys.Name)
		})
	}
}
