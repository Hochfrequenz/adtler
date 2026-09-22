//go:build integration

package adt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// redact removes values that must not reach a test log or a pull request.
//
// Two kinds of value leak through error text here. ADT messages echo the
// requested object back verbatim — a 404 for a behavior definition reads
// "Inactive version for BDEF <NAME> does not exist" — and Go's transport
// errors embed the full request URL, which carries the internal host name and
// port. Both are forbidden in this public repository, and the only path that
// prints them is the failure path: exactly the output someone pastes into a
// pull request.
func redact(text, host, name string) string {
	for _, r := range []struct{ secret, placeholder string }{
		{host, "<host>"},
		{name, "<name>"},
	} {
		if r.secret == "" {
			continue
		}
		for _, variant := range []string{r.secret, strings.ToLower(r.secret), strings.ToUpper(r.secret)} {
			text = strings.ReplaceAll(text, variant, r.placeholder)
		}
	}
	return text
}

// searchFixtures runs a discovery search and separates the three outcomes that
// look alike from the outside.
//
// A failed search must not become a green skip — that is the blind spot
// adtler#149 shipped with. But "failed" is not one thing: if SAP answered and
// the answer was an error (a rejected credential, a 500, a refused request),
// something is wrong and the test must fail. If SAP never answered at all — a
// timeout or a connection failure — nothing was measured and nothing can be,
// so the test skips and says why. The ECC system reaches the second case for
// enhancement implementations: that search does not return within this
// client's 30-second timeout.
func searchFixtures(t *testing.T, ctx context.Context, sys integrationSystem, adtType string) []adt.ObjectInfo {
	t.Helper()
	results, err := sys.Client.SearchObjects(ctx, "*", adtType, 10)
	if err == nil {
		return results
	}
	var adtErr *adt.ADTError
	if errors.As(err, &adtErr) {
		t.Fatalf("searching for %s objects: SAP answered with an error (status %d, type %q)",
			adtType, adtErr.StatusCode, adtErr.Type)
	}
	t.Skipf("searching for %s objects never reached the system, so nothing could be measured: %s",
		adtType, redact(err.Error(), sys.Config.Host, ""))
	return nil
}

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
				assertRAPSourceReadable(t, ctx, sys, "BDEF", "BDEF/BDO", "define behavior")
			})
			t.Run("SRVD", func(t *testing.T) {
				assertRAPSourceReadable(t, ctx, sys, "SRVD", "SRVD/SRV", "define service")
			})
			t.Run("SRVB", func(t *testing.T) {
				assertServiceBindingReadable(t, ctx, sys)
			})
		})
	}
}

// findRAPObject returns one object of the given ADT type from the target
// system, or skips the test when the system holds none.
func findRAPObject(t *testing.T, ctx context.Context, sys integrationSystem, adtType string) adt.ObjectInfo {
	t.Helper()
	results := searchFixtures(t, ctx, sys, adtType)
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
func assertRAPSourceReadable(t *testing.T, ctx context.Context, sys integrationSystem, objectType, adtType, wantKeyword string) {
	t.Helper()
	obj := findRAPObject(t, ctx, sys, adtType)

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

	src, err := sys.Client.GetSource(ctx, built)
	if err != nil {
		t.Fatalf("GetSource for %s: %s", objectType, redact(err.Error(), sys.Config.Host, obj.Name))
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
func assertServiceBindingReadable(t *testing.T, ctx context.Context, sys integrationSystem) {
	t.Helper()
	obj := findRAPObject(t, ctx, sys, "SRVB/SVB")

	built, err := adt.ObjectURI("SRVB", obj.Name)
	if err != nil {
		t.Fatalf("ObjectURI for a service binding: %v", err)
	}
	if !strings.EqualFold(built, obj.URI) {
		t.Fatalf("SRVB: built URI does not match the URI the system reports")
	}

	info, err := sys.Client.GetObjectInfo(ctx, built)
	if err != nil {
		t.Fatalf("GetObjectInfo for SRVB: %s — this is the 406 adtler#65 recorded",
			redact(err.Error(), sys.Config.Host, obj.Name))
	}
	if !strings.EqualFold(info.Name, obj.Name) {
		t.Errorf("GetObjectInfo returned a different object than the one requested")
	}

	// A service binding is configuration and carries no source. Recorded
	// here so the absence stays a measured fact rather than an assumption
	// the next reader has to re-establish.
	if _, err := sys.Client.GetSource(ctx, built); err == nil {
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
			results := searchFixtures(t, ctx, sys, "ENHO")
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
				t.Fatalf("GetObjectInfo on an unmapped object kind: %s — the 406 fallback did not save it",
					redact(err.Error(), sys.Config.Host, target.Name))
			}
			if !strings.EqualFold(info.Name, target.Name) {
				t.Errorf("GetObjectInfo returned a different object than the one requested")
			}
		})
	}
}
