//go:build integration

package adt_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestDeleteObject_KindWithoutAcceptEntry_MultiSystem_Integration covers
// adtler#257 on every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// An enhancement implementation has no entry in this client's Accept catalogue,
// so its ETag read asks for "application/xml". Both SAP ERP 6.0 and SAP S/4HANA
// check the Accept header before they look the object up: for a missing object
// they answer that offer with 406 and */* with 404. Before the fix
// DeleteObject stopped at the 406; with the */* retry the read reaches the
// object lookup, and a missing object comes back as a 404.
//
// The object does not exist, so the test creates and deletes nothing: the read
// fails and no DELETE is sent.
func TestDeleteObject_KindWithoutAcceptEntry_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	const missingURI = "/sap/bc/adt/enhancements/enhoxh/zadtler_257_missing"

	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			err := sys.Client.DeleteObject(ctx, missingURI, "", "")
			if err == nil {
				t.Fatal("DeleteObject of a missing enhancement implementation succeeded")
			}
			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("want an *adt.ADTError, got %T: %v", err, err)
			}
			t.Logf("status %d, type %s", adtErr.StatusCode, adtErr.Type)
			if adtErr.StatusCode != http.StatusNotFound {
				t.Errorf("want 404 for the missing object, got %d (%s): the ETag read did not get past the Accept check", adtErr.StatusCode, adtErr.Type)
			}
		})
	}
}
