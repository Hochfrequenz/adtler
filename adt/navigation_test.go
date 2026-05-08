package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestNavigateToDefinition(t *testing.T) {
	const wantSource = "REPORT ztest.\nDATA cls TYPE REF TO zcl_target."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/sap/bc/adt/navigation/target" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != wantSource {
			t.Errorf("body: got %q, want %q", string(body), wantSource)
		}
		if ct := r.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
			t.Errorf("Content-Type: got %q", ct)
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<objectReference xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:uri="/sap/bc/adt/oo/classes/zcl_target/source/main#start=10,5"
  adtcore:type="CLAS/OC"
  adtcore:name="ZCL_TARGET"/>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	uri, err := client.NavigateToDefinition(context.Background(),
		"/sap/bc/adt/programs/programs/ztest/source/main#start=5,8", wantSource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "/sap/bc/adt/oo/classes/zcl_target/source/main#start=10,5"
	if uri != want {
		t.Errorf("got %q, want %q", uri, want)
	}
}
