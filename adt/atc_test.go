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

func TestRunATCCheck_UsesDiscoveryAdvertisedContentType(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/atc/runs">
      <app:accept>application/vnd.sap.adt.atc.runs.v1+xml</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedCT string
	worklistXML := `<?xml version="1.0"?><atcworklist:worklist xmlns:atcworklist="http://www.sap.com/adt/atc/worklist" atcworklist:id="0000000000"/>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
		case r.Method == http.MethodPost && r.URL.Path == "/sap/bc/adt/atc/runs":
			capturedCT = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sap/bc/adt/atc/worklists/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(worklistXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}
	want := "application/vnd.sap.adt.atc.runs.v1+xml"
	if capturedCT != want {
		t.Errorf("Content-Type: got %q, want %q", capturedCT, want)
	}
}

func TestRunATCCheck_FallbackContentTypeWhenDiscoveryEmpty(t *testing.T) {
	var capturedCT string
	worklistXML := `<?xml version="1.0"?><atcworklist:worklist xmlns:atcworklist="http://www.sap.com/adt/atc/worklist" atcworklist:id="0000000000"/>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			// Empty discovery body — no entries
		case r.Method == http.MethodPost && r.URL.Path == "/sap/bc/adt/atc/runs":
			capturedCT = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sap/bc/adt/atc/worklists/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(worklistXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}
	if capturedCT != "application/xml" {
		t.Errorf("Content-Type fallback: got %q, want %q", capturedCT, "application/xml")
	}
}
