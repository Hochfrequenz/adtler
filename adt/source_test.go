package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

// Test-local constants for source-op content-type assertions. Mirror
// adt.contentTypeTextPlain / adt.contentTypeTextPlainUTF8 (unexported
// from the production package).
const (
	testCTTextPlain     = `text/plain`
	testCTTextPlainUTF8 = `text/plain; charset=utf-8`
)

// discoveryXMLProgramsUTF8Only is a minimal discovery document advertising
// exactly one content type (the UTF-8 charset variant) for the programs
// collection. Shared across tests (and adt/discovery_accept_header_test.go)
// that need "discovery populated with this specific type" as their
// discriminating signal — goconst (CI-enabled, see .github/workflows/
// golangci-lint.yml) flags this exact literal once 3+ tests each declared
// their own copy.
const discoveryXMLProgramsUTF8Only = `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

func TestGetSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/programs/programs/ZTEST/source/main" {
			w.Header().Set("ETag", `"etag-abc123"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("REPORT ZTEST.\nWRITE 'Hello'."))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.GetSource(context.Background(), "/sap/bc/adt/programs/programs/ZTEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Source != "REPORT ZTEST.\nWRITE 'Hello'." {
		t.Errorf("source: got %q", result.Source)
	}
	if result.ETag != `"etag-abc123"` {
		t.Errorf("etag: got %q", result.ETag)
	}
}

