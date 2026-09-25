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

// releaseTransportServer mocks a synchronous release followed by a status read.
// postReleaseStatus is the tm:status attribute returned by the status GET; pass
// "" to make the status read fail (no <request> element).
func releaseTransportServer(t *testing.T, transport, postReleaseStatus string) *httptest.Server {
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

func TestReleaseTransport_Verified(t *testing.T) {
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
			srv := releaseTransportServer(t, transport, tc.postReleaseStatus)
			defer srv.Close()

			cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
			client := adt.NewClient(cfg)

			res, err := client.ReleaseTransport(context.Background(), transport)
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

// TestReleaseTransport_ECCWorklistStatusRead_ReportsModifiable is the
// regression guard for aibap.mcp#496: on ECC the post-release status read
// (GetTransportInfo) does not come back as a Format 1 single-request body —
// it comes back as the whole transport-organizer worklist (eccWorklistXML),
// nesting requests under <tm:workbench>/<tm:modifiable>. Before Task 5,
// parseTransportInfo only bound a request as a direct child of the root, so
// it never found DEVK902952 in that shape and GetTransportInfo always
// errored on ECC; ReleaseTransport treats a failed status read as "assume
// released" (see its doc comment), so the silent-failure detection this
// method exists for never fired on the one system it targets. With
// parseTransportInfo fixed, the status read succeeds, finds DEVK902952 still
// at status "D" (modifiable — see eccWorklistXML), and this test asserts the
// caller now gets Released: false instead of the false-positive Released:
// true.
func TestReleaseTransport_ECCWorklistStatusRead_ReportsModifiable(t *testing.T) {
	const transport = "DEVK902952"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "newreleasejobs"):
			// Synchronous release: report "released", exactly like the S/4
			// case — the release call itself does not expose the ECC bug.
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <tm:releasereports><tm:checkReport chkrun:status="released"/></tm:releasereports>
</tm:root>`))
		case r.Method == http.MethodGet && r.URL.Path == "/sap/bc/adt/cts/transportrequests/"+transport:
			// The ECC bug this test guards: the post-release status GET for a
			// single transport number answers with the whole worklist body
			// instead of that one request.
			_, _ = w.Write([]byte(eccWorklistXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	res, err := client.ReleaseTransport(context.Background(), transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Transport != transport {
		t.Errorf("Transport = %q, want %q", res.Transport, transport)
	}
	if res.Released {
		t.Errorf("Released = true, want false: eccWorklistXML shows %s still at status %q (modifiable)",
			transport, adt.TransportStatusModifiable)
	}
}

func TestReleaseTransport_ReleaseErrorPropagates(t *testing.T) {
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

	res, err := client.ReleaseTransport(context.Background(), "DEVK900123")
	if err == nil {
		t.Fatalf("expected error, got result %+v", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}
