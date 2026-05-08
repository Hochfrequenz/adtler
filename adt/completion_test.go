package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestGetCompletions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/sap/bc/adt/abapsource/codecompletion/proposal" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// SAP expects line/column embedded in the URI fragment, not as
		// separate top-level query params.
		gotURI := r.URL.Query().Get("uri")
		wantURI := "/sap/bc/adt/programs/programs/ZTEST/source/main#start=5,10"
		if gotURI != wantURI {
			t.Errorf("uri: got %q, want %q", gotURI, wantURI)
		}
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>` +
			`<asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml">` +
			`<asx:values><DATA>` +
			`<SCC_COMPLETION><KIND>52</KIND><IDENTIFIER>METHOD</IDENTIFIER></SCC_COMPLETION>` +
			`<SCC_COMPLETION><KIND>52</KIND><IDENTIFIER>MESSAGE</IDENTIFIER></SCC_COMPLETION>` +
			`</DATA></asx:values></asx:abap>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	items, err := client.GetCompletions(context.Background(), "/sap/bc/adt/programs/programs/ZTEST", "REPORT ZTEST.", 5, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Text != "METHOD" {
		t.Errorf("item[0].Text: got %q", items[0].Text)
	}
}