func TestGetIncludeSource(t *testing.T) {
	// ZCL_TEST padded to 30 chars = ZCL_TEST=======================
	wantPath := "/sap/bc/adt/oo/classes/zcl_test/includes/testclasses"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wantPath {
			w.Header().Set("ETag", `"etag-incl"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("CLASS lcl_test DEFINITION FOR TESTING.\nENDCLASS."))
			return
		}
		t.Logf("unexpected path: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.GetIncludeSource(context.Background(), "/sap/bc/adt/oo/classes/zcl_test", "testclasses")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Source != "CLASS lcl_test DEFINITION FOR TESTING.\nENDCLASS." {
		t.Errorf("source: got %q", result.Source)
	}
	if result.ETag != `"etag-incl"` {
		t.Errorf("etag: got %q", result.ETag)
	}
}

func TestSetIncludeSource(t *testing.T) {
	wantPath := "/sap/bc/adt/oo/classes/zcl_test/includes/testclasses"
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotPath = r.URL.Path
		gotMethod = r.Method
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		gotBody = string(body)
		w.Header().Set("ETag", `"etag-new"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	newETag, err := client.SetIncludeSource(context.Background(),
		"/sap/bc/adt/oo/classes/zcl_test", "testclasses",
		"CLASS lcl_test DEFINITION FOR TESTING.\nENDCLASS.", "", "", `"etag-old"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: got %q, want PUT", gotMethod)
	}
	if gotPath != wantPath {
		t.Errorf("path: got %q, want %q", gotPath, wantPath)
	}
	if gotBody != "CLASS lcl_test DEFINITION FOR TESTING.\nENDCLASS." {
		t.Errorf("body: got %q", gotBody)
	}
	if newETag != `"etag-new"` {
		t.Errorf("etag: got %q", newETag)
	}
}

// captureIncludeIfMatch runs SetIncludeSource against a stub server that records
// the If-Match request header, and returns what was sent. Shared by the
// If-Match behaviour tests so they don't each repeat the stub-server boilerplate.
func captureIncludeIfMatch(t *testing.T, lockHandle, etag string) string {
	t.Helper()
	var gotIfMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotIfMatch = r.Header.Get("If-Match")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)
	if _, err := client.SetIncludeSource(context.Background(),
		"/sap/bc/adt/oo/classes/zcl_test", "testclasses",
		"CLASS lcl_test DEFINITION FOR TESTING.\nENDCLASS.", lockHandle, "", etag); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return gotIfMatch
}

func TestSetIncludeSource_NoETag(t *testing.T) {
	// Empty etag (initial write on an empty include) → no If-Match.
	if got := captureIncludeIfMatch(t, "", ""); got != "" {
		t.Errorf("If-Match should be empty for initial write, got %q", got)
	}
}

func TestSetIncludeSource_OmitsIfMatchWhenLocked(t *testing.T) {
	// aibap.mcp#436: with a lock handle, SetIncludeSource must NOT send If-Match
	// (the GET-derived ETag never matches SAP's class-level write precondition,
	// causing 412). The lock query parameter must still be sent.
	var gotIfMatch, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotIfMatch = r.Header.Get("If-Match")
		gotQuery = r.URL.RawQuery
		w.Header().Set("ETag", `"etag-new"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	// Locked write WITH a transport — the full real-world shape. If-Match must be
	// omitted, and both lockHandle and corrNr must ride the query string.
	newETag, err := client.SetIncludeSource(context.Background(),
		"/sap/bc/adt/oo/classes/zcl_test", "testclasses",
		"CLASS lcl_test DEFINITION FOR TESTING.\nENDCLASS.", "LOCKHANDLE123", "TR123", `"etag-old"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotIfMatch != "" {
		t.Errorf("If-Match should be omitted when a lock handle is present, got %q", gotIfMatch)
	}
	if !strings.Contains(gotQuery, "lockHandle=LOCKHANDLE123") {
		t.Errorf("lockHandle query param missing, got query %q", gotQuery)
	}
	if !strings.Contains(gotQuery, "corrNr=TR123") {
		t.Errorf("corrNr query param missing, got query %q", gotQuery)
	}
	if newETag != `"etag-new"` {
		t.Errorf("returned ETag: got %q, want %q", newETag, `"etag-new"`)
	}
}

func TestSetIncludeSource_KeepsIfMatchWhenUnlocked(t *testing.T) {
	// Without a lock handle there is no exclusivity guarantee, so If-Match is
	// still sent as a best-effort optimistic-concurrency check (unchanged).
	if got := captureIncludeIfMatch(t, "", `"etag-old"`); got != `"etag-old"` {
		t.Errorf("If-Match should be sent when unlocked, got %q", got)
	}
}

func TestSetSource(t *testing.T) {
	var gotMethod, gotIfMatch, gotContentType, gotBody string

	discoveryXML := discoveryXMLProgramsUTF8Only

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if r.URL.Path == "/sap/bc/adt/programs/programs/ZTEST/source/main" {
			gotMethod = r.Method
			gotIfMatch = r.Header.Get("If-Match")
			gotContentType = r.Header.Get("Content-Type")
			body := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(body)
			gotBody = string(body)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SetSource(context.Background(), "/sap/bc/adt/programs/programs/ZTEST", "REPORT ZTEST.\nNEW CODE.", "", "", `"etag-abc123"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: got %q, want PUT", gotMethod)
	}
	if gotIfMatch != `"etag-abc123"` {
		t.Errorf("If-Match: got %q", gotIfMatch)
	}
	if gotContentType != testCTTextPlainUTF8 {
		t.Errorf("Content-Type: got %q, want %q", gotContentType, testCTTextPlainUTF8)
	}
	if gotBody != "REPORT ZTEST.\nNEW CODE." {
		t.Errorf("body: got %q", gotBody)
	}
}

func TestSetSource_DDLSUsesQueryLockDeliveryAndOmitsIfMatch(t *testing.T) {
	// aibap.mcp#383: DDL sources need the lock handle as a ?lockHandle= query
	// param (header delivery 400/403s and never triggers the 423 retry), and
	// reject the GET-derived If-Match (#436-style), so it must be omitted when
	// locked. Contrast the program path (TestSetSource), which keeps both.
	var gotMethod, gotQuery, gotIfMatch, gotLockHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		gotIfMatch = r.Header.Get("If-Match")
		gotLockHeader = r.Header.Get("X-SAP-Lock-Handle")
		w.Header().Set("ETag", `"new-etag"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SetSource(context.Background(),
		"/sap/bc/adt/ddic/ddl/sources/zmycds",
		"define root view entity ZMYCDS as select from t000 { key mandt as Client }",
		"LOCKHANDLE1", "TR1", `"etag-from-get"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: got %q, want PUT", gotMethod)
	}
	if !strings.Contains(gotQuery, "lockHandle=LOCKHANDLE1") {
		t.Errorf("lock handle must be a query param for DDLS; query=%q", gotQuery)
	}
	if !strings.Contains(gotQuery, "corrNr=TR1") {
		t.Errorf("corrNr must be a query param; query=%q", gotQuery)
	}
	if gotIfMatch != "" {
		t.Errorf("If-Match must be omitted for a locked DDLS write, got %q", gotIfMatch)
	}
	if gotLockHeader != "" {
		t.Errorf("X-SAP-Lock-Handle header must NOT be used for DDLS, got %q", gotLockHeader)
	}
}

func TestSetSource_DDLSUnlockedKeepsIfMatch(t *testing.T) {
	// Without a lock handle there is nothing enforcing exclusivity, so a DDLS
	// write still sends the caller's If-Match as a best-effort check (and still
	// uses query delivery — no lock header).
	var gotIfMatch, gotLockHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotIfMatch = r.Header.Get("If-Match")
		gotLockHeader = r.Header.Get("X-SAP-Lock-Handle")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SetSource(context.Background(),
		"/sap/bc/adt/ddic/ddl/sources/zmycds", "define ...", "", "", `"etag-x"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotIfMatch != `"etag-x"` {
		t.Errorf("unlocked DDLS write should keep If-Match, got %q", gotIfMatch)
	}
	if gotLockHeader != "" {
		t.Errorf("X-SAP-Lock-Handle header must NOT be used for DDLS, got %q", gotLockHeader)
	}
}

func TestSourceContentType_DiscoveryEmpty_FallsBackToTextPlain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Empty discovery response
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClientForTest(cfg)

	got := client.SourceContentTypeForTest("/sap/bc/adt/programs/programs/ZTEST")
	if got != testCTTextPlain {
		t.Errorf("empty discovery: got %q, want %q", got, testCTTextPlain)
	}
}

func TestSourceContentType_DiscoveryAdvertisesType_UsesIt(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain; charset=utf-8</app:accept>
      <app:accept>text/plain</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClientForTest(cfg)

	// Force discovery load
	if err := client.LoadDiscoveryForTest(context.Background()); err != nil {
		t.Fatalf("LoadDiscoveryForTest: %v", err)
	}

	got := client.SourceContentTypeForTest("/sap/bc/adt/programs/programs/ZTEST")
	if got != testCTTextPlainUTF8 {
		t.Errorf("discovery-advertised: got %q, want %q", got, testCTTextPlainUTF8)
	}
}

func TestGetSource_UsesDiscoveryAdvertisedAcceptHeader(t *testing.T) {
	discoveryXML := discoveryXMLProgramsUTF8Only

	var capturedAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/source/main") {
			capturedAccept = r.Header.Get("Accept")
			w.Header().Set("ETag", `"etag-1"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("REPORT ZTEST."))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetSource(context.Background(), "/sap/bc/adt/programs/programs/ZTEST"); err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if capturedAccept != testCTTextPlainUTF8 {
		t.Errorf("Accept header: got %q, want %q", capturedAccept, testCTTextPlainUTF8)
	}
}

func TestGetIncludeSource_UsesDiscoveryAdvertisedAcceptHeader(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/oo/classes">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/includes/testclasses") {
			capturedAccept = r.Header.Get("Accept")
			w.Header().Set("ETag", `"etag-inc"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("CLASS ltcl_test DEFINITION."))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetIncludeSource(context.Background(), "/sap/bc/adt/oo/classes/ZCL_TEST", "testclasses"); err != nil {
		t.Fatalf("GetIncludeSource: %v", err)
	}
	if capturedAccept != testCTTextPlainUTF8 {
		t.Errorf("Accept: got %q, want %q", capturedAccept, testCTTextPlainUTF8)
	}
}

func TestSetSource_UsesDiscoveryAdvertisedContentType(t *testing.T) {
	// Discovery advertises ONLY testCTTextPlain (no charset). This differs
	// from the pre-refactor hardcoded testCTTextPlainUTF8, so a
	// captured Content-Type of testCTTextPlain proves discovery was
	// actually consulted.
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedContentType, capturedAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/source/main") {
			capturedContentType = r.Header.Get("Content-Type")
			capturedAccept = r.Header.Get("Accept")
			w.Header().Set("ETag", `"etag-new"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SetSource(context.Background(),
		"/sap/bc/adt/programs/programs/ZTEST",
		"REPORT ZTEST.",
		"lock1", "", `"etag-old"`)
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	// sourceContentType prefers testCTTextPlainUTF8 but discovery
	// only advertises testCTTextPlain → should return testCTTextPlain.
	want := testCTTextPlain
	if capturedContentType != want {
		t.Errorf("Content-Type: got %q, want %q", capturedContentType, want)
	}
	if capturedAccept != want {
		t.Errorf("Accept: got %q, want %q", capturedAccept, want)
	}
}

func TestSetIncludeSource_UsesDiscoveryAdvertisedContentType(t *testing.T) {
	// Discovery advertises only testCTTextPlain (no charset). Since the
	// pre-refactor hardcoded Content-Type was testCTTextPlainUTF8,
	// a captured Content-Type of plain testCTTextPlain proves discovery was
	// actually consulted.
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/oo/classes">
      <app:accept>text/plain</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/includes/testclasses") {
			capturedCT = r.Header.Get("Content-Type")
			w.Header().Set("ETag", `"etag-new"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SetIncludeSource(context.Background(),
		"/sap/bc/adt/oo/classes/ZCL_TEST", "testclasses",
		"CLASS ltcl_test DEFINITION.",
		"lock1", "", `"etag-old"`)
	if err != nil {
		t.Fatalf("SetIncludeSource: %v", err)
	}
	want := testCTTextPlain
	if capturedCT != want {
		t.Errorf("Content-Type: got %q, want %q", capturedCT, want)
	}
}

// assertSetSourceRetriesAndSucceeds drives SetSource against a stub whose
// write endpoint rejects header-delivered lock handle with the given
// status/body on the first attempt, then succeeds via query-param delivery
// on the retry. Asserts the retry actually fired (2+ attempts, lockHandle
// seen in the query string) and the returned ETag matches. Shared by the
// 423/403 (#443) and 400 (#494) retry-on-header-failure tests, which are
// otherwise identical in shape and differ only in the failure response.
func assertSetSourceRetriesAndSucceeds(t *testing.T, objectURI, source string, failStatus int, failBody, wantEtag string) {
	t.Helper()
	var attempts int
	var sawQueryHandle bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		attempts++
		if r.URL.Query().Get("lockHandle") != "" {
			sawQueryHandle = true
			w.Header().Set("ETag", wantEtag)
			w.WriteHeader(http.StatusOK)
			return
		}
		// Header-delivery attempt → the failure under test.
		w.WriteHeader(failStatus)
		_, _ = w.Write([]byte(failBody))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	etag, err := client.SetSource(context.Background(), objectURI, source, "LOCKH1", "TR1", `"etag-old"`)
	if err != nil {
		t.Fatalf("SetSource should have retried with query delivery and succeeded: %v", err)
	}
	if attempts < 2 {
		t.Errorf("expected a query-delivery retry (2 write attempts), got %d", attempts)
	}
	if !sawQueryHandle {
		t.Error("retry did not deliver the lock handle as a ?lockHandle= query parameter")
	}
	if etag != wantEtag {
		t.Errorf("returned ETag: got %q, want %q", etag, wantEtag)
	}
}

// #443: OO classes/interfaces reject header lock delivery with 403
// ExceptionResourceNoAccess ("currently editing"); the write must retry with
// ?lockHandle= query delivery. Verified live on S/4. Classes no longer take
// the header attempt at all (#281), so an interface carries this test.
func TestSetSource_RetriesQueryDeliveryOnCurrentlyEditing(t *testing.T) {
	assertSetSourceRetriesAndSucceeds(t,
		"/sap/bc/adt/oo/interfaces/zif_oo_retry",
		"INTERFACE zif_oo_retry PUBLIC.\nENDINTERFACE.",
		http.StatusForbidden,
		`<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionResourceNoAccess"/><message lang="EN">User X is currently editing ZIF_OO_RETRY</message></exc:exception>`,
		`"oo-new"`,
	)
}

// sawQueryRetryAfter403 drives SetSource against a stub whose write endpoint
// always returns 403 (body403 controls whether it's a typed exception or bare),
// and reports whether any request delivered the lock handle via ?lockHandle= —
// i.e. whether the query-delivery retry fired. It asserts the 403 surfaces.
// (adtler's doMutate may do its own CSRF-refresh retry, so we assert on delivery
// mode, not attempt count.)
func sawQueryRetryAfter403(t *testing.T, ooURI, lockHandle, body403 string) bool {
	t.Helper()
	var sawQueryHandle bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Query().Get("lockHandle") != "" {
			sawQueryHandle = true
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body403))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)
	if _, err := client.SetSource(context.Background(), ooURI, "CLASS x.", lockHandle, "TR1", `"e"`); err == nil {
		t.Fatal("expected the 403 to surface")
	}
	return sawQueryHandle
}

// A 403 "currently editing" without a lock handle must NOT trigger the retry
// (nothing to re-deliver) — the error surfaces so a genuine denial isn't masked.
func TestSetSource_NoRetryOn403WithoutLockHandle(t *testing.T) {
	body := `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionResourceNoAccess"/><message lang="EN">no access</message></exc:exception>`
	if sawQueryRetryAfter403(t, "/sap/bc/adt/oo/classes/zcl_oo_noretry", "", body) {
		t.Error("query-delivery retry fired without a lock handle — must not happen")
	}
}

// A bare 403 with no typed exception (empty ADTError.Type) must NOT trigger the
// query-delivery retry even when a lock handle is held — 403 is overloaded, and
// on R/3 a query retry would mask the real error as a 423. Guards the decision
// to match isCurrentlyEditing on Type only (#443 review).
func TestSetSource_NoRetryOnBare403(t *testing.T) {
	if sawQueryRetryAfter403(t, "/sap/bc/adt/oo/interfaces/zif_oo_bare403", "LOCKH1", "Forbidden") {
		t.Error("bare 403 (empty Type) wrongly triggered the query-delivery retry")
	}
}

const (
	classURI281 = "/sap/bc/adt/oo/classes/zcl_281"
	request281  = "AAAK900281"
	lockH281    = "LOCKH1"
)

// classWrite281 is one PUT the #281 stub saw: how the lock handle came and
// what corrNr and If-Match it carried.
type classWrite281 struct {
	query, header   bool
	corrNr, ifMatch string
}

// driveClassWrite281 runs SetSource for a class against a stub that answers
// each PUT with respond(write) and returns the PUTs in order.
func driveClassWrite281(t *testing.T, transport string, respond func(classWrite281) (int, string)) ([]classWrite281, string, error) {
	t.Helper()
	var writes []classWrite281
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPut || r.URL.Path != classURI281+"/source/main" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cw := classWrite281{
			query:   r.URL.Query().Get("lockHandle") == lockH281,
			header:  r.Header.Get("X-SAP-Lock-Handle") == lockH281,
			corrNr:  r.URL.Query().Get("corrNr"),
			ifMatch: r.Header.Get("If-Match"),
		}
		writes = append(writes, cw)
		status, body := respond(cw)
		if status == http.StatusOK {
			w.Header().Set("ETag", `"new"`)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	etag, err := client.SetSource(context.Background(), classURI281, "CLASS zcl_281 DEFINITION PUBLIC.\nENDCLASS.", lockH281, transport, `"old"`)
	return writes, etag, err
}

const (
	notValid281  = `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionResourceNoAccess"/><message lang="EN">Transport request AAAK900281 is not valid for object ZCL_281!</message></exc:exception>`
	notLocked281 = `<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionResourceInvalidLockHandle"/><message lang="EN">Resource CLASS ZCL_281 is not locked (invalid lock handle: LOCKH1)</message></exc:exception>`
)

// #281: a class write into a request delivers the lock handle as
// ?lockHandle= first, keeping corrNr and If-Match. On ECC the header form
// fails with 403 "Transport request … is not valid for object" whenever the
// request does not hold the class yet, and that failure also ends the lock,
// so a later query retry answers 423 instead (#280). The stub refuses any
// header-delivered handle with that 403, so the old order fails here.
func TestSetSource_ClassIntoRequestDeliversLockHandleAsQueryFirst(t *testing.T) {
	writes, etag, err := driveClassWrite281(t, request281, func(cw classWrite281) (int, string) {
		if !cw.query {
			return http.StatusForbidden, notValid281
		}
		return http.StatusOK, ""
	})
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	if len(writes) != 1 {
		t.Fatalf("writes: got %d, want 1 (no header attempt before the query delivery)", len(writes))
	}
	w := writes[0]
	if !w.query || w.header {
		t.Errorf("delivery: query=%v header=%v, want query only", w.query, w.header)
	}
	if w.corrNr != request281 {
		t.Errorf("corrNr: got %q, want %q", w.corrNr, request281)
	}
	if w.ifMatch != `"old"` {
		t.Errorf("If-Match: got %q, want %q", w.ifMatch, `"old"`)
	}
	if etag != `"new"` {
		t.Errorf("ETag: got %q, want %q", etag, `"new"`)
	}
}

// #281: a 423 to the query form means the lock is gone (on ECC a GET between
// LockObject and SetSource ends it). The header form still writes into a
// request that already holds the class, as it did before #281, so it is
// tried next and its result returned.
func TestSetSource_ClassIntoRequestFallsBackToHeaderOn423(t *testing.T) {
	writes, etag, err := driveClassWrite281(t, request281, func(cw classWrite281) (int, string) {
		if cw.query {
			return http.StatusLocked, notLocked281
		}
		return http.StatusOK, ""
	})
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	if len(writes) != 2 || !writes[0].query || !writes[1].header || writes[1].query {
		t.Fatalf("writes: got %+v, want query then header", writes)
	}
	if writes[1].corrNr != request281 {
		t.Errorf("header write corrNr: got %q, want %q", writes[1].corrNr, request281)
	}
	if etag != `"new"` {
		t.Errorf("ETag: got %q, want %q", etag, `"new"`)
	}
}

// #281: when the header fallback fails too, its error is the one returned,
// and the query form is not tried again. (A 403 also makes doMutate repeat
// the same write once with a fresh CSRF token, so writes are checked by
// delivery, not counted.)
func TestSetSource_ClassIntoRequestReturnsHeaderErrorAfter423(t *testing.T) {
	writes, _, err := driveClassWrite281(t, request281, func(cw classWrite281) (int, string) {
		if cw.query {
			return http.StatusLocked, notLocked281
		}
		return http.StatusForbidden, notValid281
	})
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.StatusCode != http.StatusForbidden {
		t.Fatalf("err: got %v, want the header write's 403", err)
	}
	if len(writes) < 2 || !writes[0].query {
		t.Fatalf("writes: got %+v, want query first", writes)
	}
	for i, w := range writes[1:] {
		if !w.header || w.query {
			t.Errorf("write %d after the 423: got %+v, want header delivery only", i+1, w)
		}
	}
}

// #281: other query-form failures are returned as they are; the header form
// is only a fallback for a lock that is gone.
func TestSetSource_ClassIntoRequestNoHeaderFallbackOnOtherErrors(t *testing.T) {
	writes, _, err := driveClassWrite281(t, request281, func(classWrite281) (int, string) {
		return http.StatusForbidden, notValid281
	})
	if err == nil {
		t.Fatal("expected the 403 to surface")
	}
	for i, w := range writes {
		if w.header {
			t.Errorf("write %d used header delivery: %+v", i, w)
		}
	}
}

// #281: without a corrNr a class write keeps the header-first order. On ECC
// the header form writes even after a GET between LockObject and SetSource
// ended the lock, which the query form would refuse with 423.
func TestSetSource_ClassWithoutRequestKeepsHeaderFirst(t *testing.T) {
	writes, _, err := driveClassWrite281(t, "", func(classWrite281) (int, string) {
		return http.StatusOK, ""
	})
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	if len(writes) != 1 || !writes[0].header || writes[0].query {
		t.Fatalf("writes: got %+v, want one header-delivered write", writes)
	}
}

// aibap.mcp#494: on the reporter's ECC (NetWeaver 7.5x) system, header
// delivery of the lock handle is rejected outright with 400
// ExceptionParameterNotFound ("Parameter lockHandle could not be found") —
// not the 423/403 signals the retry gate already recognises — so the write
// never falls back to query-param delivery and every source write fails.
// Live-probed on both systems: neither reproduces this 400, confirming it's a system/SP-specific ADT handler
// difference outside our landscape, not something our existing fixtures
// cover. Reproduced here as a synthetic fixture matching the reporter's
// exact SAP response body.
func TestSetSource_RetriesQueryDeliveryOnLockHandleParameterNotFound(t *testing.T) {
	assertSetSourceRetriesAndSucceeds(t,
		"/sap/bc/adt/programs/programs/z_lockhandle_494",
		"REPORT z_lockhandle_494.",
		http.StatusBadRequest,
		`<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionParameterNotFound"/><message lang="EN">Parameter lockHandle could not be found</message></exc:exception>`,
		`"prog-new"`,
	)
}

// The parameter-name match is case-insensitive: SAP's own casing for this
// message isn't guaranteed stable across releases/locales.
func TestSetSource_RetriesQueryDeliveryOnLockHandleParameterNotFound_CaseInsensitive(t *testing.T) {
	const progURI = "/sap/bc/adt/programs/programs/z_lockhandle_494_case"
	var sawQueryHandle bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Query().Get("lockHandle") != "" {
			sawQueryHandle = true
			w.Header().Set("ETag", `"prog-new"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionParameterNotFound"/><message lang="EN">Parameter LOCKHANDLE could not be found</message></exc:exception>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.SetSource(context.Background(), progURI, "REPORT z.", "LOCKH1", "TR1", `"etag-old"`); err != nil {
		t.Fatalf("SetSource should have retried with query delivery and succeeded: %v", err)
	}
	if !sawQueryHandle {
		t.Error("retry did not fire for a differently-cased parameter name")
	}
}

// A 400 ExceptionParameterNotFound naming a DIFFERENT parameter (the classic
// "missing transport" case, #378 finding 1) must NOT trigger the
// lock-handle-delivery retry — retrying with query-param delivery would not
// fix a missing corrNr and would mask the real error behind a misleading
// second failure.
func TestSetSource_NoRetryOnParameterNotFoundForOtherParam(t *testing.T) {
	const progURI = "/sap/bc/adt/programs/programs/z_lockhandle_494_other"
	var sawQueryHandle bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Query().Get("lockHandle") != "" {
			sawQueryHandle = true
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="ExceptionParameterNotFound"/><message lang="EN">Parameter corrNr could not be found</message></exc:exception>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.SetSource(context.Background(), progURI, "REPORT z.", "LOCKH1", "", `"e"`); err == nil {
		t.Fatal("expected the 400 to surface")
	}
	if sawQueryHandle {
		t.Error("query-delivery retry fired for an unrelated missing parameter — must not happen")
	}
}
