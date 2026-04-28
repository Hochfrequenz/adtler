package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	atcWorklistsPath = "/sap/bc/adt/atc/worklists"
	atcRunsPath      = "/sap/bc/adt/atc/runs"
)

// atcMockServer simulates the canonical 3-step ATC flow:
//  1. POST /sap/bc/adt/atc/worklists?checkVariant=...   → returns plain-text worklist ID
//  2. POST /sap/bc/adt/atc/runs?worklistId=...           → returns <worklistRun>
//  3. GET  /sap/bc/adt/atc/worklists/{id}                → returns empty <worklist>
//
// All requests are recorded under c.mu so individual tests can assert on
// order, paths, query strings, headers, and bodies.
type atcMockCapture struct {
	mu sync.Mutex

	// Order of meaningful (non-CSRF) calls received.
	calls []string

	// Worklists POST.
	worklistsCalled       bool
	worklistsQuery        string
	worklistsAcceptHeader string

	// Runs POST.
	runsQuery       string
	runsContentType string
	runsBody        string

	// Worklist GET.
	worklistGetPath string

	// Server-side responses (configurable per-test).
	worklistID       string // returned by the worklists POST
	runResponseID    string // worklistRun/@id in the runs response (empty = same as worklistID)
	discoveryXML     string
	worklistFindings string // body returned by the worklist GET
}

