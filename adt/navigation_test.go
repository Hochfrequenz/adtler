package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// TestNavigateToDefinition_ReturnsTargetRef regression-tests adtler#8:
// when the endpoint returns a child <objectReference> with a different URI
// than the root, the function should return the child URI (the actual
// navigation target), not the root URI (the echo of the input position).
func TestNavigateToDefinition_ReturnsTargetRef(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<adtcore:objectReference xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:uri="/sap/bc/adt/programs/programs/ztest/source/main#start=15,5"
  adtcore:type="PROG/P" adtcore:name="ZTEST">
  <adtcore:objectReference
    adtcore:uri="/sap/bc/adt/oo/classes/cl_abap_unit_assert"
    adtcore:type="CLAS/OC" adtcore:name="CL_ABAP_UNIT_ASSERT"/>
</adtcore:objectReference>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	target, err := client.NavigateToDefinition(context.Background(),
		"/sap/bc/adt/programs/programs/ztest/source/main#start=15,5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "/sap/bc/adt/oo/classes/cl_abap_unit_assert" {
		t.Errorf("target: got %q, want the child objectReference URI", target)
	}
}

// TestNavigateToDefinition_EchoWhenNoTarget verifies that when SAP returns
// no child objectReference (cursor on a non-navigable token), the function
// returns the root URI (canonical echo of the input).
func TestNavigateToDefinition_EchoWhenNoTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<adtcore:objectReference xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:uri="/sap/bc/adt/programs/programs/ztest/source/main#start=1,1"
  adtcore:type="PROG/P" adtcore:name="ZTEST"/>
`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	target, err := client.NavigateToDefinition(context.Background(),
		"/sap/bc/adt/programs/programs/ztest/source/main#start=1,1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return the echo since there's no child target.
	if target != "/sap/bc/adt/programs/programs/ztest/source/main#start=1,1" {
		t.Errorf("target: got %q, expected the root echo", target)
	}
}

// TestNavigateToDefinition_LinkFallback verifies that when the target is in
// a <link> element instead of an objectReference child, the function picks
// it up as a fallback.
func TestNavigateToDefinition_LinkFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<adtcore:objectReference xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:uri="/sap/bc/adt/programs/programs/ztest/source/main#start=15,5">
  <atom:link xmlns:atom="http://www.w3.org/2005/Atom"
    href="/sap/bc/adt/oo/classes/cl_abap_unit_assert"
    rel="http://www.sap.com/adt/relations/navigation"/>
</adtcore:objectReference>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	target, err := client.NavigateToDefinition(context.Background(),
		"/sap/bc/adt/programs/programs/ztest/source/main#start=15,5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if target != "/sap/bc/adt/oo/classes/cl_abap_unit_assert" {
		t.Errorf("target: got %q, want the link href", target)
	}
}
