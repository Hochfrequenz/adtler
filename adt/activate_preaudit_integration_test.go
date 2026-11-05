//go:build integration && transport

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestActivateObjects_TransportableClass_Integration is the regression test
// for #279: a class created in a request and written with a method is
// activated by ActivateObjects, leaving none of its parts inactive.
//
// On SAP_BASIS 750 the first activation POST does not activate such a class.
// SAP answers with a pre-audit list of the class's inactive parts and expects
// them back; before #279 the client reported "SAP silently did not activate
// it". A $TMP class does not get that answer, so the class is created in a
// request. The class is deleted afterwards; on a release without ADT entry
// removal (#276) the request keeps its entries and stays behind.
func TestActivateObjects_TransportableClass_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			c := sys.Client
			name := fmt.Sprintf("ZCL_ADT279_%d", time.Now().UnixNano()%1000000000)
			uri := "/sap/bc/adt/oo/classes/" + strings.ToLower(name)

			request := createDisposableTransport(t, c, "K", "adtler #279 activation pre-audit")
			t.Cleanup(func() { cleanupTransportEntries(t, c, request) })
			if err := c.CreateObject(ctx, "CLAS", name, testPackage, "adtler #279", request); err != nil {
				t.Fatalf("CreateObject: %v", err)
			}
			t.Cleanup(func() {
				if err := c.DeleteObject(context.Background(), uri, "", request); err != nil {
					t.Logf("[cleanup] DeleteObject: %v", err)
				}
			})
			_ = c.Logout(ctx)

			src, err := c.GetSource(ctx, uri)
			if err != nil {
				t.Fatalf("GetSource: %v", err)
			}
			lock, err := c.LockObject(ctx, uri)
			if err != nil {
				t.Fatalf("LockObject: %v", err)
			}
			_, err = c.SetSource(ctx, uri, classPool70Source(name, "279"), lock, request, src.ETag)
			_ = c.UnlockObject(ctx, uri, lock)
			if err != nil {
				t.Fatalf("SetSource: %v", err)
			}

			pending := inactiveUnder(t, c, uri)
			if len(pending) == 0 {
				t.Fatal("precondition: no part of the class is inactive after the write")
			}

			res, err := c.ActivateObjects(ctx, []string{uri})
			if err != nil {
				t.Fatalf("ActivateObjects: %v", err)
			}
			for _, m := range res.Messages {
				t.Logf("  [%s] %s", m.Type, m.Text)
			}
			if !res.Success {
				t.Fatalf("ActivateObjects reported failure (%d parts were inactive)", len(pending))
			}
			if left := inactiveUnder(t, c, uri); len(left) > 0 {
				t.Fatalf("%d parts still inactive after activation", len(left))
			}
			t.Logf("activated %d inactive parts", len(pending))
		})
	}
}
