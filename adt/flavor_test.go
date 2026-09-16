package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// aibap.mcp#377: ECC's ADT handler does not validate lockHandle (a bogus
// handle is silently accepted, no enqueue held), so the lockMap cache
// cannot be trusted to mirror server state there. SystemFlavor lets callers
// branch defensively on ECC vs S/4 without probing behaviorally on every
// write. Detection is discovery-based: /sap/bc/adt/packages is S/4-only
// (see CLAUDE.md "SAP ADT" and adt/object.go's CreatePackage 404 handling).

func TestSystemFlavor_PackagesEndpointAdvertised_ReturnsS4(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/packages">
      <app:accept>application/vnd.sap.adt.packages.v2+xml</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "token")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(discoveryXML))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	flavor, err := client.SystemFlavor(context.Background())
	if err != nil {
		t.Fatalf("SystemFlavor: %v", err)
	}
	if flavor != adt.SystemFlavorS4 {
		t.Errorf("flavor: got %v, want SystemFlavorS4", flavor)
	}
}

func TestSystemFlavor_PackagesEndpointAbsent_ReturnsECC(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-CSRF-Token", "token")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(discoveryXML))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	flavor, err := client.SystemFlavor(context.Background())
	if err != nil {
		t.Fatalf("SystemFlavor: %v", err)
	}
	if flavor != adt.SystemFlavorECC {
		t.Errorf("flavor: got %v, want SystemFlavorECC", flavor)
	}
}
