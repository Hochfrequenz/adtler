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

// TestGetABAPDoc_SendsCorrectPath regression-tests adtler#18:
// the old code POSTed to /sap/bc/adt/docu/abap/langu with keyword=<KW> —
// both R/3 and S/4 ignored that parameter and returned the homepage. The fix
// GETs /sap/public/bc/abap/docu with object=ABEN<KW>&format=eclipse, which
// is the path SAP's own ABAP help UI uses internally.
func TestGetABAPDoc_SendsCorrectPath(t *testing.T) {
	var gotPath, gotMethod, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotMethod = r.Method
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body><h1>DATA Statement</h1><p>Declares variables.</p></body></html>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.GetABAPDoc(context.Background(), "DATA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the request shape.
	if gotMethod != http.MethodGet {
		t.Errorf("method: got %q, want GET", gotMethod)
	}
	if !strings.Contains(gotPath, "/sap/public/bc/abap/docu") {
		t.Errorf("path should use the public docu servlet, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "object=ABENDATA") {
		t.Errorf("path should contain object=ABENDATA, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "format=eclipse") {
		t.Errorf("path should contain format=eclipse, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "sap-client=100") {
		t.Errorf("path should contain sap-client=100, got %q", gotPath)
	}
	if gotAccept != "text/html" {
		t.Errorf("Accept: got %q, want text/html", gotAccept)
	}

	// Verify the response is HTML-stripped.
	if strings.Contains(result, "<h1>") || strings.Contains(result, "<p>") {
		t.Errorf("result should be HTML-stripped, got %q", result)
	}
	if !strings.Contains(result, "DATA Statement") {
		t.Errorf("result should contain the doc text, got %q", result)
	}
}

// TestGetABAPDoc_EmptyKeyword verifies the homepage path (no object param).
func TestGetABAPDoc_EmptyKeyword(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<html><body>Homepage</body></html>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.GetABAPDoc(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(gotPath, "object=") {
		t.Errorf("empty keyword should not send object= param, got path %q", gotPath)
	}
}
