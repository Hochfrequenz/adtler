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
// On a system without them (SAP ERP 6.0) the create must fail with the
// "not available on this SAP system" message instead of a raw 404.
//
// The handler class is discovered: the test borrows the one of an existing
// HTTP service. It is never called; the service gets no ICF node.
func TestHTTPService_RoundTrip_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			handler := discoverHTTPHandlerClass(t, sys)
			name := fmt.Sprintf("ZADTLER_HT%05d", rand.Intn(100000)) //nolint:gosec // throwaway name, not security-sensitive
			uri, err := adt.ObjectURI("HTTP", name)
			if err != nil {
				t.Fatal(err)
			}

			if handler == "" {
				err := sys.Client.CreateHTTPService(ctx, name, "$TMP", "adtler#255 integration test", "CL_EXAMPLE", "")
				if err == nil {
					removeProgramIfPresent(t, sys.Client, uri)
					t.Fatalf("[%s] found no HTTP services, yet CreateHTTPService succeeded", sys.Name)
				}
				if !strings.Contains(err.Error(), "not available on this SAP system") {
					t.Fatalf("[%s] want the 'not available' message, got: %s", sys.Name, redact(err.Error(), sys.Config.Host, ""))
				}
				t.Logf("[%s] no HTTP services on this system; create refused as expected", sys.Name)
				return
			}

			if err := sys.Client.CreateHTTPService(ctx, name, "$TMP", "adtler#255 integration test", handler, ""); err != nil {
				t.Fatalf("[%s] CreateHTTPService: %s", sys.Name, redact(err.Error(), sys.Config.Host, ""))
			}
			t.Cleanup(func() { removeProgramIfPresent(t, sys.Client, uri) })

			svc, err := sys.Client.GetHTTPService(ctx, name)
			if err != nil {
				t.Fatalf("[%s] GetHTTPService: %s", sys.Name, redact(err.Error(), sys.Config.Host, ""))
			}
			if svc.HandlerClass != handler || svc.Name != name || svc.URL == "" {
				t.Errorf("[%s] read back name=%q handler matches=%v url set=%v", sys.Name, svc.Name, svc.HandlerClass == handler, svc.URL != "")
			}

			res, err := sys.Client.ActivateObjects(ctx, []string{uri})
			if err != nil || !res.Success {
				t.Fatalf("[%s] ActivateObjects: err=%v result=%+v", sys.Name, err, res)
			}

			if err := sys.Client.DeleteObject(ctx, uri, "", ""); err != nil {
				t.Fatalf("[%s] DeleteObject: %s", sys.Name, redact(err.Error(), sys.Config.Host, ""))
			}
			if _, err := sys.Client.GetObjectInfo(ctx, uri); adt.ClassifyError(err) != adt.ErrorNotFound {
				t.Errorf("[%s] after DeleteObject, GetObjectInfo should report not found, got: %v", sys.Name, err)
			}
			t.Logf("[%s] create → read handler → activate → delete OK", sys.Name)
		})
	}
}

// discoverHTTPHandlerClass returns the handler class of an existing HTTP
// service, or "" when the system has none.
func discoverHTTPHandlerClass(t *testing.T, sys integrationSystem) string {
	t.Helper()
	ctx := context.Background()
	found, err := sys.Client.SearchObjects(ctx, "*", "HTTP", 20)
	if err != nil {
		t.Logf("[%s] searching HTTP services: %s", sys.Name, redact(err.Error(), sys.Config.Host, ""))
		return ""
	}
	for _, o := range found {
		if svc, err := sys.Client.GetHTTPService(ctx, o.Name); err == nil && svc.HandlerClass != "" {
			t.Logf("[%s] borrowing the handler class of one of %d HTTP services", sys.Name, len(found))
			return svc.HandlerClass
		}
	}
	return ""
}
