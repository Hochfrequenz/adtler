package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	wantSymbolsContentType    = "application/vnd.sap.adt.textelements.symbols.v1"
	wantSelectionsContentType = "application/vnd.sap.adt.textelements.selections.v1"
	wantSessionTypeStateful   = "stateful"
)

// recordedRequest captures the parts of an HTTP request the unit tests
// need to assert on (path, query string, headers, body).
type recordedRequest struct {
	method      string
	path        string
	rawQuery    string
	contentType string
	accept      string
	sessionType string
	body        string
}

// newSetTextElementsServer returns an httptest server that responds 200 to
// every PUT to a textelements URL and records the request. The CSRF
// endpoint is handled automatically.
func newSetTextElementsServer(t *testing.T, recorded *[]recordedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		*recorded = append(*recorded, recordedRequest{
			method:      r.Method,
			path:        r.URL.Path,
			rawQuery:    r.URL.RawQuery,
			contentType: r.Header.Get("Content-Type"),
			accept:      r.Header.Get("Accept"),
			sessionType: r.Header.Get("X-sap-adt-sessiontype"),
			body:        string(bodyBytes),
		})
		w.WriteHeader(http.StatusOK)
	}))
}

// TestSetTextElements_SendsBothEndpointsWithCorrectShape verifies the PUT
// requests SetTextElements emits when both symbols and selections are
// supplied: two requests (one per child resource), each with the right
// vendor MIME types, the stateful sessiontype header, the lockHandle and
// corrNr URL parameters, and a body that round-trips through the format
// helpers.
func TestSetTextElements_SendsBothEndpointsWithCorrectShape(t *testing.T) {
	var recorded []recordedRequest
	srv := newSetTextElementsServer(t, &recorded)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	symbols := []adt.TextSymbol{
		{Key: "001", Text: "Hello", MaxLength: 30},
	}
	selections := []adt.SelectionText{
		{Name: "P_TEST", Text: "Label"},
	}

	const (
		programURI = "/sap/bc/adt/programs/programs/ZTEST"
		lockHandle = "ABCDEF1234567890"
		transport  = "DEVK900001"
	)

	if err := client.SetTextElements(context.Background(), programURI, symbols, selections, lockHandle, transport); err != nil {
		t.Fatalf("SetTextElements: %v", err)
	}

	if len(recorded) != 2 {
		t.Fatalf("expected 2 PUTs (symbols + selections), got %d", len(recorded))
	}

	checkSymbols := recorded[0]
	if got, want := checkSymbols.path, "/sap/bc/adt/textelements/programs/ZTEST/source/symbols"; got != want {
		t.Errorf("symbols path: got %q, want %q", got, want)
	}
	if checkSymbols.contentType != wantSymbolsContentType {
		t.Errorf("symbols Content-Type: got %q, want %q", checkSymbols.contentType, wantSymbolsContentType)
	}
	if checkSymbols.accept != wantSymbolsContentType {
		t.Errorf("symbols Accept: got %q, want %q", checkSymbols.accept, wantSymbolsContentType)
	}
	if checkSymbols.sessionType != wantSessionTypeStateful {
		t.Errorf("symbols X-sap-adt-sessiontype: got %q, want %q", checkSymbols.sessionType, wantSessionTypeStateful)
	}
	if !strings.Contains(checkSymbols.rawQuery, "lockHandle=ABCDEF1234567890") {
		t.Errorf("symbols query missing lockHandle: %q", checkSymbols.rawQuery)
	}
	if !strings.Contains(checkSymbols.rawQuery, "corrNr=DEVK900001") {
		t.Errorf("symbols query missing corrNr: %q", checkSymbols.rawQuery)
	}
	if !strings.Contains(checkSymbols.body, "001=Hello") {
		t.Errorf("symbols body missing entry: %q", checkSymbols.body)
	}

	checkSelections := recorded[1]
	if got, want := checkSelections.path, "/sap/bc/adt/textelements/programs/ZTEST/source/selections"; got != want {
		t.Errorf("selections path: got %q, want %q", got, want)
	}
	if checkSelections.contentType != wantSelectionsContentType {
		t.Errorf("selections Content-Type: got %q, want %q", checkSelections.contentType, wantSelectionsContentType)
	}
	if checkSelections.accept != wantSelectionsContentType {
		t.Errorf("selections Accept: got %q, want %q", checkSelections.accept, wantSelectionsContentType)
	}
	if checkSelections.sessionType != wantSessionTypeStateful {
		t.Errorf("selections X-sap-adt-sessiontype: got %q, want %q", checkSelections.sessionType, wantSessionTypeStateful)
	}
	if !strings.Contains(checkSelections.rawQuery, "lockHandle=ABCDEF1234567890") {
		t.Errorf("selections query missing lockHandle: %q", checkSelections.rawQuery)
	}
	if !strings.Contains(checkSelections.rawQuery, "corrNr=DEVK900001") {
		t.Errorf("selections query missing corrNr: %q", checkSelections.rawQuery)
	}
	if !strings.Contains(checkSelections.body, "P_TEST") || !strings.Contains(checkSelections.body, "Label") {
		t.Errorf("selections body missing entry: %q", checkSelections.body)
	}
}

