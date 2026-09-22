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

// CreatePackage sent only a Content-Type and no Accept header, so S/4 rejected
// the POST with 400 ExceptionResourceBadRequest ("Accept header missing") and
// package creation could never succeed there. Measured on SAP S/4HANA
// on-premise (SAP_BASIS 816, S4CORE 109) on 2026-09-21: with the Accept header
// added, the same call creates the package.
//
// This is the same class of defect as the discovery GET in
// TestDiscovery_ServerRequiresAcceptHeader_StillPopulatesCache — a request
// built without Accept against a server that demands one.
func TestCreatePackage_SendsAcceptHeader(t *testing.T) {
	const want = "application/vnd.sap.adt.packages.v2+xml"

	var gotAccept, gotContentType, gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == logoffPath {
			w.WriteHeader(http.StatusOK)
			return
		}
		gotPath, gotMethod = r.URL.Path, r.Method
		gotAccept = r.Header.Get("Accept")
		gotContentType = r.Header.Get("Content-Type")
		// Reproduce what a real S/4 system does when Accept is absent.
		if gotAccept == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionResourceBadRequest"/><message lang="EN">Request could not be understood by the server due to malformed syntax: Accept header missing</message></exc:exception>`))
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.CreatePackage(context.Background(), "$ZEXAMPLE", "Example package", "DEVELOPER", "LOCAL", "", "")
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if gotAccept != want {
		t.Errorf("Accept header: got %q, want %q", gotAccept, want)
	}
	if gotContentType != want {
		t.Errorf("Content-Type header: got %q, want %q", gotContentType, want)
	}
	if gotMethod != http.MethodPost || gotPath != "/sap/bc/adt/packages" {
		t.Errorf("request: got %s %s, want POST /sap/bc/adt/packages", gotMethod, gotPath)
	}
}

// A server rejection must surface as an error rather than being reported as a
// created package.
//
// This is not the Accept-header guard, despite sitting beside it: the fake
// server here rejects unconditionally, so the test passes with or without the
// header. It covers error propagation only — TestCreatePackage_SendsAcceptHeader
// above is what fails when the header is dropped.
func TestCreatePackage_ServerRejectionSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == logoffPath {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionInvalidData"/><message lang="EN">Check of condition failed</message></exc:exception>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.CreatePackage(context.Background(), "$ZEXAMPLE", "Example package", "", "LOCAL", "", "")
	if err == nil {
		t.Fatal("expected the server rejection to surface as an error")
	}
	if !strings.Contains(err.Error(), "Check of condition failed") {
		t.Errorf("error should carry the SAP message, got: %v", err)
	}
}
