package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestSearchObjects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != searchEndpoint {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		if q.Get("operation") != "quickSearch" {
			t.Errorf("operation: got %q", q.Get("operation"))
		}
		if q.Get("query") != "ZTEST*" {
			t.Errorf("query: got %q", q.Get("query"))
		}
		if q.Get("maxResults") != "10" {
			t.Errorf("maxResults: got %q", q.Get("maxResults"))
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:objectReference adtcore:uri="/sap/bc/adt/programs/programs/ZTEST_REPORT" adtcore:type="PROG/P" adtcore:name="ZTEST_REPORT" adtcore:description="Test Report" adtcore:packageName="ZPACKAGE"/>
</adtcore:objectReferences>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	results, err := client.SearchObjects(context.Background(), "ZTEST*", "", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "ZTEST_REPORT" {
		t.Errorf("name: got %q", results[0].Name)
	}
	if results[0].PackageName != "ZPACKAGE" {
		t.Errorf("package: got %q", results[0].PackageName)
	}
}

func TestSearchPackages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != searchEndpoint {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		if q.Get("operation") != "quickSearch" {
			t.Errorf("operation: got %q", q.Get("operation"))
		}
		if q.Get("query") != "ZPKG*" {
			t.Errorf("query: got %q", q.Get("query"))
		}
		// SearchPackages must constrain the search to the package object type.
		if q.Get("objectType") != adt.ObjectTypePackage {
			t.Errorf("objectType: got %q, want %q", q.Get("objectType"), adt.ObjectTypePackage)
		}
		if q.Get("maxResults") != "5" {
			t.Errorf("maxResults: got %q", q.Get("maxResults"))
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<adtcore:objectReferences xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:objectReference adtcore:uri="/sap/bc/adt/packages/ZPKG_TEST" adtcore:type="DEVC/K" adtcore:name="ZPKG_TEST" adtcore:description="Test Package"/>
</adtcore:objectReferences>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	results, err := client.SearchPackages(context.Background(), "ZPKG*", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "ZPKG_TEST" {
		t.Errorf("name: got %q", results[0].Name)
	}
	if results[0].Type != adt.ObjectTypePackage {
		t.Errorf("type: got %q, want %q", results[0].Type, adt.ObjectTypePackage)
	}
}

func TestWhereUsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/sap/bc/adt/repository/informationsystem/usageReferences" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Query().Get("uri") == "" {
			t.Error("expected uri parameter")
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0"?>
<usageReferences:usageReferenceResult xmlns:usageReferences="http://www.sap.com/adt/ris/usageReferences" xmlns:adtcore="http://www.sap.com/adt/core">
  <usageReferences:referencedObjects>
    <usageReferences:referencedObject uri="/sap/bc/adt/programs/programs/ZCALLER">
      <usageReferences:adtObject adtcore:name="ZCALLER" adtcore:type="PROG/P" adtcore:description="Caller">
        <adtcore:packageRef adtcore:name="ZPACKAGE"/>
      </usageReferences:adtObject>
    </usageReferences:referencedObject>
  </usageReferences:referencedObjects>
</usageReferences:usageReferenceResult>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	results, err := client.WhereUsed(context.Background(), "/sap/bc/adt/programs/programs/ZTEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 || results[0].Name != "ZCALLER" {
		t.Errorf("unexpected results: %+v", results)
	}
}
