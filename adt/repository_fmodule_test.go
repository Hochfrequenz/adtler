package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	// fmoduleURI is a function module URI as search_objects returns it.
	fmoduleURI = "/sap/bc/adt/functions/groups/zmy_fg/fmodules/zmy_fm"

	// fmoduleAccept is the one media type SAP S/4HANA produces for a function
	// module. It is also what the 406 response of adtler#169 names.
	fmoduleAccept = "application/vnd.sap.adt.functions.fmodules.v3+xml"

	// fmoduleXML is a function module object document, reduced to what
	// parseGenericObjectInfo reads.
	fmoduleXML = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<fmodule:abapFunctionModule xmlns:fmodule="http://www.sap.com/adt/functions/fmodules"` +
		` xmlns:adtcore="http://www.sap.com/adt/core"` +
		` adtcore:name="ZMY_FM" adtcore:type="FUGR/FF" adtcore:description="Example function module">` +
		`<adtcore:packageRef adtcore:name="ZFMPACK"/></fmodule:abapFunctionModule>`
)

// strictFunctionModuleServer behaves like SAP S/4HANA for a function module:
// it answers 200 only to a request whose Accept header names the fmodules
// media type and 406 to everything else, including */*. Refusing */* too
// means a client that reaches the object only through the 406 retry fails
// this server — the first request has to be right.
func strictFunctionModuleServer(offers *[]string, mu *sync.Mutex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != fmoduleURI {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		accept := r.Header.Get("Accept")
		mu.Lock()
		*offers = append(*offers, accept)
		mu.Unlock()
		if !strings.Contains(accept, fmoduleAccept) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte(notAcceptableXML))
			return
		}
		w.Header().Set("Content-Type", fmoduleAccept)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmoduleXML))
	}))
}

// TestGetObjectInfo_FunctionModule_FirstRequestCarriesFmodulesType covers
// adtler#169: the first request for a function module must already offer the
// fmodules media type, so the lookup does not cost a 406 and a retry on
// S/4HANA. The same call must also report the URI it was asked for.
func TestGetObjectInfo_FunctionModule_FirstRequestCarriesFmodulesType(t *testing.T) {
	var (
		offers []string
		mu     sync.Mutex
	)
	srv := strictFunctionModuleServer(&offers, &mu)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	info, err := client.GetObjectInfo(context.Background(), fmoduleURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(offers) != 1 {
		t.Fatalf("server saw %d requests (%v), want exactly 1 — a retry means the first offer was wrong", len(offers), offers)
	}
	if !strings.Contains(offers[0], fmoduleAccept) {
		t.Errorf("first offer was %q, want it to contain %q", offers[0], fmoduleAccept)
	}
	if info.Name != "ZMY_FM" {
		t.Errorf("name: got %q, want %q", info.Name, "ZMY_FM")
	}
	if info.PackageName != "ZFMPACK" {
		t.Errorf("package: got %q, want %q", info.PackageName, "ZFMPACK")
	}
	if info.URI != fmoduleURI {
		t.Errorf("URI: got %q, want %q — GetObjectInfo must report the URI it fetched", info.URI, fmoduleURI)
	}
}
