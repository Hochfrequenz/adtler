package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// TestResolveETag_FallsBackToFetchETag regression-tests adtler#9 and #14:
// when GetSource fails (e.g. 400 for CLAS, 404 for DTEL/DOMA on S/4 because
// /source/main doesn't exist for those types), ResolveETag falls back to
// FetchETag which GETs the bare object URI with the type-appropriate Accept.
func TestResolveETag_FallsBackToFetchETag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		// /source/main returns 400 (mimics CLAS on S/4)
		case r.URL.Path == "/sap/bc/adt/oo/classes/zcl_test/source/main":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><exc:ExceptionText xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><message>Resource ZCL_TEST: wrong input data</message></exc:ExceptionText>`))
		// Bare class URI returns the object metadata WITH an ETag header
		case r.URL.Path == "/sap/bc/adt/oo/classes/zcl_test":
			w.Header().Set("ETag", `"etag-from-bare-uri"`)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<class:abapClass xmlns:class="http://www.sap.com/adt/oo/classes" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="ZCL_TEST" adtcore:type="CLAS/OC"/>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)
	lockMap := adt.NewLockMap()

	etag, err := lockMap.ResolveETag(context.Background(), client,
		"test:zcl_test", "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("expected fallback to FetchETag, got error: %v", err)
	}
	if etag != `"etag-from-bare-uri"` {
		t.Errorf("etag: got %q, want the ETag from the bare class URI", etag)
	}
}
