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

// releaseVerifiedServer mocks a synchronous release followed by a status read.
// postReleaseStatus is the tm:status attribute returned by the status GET; pass
// "" to make the status read fail (no <request> element).
func releaseVerifiedServer(t *testing.T, transport, postReleaseStatus string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "newreleasejobs"):
			// Synchronous release: report "released".
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <tm:releasereports><tm:checkReport chkrun:status="released"/></tm:releasereports>
</tm:root>`))
		case r.Method == http.MethodGet && r.URL.Path == "/sap/bc/adt/cts/transportrequests/"+transport:
			if postReleaseStatus == "" {
				// No request element → GetTransportInfo errors.
				_, _ = w.Write([]byte(`<?xml version="1.0"?><tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"/>`))
				return
			}
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:request tm:number="` + transport + `" tm:desc="d" tm:status="` + postReleaseStatus + `"/>
</tm:root>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestReleaseTransportVerified(t *testing.T) {
	cases := []struct {
		name              string
		postReleaseStatus string
		wantReleased      bool
	}{
		{"released (status L)", adt.TransportStatusReleased, true},
		{"silent fail (status D)", adt.TransportStatusModifiable, false},
		{"status read fails -> optimistic released", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const transport = "DEVK900123"
			srv := releaseVerifiedServer(t, transport, tc.postReleaseStatus)
			defer srv.Close()

			cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
			client := adt.NewClient(cfg)

			res, err := client.ReleaseTransportVerified(context.Background(), transport, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Transport != transport {
				t.Errorf("Transport = %q, want %q", res.Transport, transport)
			}
			if res.Released != tc.wantReleased {
				t.Errorf("Released = %v, want %v", res.Released, tc.wantReleased)
			}
		})
	}
}

func TestReleaseTransportVerified_ReleaseErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		// Both release endpoints fail → ReleaseTransport returns an error.
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	res, err := client.ReleaseTransportVerified(context.Background(), "DEVK900123", false)
	if err == nil {
		t.Fatalf("expected error, got result %+v", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}
