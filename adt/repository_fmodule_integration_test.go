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

// fmoduleSearchLimit bounds the discovery search. One hit is enough; a few
// more let the test skip an entry whose URI is not a function module URI.
const fmoduleSearchLimit = 10

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
// on a system without any function module. It logs counts and booleans, never object names.
//
// What to look for in the -v output, per system: "first request carried the
// fmodules media type" must be true. "requests for the object" is 1 on a
// system that knows fmodules.v3 and 2 where the 406 retry still has to rescue
// the call (an older release, expected and not a failure). "package present"
// being false is the open question from the issue (does the function module
// document carry a package reference at all); it is reported, not asserted.
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

			hits, err := client.SearchObjects(ctx, "*", fmoduleSearchType, fmoduleSearchLimit)
			if err != nil {
				t.Fatalf("SearchObjects for function modules: %s", redact(err.Error(), sys.Config.Host, ""))
			}
			var uri string
			for _, h := range hits {
				if strings.Contains(h.URI, "/functions/groups/") && strings.Contains(h.URI, "/fmodules/") {
					uri = h.URI
					break
				}
			}
			if uri == "" {
				t.Skipf("no function module found on this system (%d search hits)", len(hits))
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
		})
	}
}
