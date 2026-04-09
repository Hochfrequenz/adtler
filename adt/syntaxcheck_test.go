package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestSyntaxCheckWithErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != checkrunsPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		ct := r.Header.Get("Content-Type")
		if ct != "application/vnd.sap.adt.checkobjects+xml" {
			t.Errorf("Content-Type: got %q", ct)
		}
		accept := r.Header.Get("Accept")
		if accept != "application/vnd.sap.adt.checkmessages+xml" {
			t.Errorf("Accept: got %q", accept)
		}
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)
		if !strings.Contains(bodyStr, "checkObjectList") {
			t.Errorf("body missing checkObjectList: %s", bodyStr)
		}
		if !strings.Contains(bodyStr, "/sap/bc/adt/programs/programs/ZTEST") {
			t.Errorf("body missing object URI: %s", bodyStr)
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <chkrun:checkReport chkrun:reporter="abapCheckRun"
    chkrun:triggeringUri="/sap/bc/adt/programs/programs/ZTEST"
    chkrun:status="processed" chkrun:statusText="Syntax check performed">
    <chkrun:checkMessageList>
      <chkrun:checkMessage chkrun:uri="/sap/bc/adt/programs/programs/ZTEST/source/main#start=42,5"
        chkrun:type="E" chkrun:shortText="Field &quot;FOO&quot; is unknown."/>
    </chkrun:checkMessageList>
  </chkrun:checkReport>
</chkrun:checkRunReports>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	msgs, err := client.SyntaxCheck(context.Background(), "/sap/bc/adt/programs/programs/ZTEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Type != "E" {
		t.Errorf("type: got %q", msgs[0].Type)
	}
	if msgs[0].Text != `Field "FOO" is unknown.` {
		t.Errorf("text: got %q", msgs[0].Text)
	}
	if msgs[0].Line != 42 {
		t.Errorf("line: got %d", msgs[0].Line)
	}
	if msgs[0].Column != 5 {
		t.Errorf("column: got %d", msgs[0].Column)
	}
}

func TestSyntaxCheckClean(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <chkrun:checkReport chkrun:reporter="abapCheckRun"
    chkrun:triggeringUri="/sap/bc/adt/programs/programs/ZTEST"
    chkrun:status="processed" chkrun:statusText="Object ZTEST has been checked"/>
</chkrun:checkRunReports>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	msgs, err := client.SyntaxCheck(context.Background(), "/sap/bc/adt/programs/programs/ZTEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for clean check, got %d", len(msgs))
	}
}

func TestBatchSyntaxCheckChunking(t *testing.T) {
	// Track how many requests hit the server.
	requestCount := 0
	var requestBodies []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		requestCount++
		body, _ := io.ReadAll(r.Body)
		requestBodies = append(requestBodies, string(body))

		w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
</chkrun:checkRunReports>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	// 12 URIs should produce 2 requests (chunk size is 10).
	uris := make([]string, 12)
	for i := range uris {
		uris[i] = "/sap/bc/adt/programs/programs/ZPROG" + strings.Repeat("X", i)
	}

	results := client.BatchSyntaxCheck(context.Background(), uris)

	if len(results) != 12 {
		t.Fatalf("expected 12 results, got %d", len(results))
	}
	if requestCount != 2 {
		t.Errorf("expected 2 HTTP requests (chunk of 10 + chunk of 2), got %d", requestCount)
	}
	// Verify each result is correlated to the correct URI.
	for i, r := range results {
		if r.ObjectURI != uris[i] {
			t.Errorf("result[%d]: got URI %q, want %q", i, r.ObjectURI, uris[i])
		}
	}
}

func TestBatchSyntaxCheckHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	uris := []string{"/sap/bc/adt/programs/programs/ZA", "/sap/bc/adt/programs/programs/ZB"}
	results := client.BatchSyntaxCheck(context.Background(), uris)

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for i, r := range results {
		if r.Error == "" {
			t.Errorf("result[%d]: expected error to be populated", i)
		}
		if r.ObjectURI != uris[i] {
			t.Errorf("result[%d]: got URI %q, want %q", i, r.ObjectURI, uris[i])
		}
	}
}

