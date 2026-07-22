package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// Test-local constants for source-op content-type assertions. Mirror
// adt.contentTypeTextPlain / adt.contentTypeTextPlainUTF8 (unexported
// from the production package).
const (
	testCTTextPlain     = `text/plain`
	testCTTextPlainUTF8 = `text/plain; charset=utf-8`
)

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

	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

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
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
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
