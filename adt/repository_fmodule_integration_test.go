//go:build integration

package adt_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// fmoduleSearchType is the object type a search filters on to return function
// modules.
const fmoduleSearchType = "FUGR/FF"

// fmoduleSearchLimit bounds each discovery search. A few hits per query let
// the test skip entries whose group names no package.
const fmoduleSearchLimit = 10

// fmoduleSearchQueries are common name prefixes of function modules in ordinary
// (not generated) function groups, tried in order.
var fmoduleSearchQueries = []string{"Z*", "RFC*", "STFC*", "BAPI*", "SY*"}

// fmoduleRequestRecorder remembers the Accept header of every GET that asked
// for one particular URI, in the order the requests left the client.
type fmoduleRequestRecorder struct {
	mu      sync.Mutex
	uri     string
	accepts []string
}

func (r *fmoduleRequestRecorder) watch(uri string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.uri = uri
	r.accepts = nil
}

func (r *fmoduleRequestRecorder) seen(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Namespaced names reach the wire percent-encoded, so compare both forms.
	if r.uri != "" && req.Method == http.MethodGet &&
		(req.URL.Path == r.uri || req.URL.EscapedPath() == r.uri) {
		r.accepts = append(r.accepts, req.Header.Get("Accept"))
	}
}

func (r *fmoduleRequestRecorder) acceptHeaders() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.accepts...)
}

// TestGetObjectInfo_FunctionModule_MultiSystem_Integration covers adtler#169
// on every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Before the fix, acceptHeaderForURI gave a function module URI the function
// group media type. SAP S/4HANA answers that with 406 and names
// functions.fmodules.v3+xml as the only type it produces for a function
// module; the */* retry in readWithAcceptFallback rescued the call, so it
// succeeded all the same and only cost an extra request. A test that asserts
// "the call succeeded" therefore passes without the fix. This one asserts what
// the FIRST request carried, which the retry cannot repair.
//
// The test reads only: it finds a function module through the system's own
// search, so it does not depend on a particular fixture object, and it skips
// on a system without a suitable function module. It logs counts and booleans, never object names.
//
// What to look for in the -v output, per system: "first request carried the
// fmodules media type" must be true. "requests for the object" is 1 on a
// system that knows fmodules.v3 and 2 where the 406 retry still has to rescue
// the call (an older release, expected and not a failure). "package present"
// is the other half of the issue: the function module document names no package
// on either release, so GetObjectInfo takes it from the function group. The test
// asserts that the package is set and equals the one the function group reports.
func TestGetObjectInfo_FunctionModule_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.TLSClientConfig = &tls.Config{InsecureSkipVerify: sys.Config.TLSSkipVerify} //nolint:gosec
			t.Cleanup(base.CloseIdleConnections)

			rec := &fmoduleRequestRecorder{}
			client := adt.NewClientWithTransport(sys.Config, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				rec.seen(req)
				return base.RoundTrip(req)
			}))

			// An unfiltered search returns the generated function groups of a
			// release first, and a generated group is in no package at all (no
			// TADIR entry; measured on SAP ERP 6.0), so an empty PackageName is
			// the right answer for them. Search by common name prefixes instead
			// and take the first function module whose group names a package.
			var uri, groupPackage string
			candidates := 0
			for _, query := range fmoduleSearchQueries {
				hits, err := client.SearchObjects(ctx, query, fmoduleSearchType, fmoduleSearchLimit)
				if err != nil {
					t.Fatalf("SearchObjects for function modules: %s", redact(err.Error(), sys.Config.Host, ""))
				}
				for _, h := range hits {
					i := strings.Index(h.URI, "/fmodules/")
					if !strings.Contains(h.URI, "/functions/groups/") || i < 0 {
						continue
					}
					candidates++
					group, err := client.GetObjectInfo(ctx, h.URI[:i])
					if err == nil && group.PackageName != "" {
						uri, groupPackage = h.URI, group.PackageName
						break
					}
				}
				if uri != "" {
					break
				}
			}
			if uri == "" {
				t.Skipf("no function module in a function group with a package found (%d candidates)", candidates)
			}

			rec.watch(uri)
			info, err := client.GetObjectInfo(ctx, uri)
			if err != nil {
				t.Fatalf("GetObjectInfo on a function module: %s", redact(err.Error(), sys.Config.Host, ""))
			}

			accepts := rec.acceptHeaders()
			if len(accepts) == 0 {
				t.Fatal("recorded no request for the function module URI")
			}
			firstCarriesType := strings.Contains(accepts[0], fmoduleAccept)
			t.Logf("first request carried the fmodules media type: %t", firstCarriesType)
			t.Logf("requests for the object: %d", len(accepts))
			t.Logf("package present: %t", info.PackageName != "")
			if !firstCarriesType {
				t.Errorf("first request offered %q, want it to contain %q — the 406 retry hides this from a success check", accepts[0], fmoduleAccept)
			}
			if info.URI != uri {
				t.Errorf("ObjectInfo.URI is not the URI that was fetched (empty: %t)", info.URI == "")
			}
			if info.Name == "" {
				t.Error("ObjectInfo.Name is empty")
			}

			// The function group chosen above names a package, so an empty one
			// here is the bug.
			if info.PackageName == "" {
				t.Fatal("ObjectInfo.PackageName is empty — the function module's package was not taken from its function group")
			}
			if info.PackageName != groupPackage {
				t.Error("function module package differs from its function group's")
			}
		})
	}
}

