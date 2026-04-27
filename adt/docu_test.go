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

func TestGetABAPDoc_SendsQueryParam(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotMethod = r.Method
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
	if gotMethod != http.MethodGet {
		t.Errorf("method: got %q, want GET", gotMethod)
	}
	if !strings.Contains(gotPath, "/sap/public/bc/abap/docu") {
		t.Errorf("path should use the public docu servlet, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "query=DATA") {
		t.Errorf("path should contain query=DATA, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "format=eclipse") {
		t.Errorf("path should contain format=eclipse, got %q", gotPath)
	}
	if !strings.Contains(gotPath, "sap-client=100") {
		t.Errorf("path should contain sap-client=100, got %q", gotPath)
	}
	if strings.Contains(gotPath, "object=") {
		t.Errorf("path should NOT contain object= (old broken param), got %q", gotPath)
	}
	if strings.Contains(gotPath, "keyword=") {
		t.Errorf("path should NOT contain keyword= (old broken param), got %q", gotPath)
	}
	if strings.Contains(result, "<h1>") {
		t.Errorf("result should be HTML-stripped, got %q", result)
	}
	if !strings.Contains(result, "DATA Statement") {
		t.Errorf("result should contain doc text, got %q", result)
	}
}

func TestGetABAPDoc_403ReturnsClearError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`<html><body>Service cannot be reached</body></html>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.GetABAPDoc(context.Background(), "DATA")
	if err == nil {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(err.Error(), "SICF") {
		t.Errorf("error should mention SICF activation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Errorf("error should say 'not available', got: %v", err)
	}
}
