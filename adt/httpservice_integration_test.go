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

// TestHTTPService_RoundTrip_MultiSystem_Integration covers adtler#255.
//
// On a system that has HTTP services (S/4HANA) it creates one in $TMP with a
// handler class, reads the handler back, activates it and deletes it again.
// The delete is part of the assertion: before HTTP had an Accept entry,
// DeleteObject's ETag read was answered 406.
//
// On SAP ERP 6.0, which has no HTTP services, the create must fail with the
// "not available on this SAP system" message instead of a raw 404.
//
// The handler class is discovered: the test borrows the one of an existing
// HTTP service. It is never called; the service gets no ICF node.
func TestHTTPService_RoundTrip_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			name := fmt.Sprintf("ZADTLER_HT%05d", rand.Intn(100000)) //nolint:gosec // throwaway name, not security-sensitive
			uri, err := adt.ObjectURI("HTTP", name)
			if err != nil {
				t.Fatal(err)
			}
			// A 401 is final under Basic auth: after one, neither the test nor
			// its cleanup may send the credential again (repository CLAUDE.md).
			authFailed := false
			redactErr := func(err error) string {
				if isUnauthorizedErr(err) {
					authFailed = true
				}
				return redact(err.Error(), sys.Config.Host, sys.Config.User)
			}
			cleanup := func() { deleteLeftover(t, sys.Client, &authFailed, sys.Name, name, uri, redactErr) }

			flavor, err := sys.Client.SystemFlavor(ctx)
			if err != nil {
				t.Fatalf("[%s] SystemFlavor: %s", sys.Name, redactErr(err))
			}

			if flavor == adt.SystemFlavorECC {
				err := sys.Client.CreateHTTPService(ctx, name, "$TMP", "adtler#255 integration test", "CL_EXAMPLE", "")
				if err == nil {
					cleanup()
					t.Fatalf("[%s] ECC system, yet CreateHTTPService succeeded", sys.Name)
				}
				if !strings.Contains(err.Error(), "not available on this SAP system") {
					t.Fatalf("[%s] want the 'not available' message, got: %s", sys.Name, redactErr(err))
				}
				t.Logf("[%s] ECC system; create refused as expected", sys.Name)
				return
			}

			handler := discoverHTTPHandlerClass(t, sys, redactErr)
			if handler == "" {
				t.Skipf("[%s] found no HTTP service with a handler class to borrow", sys.Name)
			}

			if err := sys.Client.CreateHTTPService(ctx, name, "$TMP", "adtler#255 integration test", handler, ""); err != nil {
				t.Fatalf("[%s] CreateHTTPService: %s", sys.Name, redactErr(err))
			}
			t.Cleanup(cleanup)

			svc, err := sys.Client.GetHTTPService(ctx, name)
			if err != nil {
				t.Fatalf("[%s] GetHTTPService: %s", sys.Name, redactErr(err))
			}
			if svc.HandlerClass != handler || svc.Name != name || svc.URL == "" {
				t.Errorf("[%s] read back name=%q handler matches=%v url set=%v", sys.Name, svc.Name, svc.HandlerClass == handler, svc.URL != "")
			}

			res, err := sys.Client.ActivateObjects(ctx, []string{uri})
			if err != nil {
				t.Fatalf("[%s] ActivateObjects: %s", sys.Name, redactErr(err))
			}
			if !res.Success {
				t.Fatalf("[%s] activation failed with %d message(s)", sys.Name, len(res.Messages))
			}

			if err := sys.Client.DeleteObject(ctx, uri, "", ""); err != nil {
				t.Fatalf("[%s] DeleteObject: %s", sys.Name, redactErr(err))
			}
			if _, err := sys.Client.GetObjectInfo(ctx, uri); adt.ClassifyError(err) != adt.ErrorNotFound {
				authFailed = authFailed || isUnauthorizedErr(err)
				t.Errorf("[%s] after DeleteObject, GetObjectInfo should report not found, got kind %s", sys.Name, adt.ClassifyError(err))
			}
			t.Logf("[%s] create → read handler → activate → delete OK", sys.Name)
		})
	}
}

// discoverHTTPHandlerClass returns the handler class of an existing HTTP
// service, or "" when the system has none. Any failed request fails the test:
// a 401 must not be repeated in the loop, and a read regression must not
// pass as a skip.
func discoverHTTPHandlerClass(t *testing.T, sys integrationSystem, redactErr func(error) string) string {
	t.Helper()
	ctx := context.Background()
	found, err := sys.Client.SearchObjects(ctx, "*", "HTTP", 20)
	if err != nil {
		t.Fatalf("[%s] searching HTTP services: %s", sys.Name, redactErr(err))
	}
	for _, o := range found {
		svc, err := sys.Client.GetHTTPService(ctx, o.Name)
		if err != nil {
			t.Fatalf("[%s] GetHTTPService on a discovered service: %s", sys.Name, redactErr(err))
		}
		if svc.HandlerClass != "" {
			t.Logf("[%s] borrowing the handler class of one of %d HTTP services", sys.Name, len(found))
			return svc.HandlerClass
		}
	}
	return ""
}