// fgroupSearchType is the object type a search filters on to return function
// groups.
const fgroupSearchType = "FUGR/F"

// TestGetObjectInfo_FunctionGroupInclude_MultiSystem_Integration covers
// adtler#232 on every system in the SAP_INTEGRATION_SYSTEMS whitelist: a function
// group include's document has no packageRef on SAP ERP 6.0 or SAP S/4HANA (only
// a containerRef to its group), so GetObjectInfo must take the package from the
// function group.
//
// The test reads only. It finds a function group with a package through the
// system's own search and then its includes by name prefix (L<group>*). The
// search runs without a type filter: filtering on the include type FUGR/I
// returns nothing on either release, although the unfiltered hits carry exactly
// that type (measured 2026-10-14). It logs counts and booleans, never object
// names.
func TestGetObjectInfo_FunctionGroupInclude_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			client := sys.Client
			var uri, groupPackage string
			groups := 0
			for _, query := range fmoduleSearchQueries {
				hits, err := client.SearchObjects(ctx, query, fgroupSearchType, fmoduleSearchLimit)
				if err != nil {
					t.Fatalf("SearchObjects for function groups: %s", redact(err.Error(), sys.Config.Host, ""))
				}
				for _, g := range hits {
					// A namespaced group's includes do not start with L<group>.
					if strings.HasPrefix(g.Name, "/") {
						continue
					}
					groups++
					group, err := client.GetObjectInfo(ctx, g.URI)
					if err != nil || group.PackageName == "" {
						continue
					}
					parts, err := client.SearchObjects(ctx, "L"+g.Name+"*", "", fmoduleSearchLimit)
					if err != nil {
						t.Fatalf("SearchObjects for function group includes: %s", redact(err.Error(), sys.Config.Host, g.Name))
					}
					for _, p := range parts {
						if strings.HasPrefix(p.URI, g.URI+"/includes/") {
							uri, groupPackage = p.URI, group.PackageName
							break
						}
					}
					if uri != "" {
						break
					}
				}
				if uri != "" {
					break
				}
			}
			if uri == "" {
				t.Skipf("no include of a function group with a package found (%d groups)", groups)
			}

			info, err := client.GetObjectInfo(ctx, uri)
			if err != nil {
				t.Fatalf("GetObjectInfo on a function group include: %s", redact(err.Error(), sys.Config.Host, ""))
			}
			t.Logf("package present: %t", info.PackageName != "")
			if info.URI != uri || info.Name == "" {
				t.Errorf("ObjectInfo does not describe the include (URI matches: %t, name empty: %t)", info.URI == uri, info.Name == "")
			}
			if info.PackageName == "" {
				t.Fatal("ObjectInfo.PackageName is empty — the include's package was not taken from its function group")
			}
			if info.PackageName != groupPackage {
				t.Error("include package differs from its function group's")
			}
		})
	}
}