func newATCMock(t *testing.T, c *atcMockCapture) *httptest.Server {
	t.Helper()

	if c.worklistID == "" {
		c.worklistID = "WL000000000000000001"
	}
	if c.worklistFindings == "" {
		c.worklistFindings = `<?xml version="1.0"?><atcworklist:worklist xmlns:atcworklist="http://www.sap.com/adt/atc/worklist" atcworklist:id="` + c.worklistID + `"/>`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()

		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			if c.discoveryXML != "" {
				_, _ = w.Write([]byte(c.discoveryXML))
			}

		case r.Method == http.MethodPost && r.URL.Path == atcWorklistsPath:
			c.calls = append(c.calls, "worklists POST")
			c.worklistsCalled = true
			c.worklistsQuery = r.URL.RawQuery
			c.worklistsAcceptHeader = r.Header.Get("Accept")
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(c.worklistID))

		case r.Method == http.MethodPost && r.URL.Path == atcRunsPath:
			c.calls = append(c.calls, "runs POST")
			c.runsQuery = r.URL.RawQuery
			c.runsContentType = r.Header.Get("Content-Type")
			body, _ := io.ReadAll(r.Body)
			c.runsBody = string(body)
			id := c.runResponseID
			if id == "" {
				id = c.worklistID
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><atc:worklistRun xmlns:atc="http://www.sap.com/adt/atc" atc:worklistId="` + id + `" atc:worklistTimestamp="2026-04-27T12:00:00Z"/>`))

		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, atcWorklistsPath+"/"):
			c.calls = append(c.calls, "worklist GET")
			c.worklistGetPath = r.URL.Path
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(c.worklistFindings))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newATCClient(srv *httptest.Server) adt.Client {
	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	return adt.NewClient(cfg)
}

// TestRunATCCheck_CreatesWorklistBeforeRun verifies the canonical 3-step
// flow: POST /worklists must precede POST /runs, which must precede the
// findings GET. This is the difference that fixes adtler#12 — R/3 returns
// 500 on a /runs POST that references a non-existent worklist ID.
func TestRunATCCheck_CreatesWorklistBeforeRun(t *testing.T) {
	cap := &atcMockCapture{}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	want := []string{"worklists POST", "runs POST", "worklist GET"}
	cap.mu.Lock()
	got := append([]string(nil), cap.calls...)
	cap.mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("call order: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRunATCCheck_WorklistsPOSTUsesCheckVariantQuery verifies the variant is
// passed on the /worklists URL query (as observed in abap-adt-api), not in
// the /runs body.
func TestRunATCCheck_WorklistsPOSTUsesCheckVariantQuery(t *testing.T) {
	cap := &atcMockCapture{}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "MY_VARIANT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if !cap.worklistsCalled {
		t.Fatalf("expected POST %s, was not called", atcWorklistsPath)
	}
	if got, want := cap.worklistsQuery, "checkVariant=MY_VARIANT"; got != want {
		t.Errorf("worklists query: got %q, want %q", got, want)
	}
}

// TestRunATCCheck_WorklistsPOSTSendsTextPlainAccept — the worklist
// creation response is a plain-text worklist ID, so the request must
// advertise Accept: text/plain (as observed in abap-adt-api).
func TestRunATCCheck_WorklistsPOSTSendsTextPlainAccept(t *testing.T) {
	cap := &atcMockCapture{}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if got, want := cap.worklistsAcceptHeader, "text/plain"; got != want {
		t.Errorf("worklists Accept: got %q, want %q", got, want)
	}
}

// TestRunATCCheck_RunsBodyOmitsCheckVariantAttribute — once the variant is
// established by the worklist creation, the /runs body must not carry
// checkVariant. Sending it can confuse R/3.
func TestRunATCCheck_RunsBodyOmitsCheckVariantAttribute(t *testing.T) {
	cap := &atcMockCapture{}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	body := cap.runsBody
	cap.mu.Unlock()
	if strings.Contains(body, "checkVariant") {
		t.Errorf("runs body must not contain checkVariant; got: %s", body)
	}
}

// TestRunATCCheck_PropagatesWorklistIDFromWorklistsResponse — the ID returned
// from the /worklists POST must be used as worklistId on the /runs URL and
// in the findings GET path.
func TestRunATCCheck_PropagatesWorklistIDFromWorklistsResponse(t *testing.T) {
	cap := &atcMockCapture{worklistID: "WLABC123"}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if got, want := cap.runsQuery, "worklistId=WLABC123"; got != want {
		t.Errorf("runs query: got %q, want %q", got, want)
	}
	if got, want := cap.worklistGetPath, atcWorklistsPath+"/WLABC123"; got != want {
		t.Errorf("worklist GET path: got %q, want %q", got, want)
	}
}

// TestRunATCCheck_PrefersWorklistIDFromRunsResponse — if the /runs response
// carries a different worklistId in the <worklistRun> envelope, that ID
// (rather than the one from step 1) is used to fetch findings.
func TestRunATCCheck_PrefersWorklistIDFromRunsResponse(t *testing.T) {
	cap := &atcMockCapture{
		worklistID:    "WL_FROM_STEP1",
		runResponseID: "WL_FROM_STEP2",
	}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if got, want := cap.worklistGetPath, atcWorklistsPath+"/WL_FROM_STEP2"; got != want {
		t.Errorf("worklist GET path: got %q, want %q", got, want)
	}
}

// TestRunATCCheck_DefaultsVariantToDEFAULT — empty caller variant maps to
// "DEFAULT" on the worklists URL. Avoids GetATCCustomizing (which has its
// own session-state issue, adtler#44).
func TestRunATCCheck_DefaultsVariantToDEFAULT(t *testing.T) {
	cap := &atcMockCapture{}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if got, want := cap.worklistsQuery, "checkVariant=DEFAULT"; got != want {
		t.Errorf("worklists query for empty variant: got %q, want %q", got, want)
	}
}

// TestRunATCCheck_PercentEncodesCheckVariant — variants with characters that
// need URL escaping (spaces, slashes) must be safely encoded on the
// worklists URL. SAP variant names are typically ALL_CAPS_UNDERSCORES, but
// the client must not corrupt unusual values.
func TestRunATCCheck_PercentEncodesCheckVariant(t *testing.T) {
	cap := &atcMockCapture{}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "MY VARIANT/X")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	want := "checkVariant=MY+VARIANT%2FX"
	if got := cap.worklistsQuery; got != want {
		t.Errorf("worklists query: got %q, want %q", got, want)
	}
}

// TestRunATCCheck_UsesDiscoveryAdvertisedContentType — when discovery
// advertises a vendor MIME type for /sap/bc/adt/atc/runs, that type drives
// the runs POST Content-Type. Regression guard for the adtler#35 refactor.
func TestRunATCCheck_UsesDiscoveryAdvertisedContentType(t *testing.T) {
	cap := &atcMockCapture{
		discoveryXML: `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/atc/runs">
      <app:accept>application/vnd.sap.adt.atc.runs.v1+xml</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`,
	}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	want := "application/vnd.sap.adt.atc.runs.v1+xml"
	if cap.runsContentType != want {
		t.Errorf("runs Content-Type: got %q, want %q", cap.runsContentType, want)
	}
}

// TestRunATCCheck_FallbackContentTypeWhenDiscoveryEmpty — when discovery
// has no entry for /sap/bc/adt/atc/runs, the runs POST falls back to
// application/xml.
func TestRunATCCheck_FallbackContentTypeWhenDiscoveryEmpty(t *testing.T) {
	cap := &atcMockCapture{}
	srv := newATCMock(t, cap)
	client := newATCClient(srv)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}

	cap.mu.Lock()
	defer cap.mu.Unlock()
	if cap.runsContentType != "application/xml" {
		t.Errorf("runs Content-Type fallback: got %q, want %q", cap.runsContentType, "application/xml")
	}
}
