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

func TestCreateObjectProgram(t *testing.T) {
	var gotCreatePath, gotCreateMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		// The post-create Logout call (adtler#4 workaround) hits /sap/public/bc/icf/logoff.
		if r.URL.Path == logoffPath {
			w.WriteHeader(http.StatusOK)
			return
		}
		gotCreatePath = r.URL.Path
		gotCreateMethod = r.Method
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.CreateObject(context.Background(), "PROG", "ZTEST_NEW", "ZPACKAGE", "Test program", "DEVK900001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCreateMethod != http.MethodPost {
		t.Errorf("method: got %q", gotCreateMethod)
	}
	if gotCreatePath != "/sap/bc/adt/programs/programs" {
		t.Errorf("path: got %q", gotCreatePath)
	}
}

// TestCreateObject_LogsOutAfterSuccess regression-tests adtler#4:
// after a successful CreateObject, the client must call Logout to release
// the session-bound ESRDIRE enqueue that S/4 creates. Without this, the
// next LockObject/SetSource fails with 423 InvalidLockHandle.
func TestCreateObject_LogsOutAfterSuccess(t *testing.T) {
	logoffCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == logoffPath {
			logoffCalled = true
			w.WriteHeader(http.StatusOK)
			return
		}
		// CreateObject POST
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.CreateObject(context.Background(), "PROG", "ZTEST_LOGOUT", "$TMP", "test", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !logoffCalled {
		t.Error("Logout was NOT called after successful CreateObject — " +
			"the ESRDIRE enqueue workaround (adtler#4) is missing")
	}
}

func TestCreateObjectUnsupportedType(t *testing.T) {
	cfg := sapmcpconfig.SAPSystem{Host: "http://localhost", User: "U", Password: "P"}
	client := adt.NewClient(cfg)

	err := client.CreateObject(context.Background(), "TABL", "ZTABLE", "ZPACKAGE", "Table", "")
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
}

// TestCreateObject_DDICUnavailableOnR3 regression-tests adtler#16:
// R/3 ADT replies HTTP 415 ExceptionUnsupportedMediaType for DTEL create
// even though the URL path /sap/bc/adt/ddic/dataelements exists. The
// CreateObject guard must convert that into the same "DDIC unavailable on
// this system" hint that 404 already produces for TABL/DOMA, otherwise
// the user gets a confusing media-type error instead of the actionable
// "use SE11 on ECC" suggestion. Each row drives the same code path with
// a different status code so the parametrized assertion catches both.
func TestCreateObject_DDICUnavailableOnR3(t *testing.T) {
	cases := []struct {
		name       string
		statusCode int
		objType    string
	}{
		{"DTEL_415", http.StatusUnsupportedMediaType, "DTEL"},
		{"DOMA_415", http.StatusUnsupportedMediaType, "DOMA"},
		{"TABL_415", http.StatusUnsupportedMediaType, "TABL"},
		{"DDLS_415", http.StatusUnsupportedMediaType, "DDLS"},
		{"DTEL_404", http.StatusNotFound, "DTEL"},
		{"TABL_404", http.StatusNotFound, "TABL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == csrfEndpoint {
					w.Header().Set("X-CSRF-Token", "token")
					w.WriteHeader(http.StatusOK)
					return
				}
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
			client := adt.NewClient(cfg)

			err := client.CreateObject(context.Background(), tc.objType, "Z_DDIC_TEST", "$TMP", "test", "")
			if err == nil {
				t.Fatalf("expected error for %s on a system that returns %d", tc.objType, tc.statusCode)
			}
			if !strings.Contains(err.Error(), "not available on this SAP system") {
				t.Errorf("error should contain 'not available on this SAP system', got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.objType) {
				t.Errorf("error should mention object type %s, got: %v", tc.objType, err)
			}
		})
	}
}

