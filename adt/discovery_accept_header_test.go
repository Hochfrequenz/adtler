package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// Live-probing s4u turned up a real bug: fetchCSRFToken's discovery GET
// (/sap/bc/adt/discovery) sent no Accept header at all. hfq tolerates that;
// s4u does not — it 400s with "Accept header missing"
// (ExceptionResourceBadRequest), and the error was silently swallowed
// (fetchCSRFToken only checked the response body length, never the status
// code), leaving the discovery cache permanently empty for the client's
// whole lifetime. NegotiateContentType's default-fallback design hid this
// in production: callers kept working off hardcoded content types with no
// error, so discovery-driven content negotiation was silently dead on s4u
// and nobody noticed. This reproduces the server behavior and asserts
// discovery still populates.
func TestDiscovery_ServerRequiresAcceptHeader_StillPopulatesCache(t *testing.T) {
	discoveryXML := discoveryXMLProgramsUTF8Only
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") == "" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionResourceBadRequest"/><message lang="EN">Request could not be understood by the server due to malformed syntax: Accept header missing</message></exc:exception>`))
			return
		}
		w.Header().Set("X-CSRF-Token", "token")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(discoveryXML))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClientForTest(cfg)

	if err := client.LoadDiscoveryForTest(context.Background()); err != nil {
		t.Fatalf("LoadDiscoveryForTest: %v", err)
	}

	// Discovery advertises "text/plain; charset=utf-8" — sourceContentType's
	// PREFERRED type when discovery has it, but NOT its hardcoded fallback
	// default ("text/plain", no charset, used when discovery is empty). An
	// earlier version of this test asserted "text/plain" here, which is
	// wrong: sourceContentType returns exactly that value on EMPTY discovery
	// too (adt/source.go's contentTypeTextPlain fallback), so that assertion
	// passed identically whether discovery populated or not — it proved
	// nothing. Asserting the charset variant only passes if discovery was
	// actually consulted. Same technique as the pre-existing
	// TestSourceContentType_DiscoveryAdvertisesType_UsesIt.
	got := client.SourceContentTypeForTest("/sap/bc/adt/programs/programs/ZTEST")
	want := "text/plain; charset=utf-8"
	if got != want {
		t.Errorf("discovery cache after a server that demands Accept: got content type %q, want %q (discovery is empty — the Accept-header 400 was swallowed)", got, want)
	}
}
