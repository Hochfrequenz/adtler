package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// noAtomLinksTransportXML is Format 1 (a single <tm:request> directly under
// <tm:root> — see xmlTransportDoc) with no <atom:link> anywhere. It exists to
// pin the "no atom relation present at all" branch of
// deriveRemoveObjectSupport, which must leave the capability at
// RemoveObjectSupportUnknown rather than guessing.
const noAtomLinksTransportXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<tm:request tm:number="DEVK900001" tm:owner="DEV" tm:desc="No links" tm:status="D"/>` +
	`</tm:root>`

// TestRemoveObjectSupport_ECCWorklist_Unsupported pins the acceptance
// criterion that eccWorklistXML (consistencycheck, releasejobs, modify,
// newtask — no addobject, no removeobject) yields
// RemoveObjectSupportUnsupported.
func TestRemoveObjectSupport_ECCWorklist_Unsupported(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	if _, err := client.GetTransportObjects(context.Background(), "HFQK900178"); err != nil {
		t.Fatalf("GetTransportObjects: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnsupported {
		t.Errorf("got %v, want RemoveObjectSupportUnsupported", got)
	}
}

// TestRemoveObjectSupport_ECCCustomizing_Unsupported extends the ECC case to
// the customizing-group shape (modify only).
func TestRemoveObjectSupport_ECCCustomizing_Unsupported(t *testing.T) {
	client := newFixtureClient(t, eccCustomizingXML)

	if _, err := client.GetTransportObjects(context.Background(), "HFQK900178"); err != nil {
		t.Fatalf("GetTransportObjects: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnsupported {
		t.Errorf("got %v, want RemoveObjectSupportUnsupported", got)
	}
}

// TestRemoveObjectSupport_S4SingleRequest_Supported pins the acceptance
// criterion that s4SingleRequestXML, which carries both removeobject and
// addobject, yields RemoveObjectSupportSupported.
func TestRemoveObjectSupport_S4SingleRequest_Supported(t *testing.T) {
	client := newFixtureClient(t, s4SingleRequestXML)

	if _, err := client.GetTransportObjects(context.Background(), "S4UK904438"); err != nil {
		t.Fatalf("GetTransportObjects: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportSupported {
		t.Errorf("got %v, want RemoveObjectSupportSupported", got)
	}
}

// TestRemoveObjectSupport_S4ObjectAtBothLevels_Supported covers the other
// "both markers present" fixture named in the acceptance criteria.
func TestRemoveObjectSupport_S4ObjectAtBothLevels_Supported(t *testing.T) {
	client := newFixtureClient(t, s4ObjectAtBothLevelsXML)

	if _, err := client.GetTransportObjects(context.Background(), "S4UK904438"); err != nil {
		t.Fatalf("GetTransportObjects: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportSupported {
		t.Errorf("got %v, want RemoveObjectSupportSupported", got)
	}
}

// TestRemoveObjectSupport_S4RequestNoObjects_Supported pins the fixture that
// exercises the addobject-without-removeobject case: s4RequestNoObjectsXML
// holds no abap_object (hence no removeobject link) but does carry addobject
// at both request and task level. Per the stated derivation rule, addobject
// alone is enough for "supported" — it is the discriminator that tells ECC
// apart from an S/4 request that simply holds no objects yet.
func TestRemoveObjectSupport_S4RequestNoObjects_Supported(t *testing.T) {
	client := newFixtureClient(t, s4RequestNoObjectsXML)

	if _, err := client.GetTransportObjects(context.Background(), "S4UK904476"); err != nil {
		t.Fatalf("GetTransportObjects: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportSupported {
		t.Errorf("got %v, want RemoveObjectSupportSupported", got)
	}
}

// TestRemoveObjectSupport_NoAtomLinks_Unknown pins the acceptance criterion
// that a body with no atom relations at all leaves the capability unknown —
// it cannot tell ECC apart from S/4 and must not guess either way.
func TestRemoveObjectSupport_NoAtomLinks_Unknown(t *testing.T) {
	client := newFixtureClient(t, noAtomLinksTransportXML)

	if _, err := client.GetTransportInfo(context.Background(), "DEVK900001"); err != nil {
		t.Fatalf("GetTransportInfo: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnknown {
		t.Errorf("got %v, want RemoveObjectSupportUnknown", got)
	}
}

// TestRemoveObjectSupport_FreshClient_StartsUnknown pins the zero value: a
// client that has never read a transport reports Unknown.
func TestRemoveObjectSupport_FreshClient_StartsUnknown(t *testing.T) {
	cfg := sapmcpconfig.SAPSystem{Host: "http://example.invalid", User: "U", Password: "P", Client: "100"}
	client := adt.NewClientForTest(cfg)

	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnknown {
		t.Errorf("got %v, want RemoveObjectSupportUnknown", got)
	}
}

// TestRemoveObjectSupport_SkipsRederivationOnceKnown asserts the "skip the
// derivation entirely once the state is known" rule observably: once the
// capability is Unsupported (from an ECC body), a later read of a body that
// would derive Supported must NOT change the cached state.
func TestRemoveObjectSupport_SkipsRederivationOnceKnown(t *testing.T) {
	var body atomic.Value
	body.Store(eccWorklistXML)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/sap/bc/adt/cts/transportrequests/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body.Load().(string)))
	}))
	t.Cleanup(srv.Close)

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClientForTest(cfg)

	if _, err := client.GetTransportObjects(context.Background(), "HFQK900178"); err != nil {
		t.Fatalf("first read: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnsupported {
		t.Fatalf("after ECC read: got %v, want Unsupported", got)
	}

	// Switch the server to an S/4 body that would derive Supported if the
	// capability were re-derived. Caching must keep it at Unsupported.
	body.Store(s4SingleRequestXML)
	if _, err := client.GetTransportObjects(context.Background(), "S4UK904438"); err != nil {
		t.Fatalf("second read: unexpected error: %v", err)
	}
	if got := client.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnsupported {
		t.Errorf("after second read with a different body: got %v, want state to remain Unsupported (cached, not re-derived)", got)
	}
}

// TestRemoveObjectSupport_SeparateClients_DoNotShareCachedState pins that the
// cached removeobject capability lives on the *httpClient instance, not
// anywhere package-global: a second, independent client starts at Unknown
// regardless of what an earlier client has already learned. This does not
// actually exercise freshSession (there is no direct way to reach its
// isolated *httpClient from this package's tests — RunClass is the only
// public entry point that uses it internally, and it does not expose a way
// to read the capability back out); it only stands in for that guarantee via
// two ordinary TestClients, the same way
// TestRemoveObjectSupport_FreshClient_StartsUnknown does for a single one.
// freshSession's own doc comment in client.go is the source of truth for why
// its *httpClient copies none of these fields.
func TestRemoveObjectSupport_SeparateClients_DoNotShareCachedState(t *testing.T) {
	client1 := newFixtureClient(t, eccWorklistXML)
	if _, err := client1.GetTransportObjects(context.Background(), "HFQK900178"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := client1.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnsupported {
		t.Fatalf("client1: got %v, want Unsupported", got)
	}

	// A second, independent client (standing in for what freshSession would
	// build: a brand-new *httpClient sharing no cache with client1) starts at
	// Unknown regardless of what client1 has already learned.
	cfg := sapmcpconfig.SAPSystem{Host: "http://example.invalid", User: "U", Password: "P", Client: "100"}
	client2 := adt.NewClientForTest(cfg)
	if got := client2.RemoveObjectSupportForTest(); got != adt.RemoveObjectSupportUnknown {
		t.Errorf("client2: got %v, want Unknown (independent client state)", got)
	}
}

// TestRemoveObjectSupport_CachedByGetTransportObjects_RemoveFromTransportIssuesNoGET
// asserts caching observably at the API boundary: after one
// GetTransportObjects call against an ECC body, a subsequent
// RemoveFromTransport call issues no further GET against the
// transportrequests endpoint — Task 7's gate (ensureRemoveObjectSupported)
// consults the cached state instead of re-reading it. Written before that
// gate existed, this test originally asserted RemoveFromTransport still
// succeeded; now that the cached state is confirmed
// RemoveObjectSupportUnsupported, the gate blocks the call before any PUT is
// sent, so the correct assertion is an ErrorNotSupported error and zero PUTs
// — not a successful call. See TestRemoveFromTransport_ECCUnsupported_NeverSendsPUT
// (transport_removeobject_gate_test.go) for the same blocking behavior
// pinned end-to-end without relying on a prior GetTransportObjects call to
// warm the cache.
func TestRemoveObjectSupport_CachedByGetTransportObjects_RemoveFromTransportIssuesNoGET(t *testing.T) {
	var getCount, putCount int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sap/bc/adt/cts/transportrequests/") {
			atomic.AddInt32(&getCount, 1)
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(eccWorklistXML))
			return
		}
		if r.Method == http.MethodPut {
			atomic.AddInt32(&putCount, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetTransportObjects(context.Background(), "HFQK900178"); err != nil {
		t.Fatalf("GetTransportObjects: unexpected error: %v", err)
	}
	if got := atomic.LoadInt32(&getCount); got != 1 {
		t.Fatalf("after GetTransportObjects: got %d GETs, want 1", got)
	}

	err := client.RemoveFromTransport(context.Background(),
		"HFQK900635", "HFQK900178", "R3TR", "PROG", "/HFQ/ORDER_REQUEST", "PROG/P", "000001")
	if err == nil {
		t.Fatal("RemoveFromTransport: expected ErrorNotSupported, got nil (cached state is Unsupported)")
	}
	if got := adt.ClassifyError(err); got != adt.ErrorNotSupported {
		t.Errorf("ClassifyError = %v, want ErrorNotSupported", got)
	}
	if got := atomic.LoadInt32(&getCount); got != 1 {
		t.Errorf("after RemoveFromTransport: got %d GETs total, want still 1 (no further GET)", got)
	}
	if got := atomic.LoadInt32(&putCount); got != 0 {
		t.Errorf("after RemoveFromTransport: got %d PUTs, want 0 (gate must block before sending)", got)
	}
}
