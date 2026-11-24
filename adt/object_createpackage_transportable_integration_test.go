//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestCreatePackage_Transportable_MultiSystem_Integration is the live test for
// adtler#291: a package with a transport layer needs change recording, and on
// S/4HANA a new top-level transportable package has to be a structure package
// with the development package below it.
//
// It creates a structure package and a development package below it, reads
// both back and removes them again, development package first. Both names are
// random per run.
//
// The request, software component and transport layer cannot be discovered, so
// they come from the environment; the test skips when one is unset:
//
//	ADTLER_TEST_TRANSPORT            a modifiable workbench request of the test user
//	ADTLER_TEST_SOFTWARE_COMPONENT   a software component that takes transportable packages
//	ADTLER_TEST_TRANSPORT_LAYER      the transport layer of that component
//
// The package endpoint is an S/4 collection and does not exist on SAP ERP 6.0
// (R/3), where the subtest skips. If the test fails, the packages it created
// may still be there; the failure message names them.
func TestCreatePackage_Transportable_MultiSystem_Integration(t *testing.T) {
	transport := os.Getenv("ADTLER_TEST_TRANSPORT")
	component := os.Getenv("ADTLER_TEST_SOFTWARE_COMPONENT")
	layer := os.Getenv("ADTLER_TEST_TRANSPORT_LAYER")
	if transport == "" || component == "" || layer == "" {
		t.Skip("ADTLER_TEST_TRANSPORT, ADTLER_TEST_SOFTWARE_COMPONENT and ADTLER_TEST_TRANSPORT_LAYER are not all set")
	}
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			suffix := rand.Intn(1000000) //nolint:gosec // throwaway name, not security-sensitive
			structure := fmt.Sprintf("ZADTLER_STRUKT_%06d", suffix)
			development := fmt.Sprintf("ZADTLER_DEV_%06d", suffix)

			// remove deletes a package the test created, and only reports a
			// failure so a leftover is named.
			remove := func(name string) {
				uri := "/sap/bc/adt/packages/" + strings.ToLower(name)
				if err := sys.Client.DeleteObject(context.Background(), uri, "", transport); err != nil &&
					adt.ClassifyError(err) != adt.ErrorNotFound {
					t.Errorf("[%s] the test package %s is left behind, remove it by hand: %s",
						sys.Name, name, redact(err.Error(), sys.Config.Host, sys.Config.User))
				}
			}

			err := sys.Client.CreatePackage(ctx, structure, "adtler#291 structure package", sys.Config.User,
				component, layer, "", transport, adt.WithPackageType(adt.PackageTypeStructure))
			if endpointUnavailable(err) {
				t.Skipf("[%s] /sap/bc/adt/packages is not available on this release", sys.Name)
			}
			if err != nil {
				t.Fatalf("[%s] CreatePackage (structure): %s", sys.Name, redact(err.Error(), sys.Config.Host, sys.Config.User))
			}
			t.Cleanup(func() { remove(structure) })

			err = sys.Client.CreatePackage(ctx, development, "adtler#291 development package", sys.Config.User,
				component, layer, "", transport, adt.WithSuperPackage(structure))
			if err != nil {
				t.Fatalf("[%s] CreatePackage (development below %s): %s", sys.Name, structure, redact(err.Error(), sys.Config.Host, sys.Config.User))
			}
			t.Cleanup(func() { remove(development) }) // runs before the structure package's

			for _, name := range []string{structure, development} {
				if _, err := sys.Client.GetObjectInfo(ctx, "/sap/bc/adt/packages/"+strings.ToLower(name)); err != nil {
					t.Errorf("[%s] package %s cannot be read back: %s", sys.Name, name, redact(err.Error(), sys.Config.Host, sys.Config.User))
				}
			}
			t.Logf("[%s] created a structure package and a development package below it", sys.Name)
		})
	}
}