// TestSyntaxCheck_FalsePositiveRetriesToActive regression-tests adtler#11:
// when the inactive-version check returns the "REPORT/PROGRAM missing" false
// positive (the pattern produced when there is no inactive version), the fix
// retries with version="active". The test mocks a server that:
//   - returns the false-positive on checkruns with version="inactive"
//   - returns clean results on checkruns with version="active"
//   - responds to GetObjectInfo with a valid object (so the existence check passes)
func TestSyntaxCheck_FalsePositiveRetriesToActive(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		// GetObjectInfo call (existence check).
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sap/bc/adt/programs/programs/") {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="ZEXISTING" adtcore:type="PROG/P"/>`))
			return
		}
		// Checkruns POST.
		if r.URL.Path == checkrunsPath {
			callCount++
			body, _ := io.ReadAll(r.Body)
			bodyStr := string(body)

			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			w.WriteHeader(http.StatusOK)

			if strings.Contains(bodyStr, `version="inactive"`) {
				// First call: return the false-positive pattern.
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <chkrun:checkReport chkrun:reporter="abapCheckRun"
    chkrun:triggeringUri="/sap/bc/adt/programs/programs/ZEXISTING"
    chkrun:status="processed">
    <chkrun:checkMessageList>
      <chkrun:checkMessage chkrun:uri="/sap/bc/adt/programs/programs/ZEXISTING/source/main#start=1,0"
        chkrun:type="E" chkrun:shortText="The REPORT/PROGRAM statement is missing, or the program type is INCLUDE."/>
    </chkrun:checkMessageList>
  </chkrun:checkReport>
</chkrun:checkRunReports>`))
				return
			}
			if strings.Contains(bodyStr, `version="active"`) {
				// Retry call: return clean results (active version is fine).
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <chkrun:checkReport chkrun:reporter="abapCheckRun"
    chkrun:triggeringUri="/sap/bc/adt/programs/programs/ZEXISTING"
    chkrun:status="processed"/>
</chkrun:checkRunReports>`))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	msgs, err := client.SyntaxCheck(context.Background(), "/sap/bc/adt/programs/programs/ZEXISTING")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The active-version check returned clean — so we expect zero messages.
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages after active-version retry, got %d: %+v", len(msgs), msgs)
	}
	// The server should have been hit twice: once for inactive, once for active.
	if callCount != 2 {
		t.Errorf("expected 2 checkruns calls (inactive then active retry), got %d", callCount)
	}
}

// TestSyntaxCheck_NonExistentObjectReturnsError regression-tests adtler#11:
// when the false-positive pattern fires AND the object doesn't exist, the fix
// returns a clear "object does not exist" error rather than the misleading
// "REPORT statement missing" syntax error.
func TestSyntaxCheck_NonExistentObjectReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		// GetObjectInfo returns 404 — object doesn't exist.
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sap/bc/adt/programs/programs/") {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<exc:ExceptionText xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <message>Resource PROG ZNONEXISTENT does not exist.</message>
</exc:ExceptionText>`))
			return
		}
		// Checkruns: return the false-positive pattern.
		if r.URL.Path == checkrunsPath {
			w.Header().Set("Content-Type", "application/vnd.sap.adt.checkmessages+xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <chkrun:checkReport chkrun:reporter="abapCheckRun"
    chkrun:triggeringUri="/sap/bc/adt/programs/programs/ZNONEXISTENT"
    chkrun:status="processed">
    <chkrun:checkMessageList>
      <chkrun:checkMessage chkrun:uri="/sap/bc/adt/programs/programs/ZNONEXISTENT/source/main#start=1,0"
        chkrun:type="E" chkrun:shortText="The REPORT/PROGRAM statement is missing, or the program type is INCLUDE."/>
    </chkrun:checkMessageList>
  </chkrun:checkReport>
</chkrun:checkRunReports>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SyntaxCheck(context.Background(), "/sap/bc/adt/programs/programs/ZNONEXISTENT")
	if err == nil {
		t.Fatal("expected error for non-existent object, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error should mention 'does not exist', got: %v", err)
	}
	if strings.Contains(err.Error(), "REPORT") {
		t.Errorf("error should NOT be the misleading REPORT-missing message, got: %v", err)
	}
}
