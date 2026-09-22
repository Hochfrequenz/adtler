//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRAPObjectURIs_Integration is the live half of adtler#65. It proves the
// three claims the fix rests on, against whatever RAP objects the target
// system happens to hold:
//
//  1. the URI ObjectURI builds is the URI the system itself reports for the
//     object, so this client and ADT agree on where these kinds live;
//  2. a behavior definition and a service definition return their source
//     through GetSource at that URI; and
//  3. a service binding answers GetObjectInfo — the request that used to
//     fail with 406 — while having no source at all.
//
// Fixtures are discovered, never hardcoded: a system with no RAP objects
// (an ECC system has none, and RAP needs a suitable client) skips. Only
// counts and kinds are logged, never object names.
func TestRAPObjectURIs_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()

			t.Run("BDEF", func(t *testing.T) {
				assertRAPSourceReadable(t, ctx, sys.Client, "BDEF", "BDEF/BDO", "define behavior")
			})
			t.Run("SRVD", func(t *testing.T) {
				assertRAPSourceReadable(t, ctx, sys.Client, "SRVD", "SRVD/SRV", "define service")
			})
			t.Run("SRVB", func(t *testing.T) {
				assertServiceBindingReadable(t, ctx, sys.Client)
			})
		})
	}
}

// findRAPObject returns one object of the given ADT type from the target
// system, or skips the test when the system holds none.
func findRAPObject(t *testing.T, ctx context.Context, client adt.Client, adtType string) adt.ObjectInfo {
	t.Helper()
	results, err := client.SearchObjects(ctx, "*", adtType, 10)
	if err != nil {
		t.Skipf("searching for %s objects failed on this system (%v) — nothing to measure here", adtType, err)
	}
	t.Logf("system holds %d %s object(s) in the first page of results", len(results), adtType)
	for _, r := range results {
		if r.Name != "" && r.URI != "" {
			return r
		}
	}
	t.Skipf("no %s object on this system — RAP objects need S/4HANA and a suitable client", adtType)
	return adt.ObjectInfo{}
}

// assertRAPSourceReadable checks that ObjectURI agrees with the system about
// where the object lives, and that GetSource returns its source there.
func assertRAPSourceReadable(t *testing.T, ctx context.Context, client adt.Client, objectType, adtType, wantKeyword string) {
	t.Helper()
	obj := findRAPObject(t, ctx, client, adtType)

	built, err := adt.ObjectURI(objectType, obj.Name)
	if err != nil {
		t.Fatalf("ObjectURI(%q, ...): %v", objectType, err)
	}
	// ADT reports the URI it considers canonical; ObjectURI lower-cases the
	// name, so compare case-insensitively. A mismatch here is the bug
	// adtler#65 describes — this client addressing a kind at a path the
	// system does not serve.
	if !strings.EqualFold(built, obj.URI) {
		t.Fatalf("%s: built URI does not match the URI the system reports (paths differ beyond case)", objectType)
	}

	src, err := client.GetSource(ctx, built)
	if err != nil {
		t.Fatalf("GetSource for %s: %v", objectType, err)
	}
	if strings.TrimSpace(src.Source) == "" {
		t.Fatalf("GetSource for %s returned empty source", objectType)
	}
	if !strings.Contains(strings.ToLower(src.Source), wantKeyword) {
		t.Errorf("%s source does not contain %q — is this really a %s document?", objectType, wantKeyword, objectType)
	}
	t.Logf("%s: read %d bytes of source at the built URI", objectType, len(src.Source))
}

// assertServiceBindingReadable covers the kind that has no source. Reading
// its object document is the only thing to do with it, and that request is
// the one adtler#65 recorded as a 406 and mistook for a missing endpoint.
func assertServiceBindingReadable(t *testing.T, ctx context.Context, client adt.Client) {
	t.Helper()
	obj := findRAPObject(t, ctx, client, "SRVB/SVB")

	built, err := adt.ObjectURI("SRVB", obj.Name)
	if err != nil {
		t.Fatalf("ObjectURI(\"SRVB\", ...): %v", err)
	}
	if !strings.EqualFold(built, obj.URI) {
		t.Fatalf("SRVB: built URI does not match the URI the system reports")
	}

	info, err := client.GetObjectInfo(ctx, built)
	if err != nil {
		t.Fatalf("GetObjectInfo for SRVB: %v — this is the 406 adtler#65 recorded", err)
	}
	if !strings.EqualFold(info.Name, obj.Name) {
		t.Errorf("GetObjectInfo returned a different object than the one requested")
	}

	// A service binding is configuration and carries no source. Recorded
	// here so the absence stays a measured fact rather than an assumption
	// the next reader has to re-establish.
	if _, err := client.GetSource(ctx, built); err == nil {
		t.Errorf("GetSource for SRVB succeeded — a service binding was measured to have no source; re-check adtler#65")
	}
}

// TestAcceptFallback_UnmappedObjectKind_Integration is the live proof of the
// rule adtler#65 turned up, tested where it matters most: on an object kind
// this client has no entry for at all.
//
// An enhancement implementation is such a kind. acceptHeaderForURI finds no
// prefix for it and falls back to "application/xml", which ADT refuses with
// 406 — measured on SAP S/4HANA on-premise (SAP_BASIS 816, S4CORE 109),
// where both enhancement sub-kinds present answered 406 to
// "application/xml" and 200 to */*. So this test passes only because of the
// fallback, never because the kind was catalogued.
//
// That is the point: the catalogue will always be behind SAP, and this is
// what stops a missing entry from reading as a missing endpoint.
func TestAcceptFallback_UnmappedObjectKind_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			results, err := sys.Client.SearchObjects(ctx, "*", "ENHO", 10)
			if err != nil {
				t.Skipf("searching for enhancement implementations failed on this system: %v", err)
			}
			var target adt.ObjectInfo
			for _, r := range results {
				if r.URI != "" && r.Name != "" {
					target = r
					break
				}
			}
			if target.URI == "" {
				t.Skip("no enhancement implementation on this system")
			}
			t.Logf("system holds %d enhancement implementation(s) in the first page; probing kind %s", len(results), target.Type)

			info, err := sys.Client.GetObjectInfo(ctx, target.URI)
			if err != nil {
				t.Fatalf("GetObjectInfo on an unmapped object kind: %v — the 406 fallback did not save it", err)
			}
			if !strings.EqualFold(info.Name, target.Name) {
				t.Errorf("GetObjectInfo returned a different object than the one requested")
			}
		})
	}
}