func TestCreatePackage(t *testing.T) {
	var gotPath, gotMethod, gotContentType, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.CreatePackage(context.Background(), "Z_MY_PKG", "My Package", "TESTUSER", "HOME", "ZS4U", "DEVK900001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != "/sap/bc/adt/packages" {
		t.Errorf("path: got %q", gotPath)
	}
	if gotContentType != "application/vnd.sap.adt.packages.v2+xml" {
		t.Errorf("content-type: got %q", gotContentType)
	}
	if !strings.Contains(gotBody, `adtcore:name="Z_MY_PKG"`) {
		t.Errorf("body missing package name: %s", gotBody)
	}
	if !strings.Contains(gotBody, `adtcore:responsible="TESTUSER"`) {
		t.Errorf("body missing responsible: %s", gotBody)
	}
	if !strings.Contains(gotBody, `pak:name="HOME"`) {
		t.Errorf("body missing softwareComponent: %s", gotBody)
	}
	if !strings.Contains(gotBody, `pak:name="ZS4U"`) {
		t.Errorf("body missing transportLayer: %s", gotBody)
	}
}

func TestCreatePackageWithoutTransport(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.CreatePackage(context.Background(), "z_tmp_pkg", "Temp", "testuser", "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("expected no query params for local package, got %q", gotQuery)
	}
}

func TestDeleteObject(t *testing.T) {
	var gotDeletePath, gotDeleteMethod, gotIfMatch, gotCorrNr string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet {
			// ETag fetch for optimistic locking
			w.Header().Set("ETag", "etag-12345")
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="ZTEST" adtcore:type="PROG/P"/>`))
			return
		}
		gotDeletePath = r.URL.Path
		gotDeleteMethod = r.Method
		gotIfMatch = r.Header.Get("If-Match")
		gotCorrNr = r.URL.Query().Get("corrNr")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.DeleteObject(context.Background(), "/sap/bc/adt/programs/programs/ZTEST", "", "DEVK900001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotDeleteMethod != http.MethodDelete {
		t.Errorf("method: got %q", gotDeleteMethod)
	}
	if gotDeletePath != "/sap/bc/adt/programs/programs/ZTEST" {
		t.Errorf("path: got %q", gotDeletePath)
	}
	if gotIfMatch != "etag-12345" {
		t.Errorf("If-Match: got %q, want %q", gotIfMatch, "etag-12345")
	}
	if gotCorrNr != "DEVK900001" {
		t.Errorf("corrNr: got %q, want %q", gotCorrNr, "DEVK900001")
	}
}

// TestDeleteObject_ETagFetchHTTPError regression-tests adtler#19:
// If the ETag-fetch GET returns a 4xx (e.g. S/4 returning 400
// ExceptionResourceWrongData for a CLAS bare-URI GET), the old code
// proceeded past the response, found no ETag header, and surfaced
// the cryptic "no ETag returned" message instead of the real SAP
// error. The fix adds a checkResponse() call between doRead() and
// the ETag header read, so the SAP error message reaches the caller.
func TestDeleteObject_ETagFetchHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet {
			// Mimic S/4's HTTP 400 ExceptionResourceWrongData reply
			// for a bare CLAS URI GET (cluster with adtler#9).
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceWrongData"/>
  <message lang="EN">Resource ZCL_TEST: wrong input data for processing</message>
</exc:exception>`))
			return
		}
		// DELETE should never be reached because the ETag fetch fails.
		t.Errorf("unexpected DELETE call after ETag fetch failure")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.DeleteObject(context.Background(), "/sap/bc/adt/oo/classes/zcl_test", "", "")
	if err == nil {
		t.Fatal("expected error from ETag fetch HTTP 400")
	}
	if strings.Contains(err.Error(), "no ETag returned") {
		t.Errorf("error should NOT be the cryptic 'no ETag returned' message; "+
			"the SAP error should be propagated instead. got: %v", err)
	}
	if !strings.Contains(err.Error(), "wrong input data for processing") {
		t.Errorf("error should contain the SAP message body, got: %v", err)
	}
}