// TestSetTextElements_PercentEncodesURLParameters verifies that lockHandle
// and corrNr are passed through url.Values so any special characters in
// the values are correctly percent-encoded.
func TestSetTextElements_PercentEncodesURLParameters(t *testing.T) {
	var recorded []recordedRequest
	srv := newSetTextElementsServer(t, &recorded)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	const (
		programURI    = "/sap/bc/adt/programs/programs/ZTEST"
		spicyHandle   = "abc+def/ghi=jkl"
		spicyCorrNr   = "DEV K900&001"
		wantLockEnc   = "abc%2Bdef%2Fghi%3Djkl"
		wantCorrNrEnc = "DEV+K900%26001"
	)

	if err := client.SetTextElements(context.Background(), programURI,
		[]adt.TextSymbol{{Key: "001", Text: "x"}}, nil,
		spicyHandle, spicyCorrNr); err != nil {
		t.Fatalf("SetTextElements: %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("expected 1 PUT (symbols only), got %d", len(recorded))
	}
	q := recorded[0].rawQuery
	if !strings.Contains(q, "lockHandle="+wantLockEnc) {
		t.Errorf("lockHandle not percent-encoded; rawQuery=%q want substring %q", q, "lockHandle="+wantLockEnc)
	}
	if !strings.Contains(q, "corrNr="+wantCorrNrEnc) {
		t.Errorf("corrNr not percent-encoded; rawQuery=%q want substring %q", q, "corrNr="+wantCorrNrEnc)
	}
}

// TestSetTextElements_NoTransport_OmitsCorrNr verifies that when the
// transport argument is empty, no corrNr query parameter is sent. SAP
// systems that don't require a transport (e.g. $TMP packages) accept
// requests without it; sending an empty corrNr= would still satisfy
// strict-parameter-presence checks but would also send a meaningless
// empty value, so we omit it entirely.
func TestSetTextElements_NoTransport_OmitsCorrNr(t *testing.T) {
	var recorded []recordedRequest
	srv := newSetTextElementsServer(t, &recorded)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if err := client.SetTextElements(context.Background(),
		"/sap/bc/adt/programs/programs/ZTEST",
		[]adt.TextSymbol{{Key: "001", Text: "x"}}, nil,
		"LOCK1", ""); err != nil {
		t.Fatalf("SetTextElements: %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("expected 1 PUT, got %d", len(recorded))
	}
	if strings.Contains(recorded[0].rawQuery, "corrNr") {
		t.Errorf("expected no corrNr in query, got %q", recorded[0].rawQuery)
	}
	if !strings.Contains(recorded[0].rawQuery, "lockHandle=LOCK1") {
		t.Errorf("expected lockHandle in query, got %q", recorded[0].rawQuery)
	}
}

// TestSetTextElements_OnlySymbols_OmitsSelectionsRequest verifies that
// when selections is nil, only one PUT (symbols) is emitted. Same in
// reverse for the symbols-nil case.
func TestSetTextElements_OnlySymbols_OmitsSelectionsRequest(t *testing.T) {
	var recorded []recordedRequest
	srv := newSetTextElementsServer(t, &recorded)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if err := client.SetTextElements(context.Background(),
		"/sap/bc/adt/programs/programs/ZTEST",
		[]adt.TextSymbol{{Key: "001", Text: "x"}}, nil, "LOCK", ""); err != nil {
		t.Fatalf("SetTextElements: %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("expected 1 PUT (symbols only), got %d", len(recorded))
	}
	if !strings.HasSuffix(recorded[0].path, "/source/symbols") {
		t.Errorf("expected only symbols PUT, got path %q", recorded[0].path)
	}
}

func TestSetTextElements_OnlySelections_OmitsSymbolsRequest(t *testing.T) {
	var recorded []recordedRequest
	srv := newSetTextElementsServer(t, &recorded)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if err := client.SetTextElements(context.Background(),
		"/sap/bc/adt/programs/programs/ZTEST",
		nil, []adt.SelectionText{{Name: "P_X", Text: "y"}}, "LOCK", ""); err != nil {
		t.Fatalf("SetTextElements: %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("expected 1 PUT (selections only), got %d", len(recorded))
	}
	if !strings.HasSuffix(recorded[0].path, "/source/selections") {
		t.Errorf("expected only selections PUT, got path %q", recorded[0].path)
	}
}
