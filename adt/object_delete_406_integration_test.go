//go:build integration

package adt_test

import (
	"context"
	"crypto/tls"
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
// The object is meant not to exist, so the read fails and no DELETE is sent.
// The client refuses to send a DELETE all the same, so an object that happens
// to carry this name is never removed: the test then fails instead.
func TestDeleteObject_KindWithoutAcceptEntry_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	const missingURI = "/sap/bc/adt/enhancements/enhoxh/zadtler_257_missing"

	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.TLSClientConfig = &tls.Config{InsecureSkipVerify: sys.Config.TLSSkipVerify} //nolint:gosec
			t.Cleanup(base.CloseIdleConnections)
			client := adt.NewClientWithTransport(sys.Config, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method == http.MethodDelete {
					return nil, errBlockedWrite
				}
				return base.RoundTrip(req)
			}))

			err := client.DeleteObject(ctx, missingURI, "", "")
			if err == nil {
				t.Fatal("DeleteObject of a missing enhancement implementation succeeded")
			}
			if errors.Is(err, errBlockedWrite) {
				t.Fatal("an enhancement implementation with the test's name exists; the DELETE was blocked, pick another name")
			}
			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("want an *adt.ADTError, got %T: %s", err, redact(err.Error(), sys.Config.Host, ""))
			}
			t.Logf("status %d, type %s", adtErr.StatusCode, adtErr.Type)
			if adtErr.StatusCode != http.StatusNotFound {
				t.Errorf("want 404 for the missing object, got %d (%s): the ETag read did not get past the Accept check", adtErr.StatusCode, adtErr.Type)
			}
		})
	}
}
