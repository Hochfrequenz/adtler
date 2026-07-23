package adt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// classrunBase is the classrun endpoint prefix. The class name is lower-cased
// and appended to it.
const classrunBase = "/sap/bc/adt/oo/classrun/"

func TestRunClass_Success(t *testing.T) {
	var gotMethod, gotPath, gotAccept, gotCSRF string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		gotCSRF = r.Header.Get("X-CSRF-Token")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Hello from classrun\n"))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.RunClass(context.Background(), "ZCL_ADT_MCP_CLASSRUN_TST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method: got %q, want POST", gotMethod)
	}
	if gotPath != classrunBase+"zcl_adt_mcp_classrun_tst" {
		t.Errorf("path: got %q, want %q", gotPath, classrunBase+"zcl_adt_mcp_classrun_tst")
	}
	if gotAccept != "text/plain" {
		t.Errorf("Accept: got %q, want text/plain", gotAccept)
	}
	if gotCSRF != "token" {
		t.Errorf("X-CSRF-Token: got %q, want token", gotCSRF)
	}
	if len(gotBody) != 0 {
		t.Errorf("body: got %q, want empty", gotBody)
	}
	if result.ClassName != "ZCL_ADT_MCP_CLASSRUN_TST" {
		t.Errorf("ClassName: got %q", result.ClassName)
	}
	if result.ConsoleOutput != "Hello from classrun\n" {
		t.Errorf("ConsoleOutput: got %q", result.ConsoleOutput)
	}
}

// TestRunClass_UTF8 verifies that non-ASCII console output (Umlaute) survives
// the text/plain round-trip intact.
func TestRunClass_UTF8(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Grüße aus Köln — Größe: 42"))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.RunClass(context.Background(), "ZCL_ADT_MCP_CLASSRUN_TST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ConsoleOutput != "Grüße aus Köln — Größe: 42" {
		t.Errorf("ConsoleOutput: got %q", result.ConsoleOutput)
	}
}

// TestRunClass_Namespaced verifies that a namespaced class name is
// percent-encoded (the "//" -> %2f..%2f path) before the request is sent.
func TestRunClass_Namespaced(t *testing.T) {
	var gotEscapedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotEscapedPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.RunClass(context.Background(), "/NA2/CL_FOO")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := classrunBase + "%2fna2%2fcl_foo"
	if gotEscapedPath != want {
		t.Errorf("escaped path: got %q, want %q", gotEscapedPath, want)
	}
	// ClassName echoes the caller's input verbatim (not lower-cased).
	if result.ClassName != "/NA2/CL_FOO" {
		t.Errorf("ClassName: got %q, want /NA2/CL_FOO", result.ClassName)
	}
}

// TestRunClass_HTTPError verifies that a non-2xx response surfaces as an
// *adt.ADTError with the status code and body message preserved.
func TestRunClass_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Not authorized to run class"))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.RunClass(context.Background(), "ZCL_NOPE")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
	}
	if adtErr.StatusCode != http.StatusForbidden {
		t.Errorf("StatusCode: got %d, want 403", adtErr.StatusCode)
	}
	if !strings.Contains(adtErr.Message, "Not authorized to run class") {
		t.Errorf("Message: got %q, want it to contain the body text", adtErr.Message)
	}
}
