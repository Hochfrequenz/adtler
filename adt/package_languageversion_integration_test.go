//go:build integration

package adt_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestCreatePackage_LanguageVersion_MultiSystem_Integration is the live test
// for adtler#254. It creates a local package as an ABAP Cloud package, reads
// its language version back, creates a class in it and reads the class's
// language version, then deletes both.
//
// The read-back proves SAP took the attribute: a request whose
// pak:languageVersion SAP ignored would create a Standard ABAP package and
// fail here. The class answers the issue's second question, whether a new
// object takes the version of its package without being told.
//
// The package endpoint is an S/4 collection and does not exist on SAP ERP 6.0
// (R/3), where the subtest skips. If the test fails before its cleanup, the
// failure message names what is left behind; remove it with SE80.
func TestCreatePackage_LanguageVersion_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			n := rand.Intn(1000000) //nolint:gosec // throwaway name, not security-sensitive
			pkg := fmt.Sprintf("$ZADTLER_LV_%06d", n)
			pkgURI := "/sap/bc/adt/packages/" + strings.ReplaceAll(strings.ToLower(pkg), "$", "%24")
			// A 401 is final under Basic auth: after one, neither the test nor
			// its cleanups may send the credential again (repository CLAUDE.md).
			authFailed := false
			redactErr := func(err error) string {
				if isUnauthorizedErr(err) {
					authFailed = true
				}
				return redact(err.Error(), sys.Config.Host, sys.Config.User)
			}

			err := sys.Client.CreatePackage(ctx, pkg, "adtler#254 language version", sys.Config.User, "LOCAL", "", adt.LanguageVersionCloudDevelopment, "")
			if endpointUnavailable(err) {
				t.Skipf("[%s] /sap/bc/adt/packages is not available on this release", sys.Name)
			}
			if err != nil {
				t.Fatalf("[%s] CreatePackage: %s", sys.Name, redactErr(err))
			}
			t.Cleanup(func() { deleteLeftover(t, sys.Client, &authFailed, sys.Name, pkg, pkgURI, redactErr) })

			info, err := sys.Client.GetObjectInfo(ctx, pkgURI)
			if err != nil {
				t.Fatalf("[%s] reading the package back: %s", sys.Name, redactErr(err))
			}
			if info.PackageLanguageVersion != adt.LanguageVersionCloudDevelopment {
				t.Fatalf("[%s] package language version = %q, want %q", sys.Name, info.PackageLanguageVersion, adt.LanguageVersionCloudDevelopment)
			}

			class := fmt.Sprintf("ZCL_ADTLER_LV_%06d", n)
			if err := sys.Client.CreateObject(ctx, "CLAS", class, pkg, "adtler#254 language version", ""); err != nil {
				t.Fatalf("[%s] CreateObject in the ABAP Cloud package: %s", sys.Name, redactErr(err))
			}
			classURI, err := adt.ObjectURI("CLAS", class)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { deleteLeftover(t, sys.Client, &authFailed, sys.Name, class, classURI, redactErr) })

			classInfo, err := sys.Client.GetObjectInfo(ctx, classURI)
			if err != nil {
				t.Fatalf("[%s] reading the class back: %s", sys.Name, redactErr(err))
			}
			if classInfo.ABAPLanguageVersion != "cloudDevelopment" {
				t.Fatalf("[%s] a class created in an ABAP Cloud package has language version %q, want cloudDevelopment", sys.Name, classInfo.ABAPLanguageVersion)
			}
			t.Logf("[%s] ABAP Cloud package created; a class in it took language version %q", sys.Name, classInfo.ABAPLanguageVersion)
		})
	}
}

// deleteLeftover removes an object the test created, unless it is already
// gone, and reports one it cannot remove.
func deleteLeftover(t *testing.T, client adt.Client, authFailed *bool, sysName, name, uri string, redactErr func(error) string) {
	t.Helper()
	if *authFailed {
		t.Errorf("[%s] %s may be left behind; not cleaned up after a 401, remove it by hand", sysName, name)
		return
	}
	ctx := context.Background()
	if _, err := client.GetObjectInfo(ctx, uri); err != nil {
		if isUnauthorizedErr(err) {
			*authFailed = true
			t.Errorf("[%s] %s may be left behind; not cleaned up after a 401, remove it by hand", sysName, name)
			return
		}
		if adt.ClassifyError(err) == adt.ErrorNotFound {
			return
		}
	}
	if err := client.DeleteObject(ctx, uri, "", ""); err != nil {
		t.Errorf("[%s] %s is left behind and cannot be deleted, remove it by hand: %s", sysName, name, redactErr(err))
	}
}

// isUnauthorizedErr reports whether err carries SAP's 401.
func isUnauthorizedErr(err error) bool {
	var adtErr *adt.ADTError
	return errors.As(err, &adtErr) && adtErr.StatusCode == http.StatusUnauthorized
}
