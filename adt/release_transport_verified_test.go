package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

// releaseTransportServer mocks a synchronous release followed by a status read.
// postReleaseStatus is the tm:status attribute returned by the status GET; pass
// "" to make the status read fail (no <request> element), or
// releaseStatusOmitted to return the request without a tm:status attribute.
const releaseStatusOmitted = "<omitted>"

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
			statusAttr := ` tm:status="` + postReleaseStatus + `"`
			if postReleaseStatus == releaseStatusOmitted {
				statusAttr = ""
			}
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:request tm:number="` + transport + `" tm:desc="d"` + statusAttr + `/>
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
		wantState         adt.TransportState
		wantRaw           string
	}{
		{"released (status R)", adt.TransportStatusReleased, adt.TransportStateReleased, adt.TransportStatusReleased},
		{"released, import protected (status N)", adt.TransportStatusReleasedProtected, adt.TransportStateReleased, adt.TransportStatusReleasedProtected},
		{"silent fail (status D)", adt.TransportStatusModifiable, adt.TransportStateModifiable, adt.TransportStatusModifiable},
		// #270: L is modifiable (protected), not released.
		{"still modifiable, protected (status L)", adt.TransportStatusModifiableProtected, adt.TransportStateModifiable, adt.TransportStatusModifiableProtected},
		// #270: O means the release started but has not finished.
		{"release started (status O)", adt.TransportStatusReleaseStarted, adt.TransportStateReleaseInProgress, adt.TransportStatusReleaseStarted},
		// #274: an unconfirmed release is unknown, never released.
		{"unknown status letter", "X", adt.TransportStateUnknown, "X"},
		{"status read fails", "", adt.TransportStateUnknown, ""},
		{"status read has no status", releaseStatusOmitted, adt.TransportStateUnknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const transport = "AAAK900123"
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
			if res.State != tc.wantState {
				t.Errorf("State = %v, want %v", res.State, tc.wantState)
			}
			if res.RawStatus != tc.wantRaw {
				t.Errorf("RawStatus = %q, want %q", res.RawStatus, tc.wantRaw)
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
// it never found AAAK902952 in that shape and GetTransportInfo always
// errored on ECC; ReleaseTransport then treated a failed status read as
// "assume released", so the silent-failure detection this method exists for
// never fired on the one system it targets. With parseTransportInfo fixed,
// the status read succeeds, finds AAAK902952 still at status "D"
// (modifiable — see eccWorklistXML), and this test asserts the caller gets
// TransportStateModifiable.
func TestReleaseTransport_ECCWorklistStatusRead_ReportsModifiable(t *testing.T) {
	const transport = "AAAK902952"
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
	if res.State != adt.TransportStateModifiable {
		t.Errorf("State = %v, want modifiable: eccWorklistXML shows %s still at status %q",
			res.State, transport, adt.TransportStatusModifiable)
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

	res, err := client.ReleaseTransport(context.Background(), "AAAK900123")
	if err == nil {
		t.Fatalf("expected error, got result %+v", res)
	}
	if res != nil {
		t.Errorf("expected nil result on error, got %+v", res)
	}
}
