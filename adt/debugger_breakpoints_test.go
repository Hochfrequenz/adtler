package adt_test

import (
	"context"
	"errors"
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
	testBreakpointsPath = "/sap/bc/adt/debugger/breakpoints"
	testBreakpointsSrc  = "/sap/bc/adt/programs/programs/ztest/source/main"
	testHeaderSession   = "X-sap-adt-sessiontype"
)

// breakpointRequest is one request the breakpoint mock received.
type breakpointRequest struct {
	method, escapedPath, rawQuery, body, sessionType string
}

// newBreakpointMock serves the breakpoint resource: POSTs and DELETEs are
// recorded and answered with status and respBody.
func newBreakpointMock(t *testing.T, status int, respBody string) (*adt.DebugSession, func() []breakpointRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []breakpointRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if !strings.HasPrefix(r.URL.Path, testBreakpointsPath) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, breakpointRequest{
			method:      r.Method,
			escapedPath: r.URL.EscapedPath(),
			rawQuery:    r.URL.RawQuery,
			body:        string(body),
			sessionType: r.Header.Get(testHeaderSession),
		})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(respBody))
	}))
	t.Cleanup(srv.Close)
	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	dbg := adt.NewDebugSession(adt.NewClient(cfg), "u", "ide1")
	return dbg, func() []breakpointRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]breakpointRequest(nil), got...)
	}
}

func bpResponse(entries ...string) string {
	return `<?xml version="1.0"?><dbg:breakpoints xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core">` +
		strings.Join(entries, "") + `</dbg:breakpoints>`
}

func TestSetBreakpoints_SeveralInOneRequest(t *testing.T) {
	dbg, requests := newBreakpointMock(t, http.StatusOK, bpResponse(
		`<breakpoint kind="line" clientId="0" id="BP14"/>`,
		`<breakpoint kind="line" clientId="1" id="BP15"/>`,
	))

	results, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeExternal, []adt.LineBreakpoint{
		{ObjectURI: testBreakpointsSrc, Line: 14},
		{ObjectURI: testBreakpointsSrc, Line: 15},
	})
	if err != nil {
		t.Fatalf("SetBreakpoints: %v", err)
	}
	if len(results) != 2 || results[0].ID != "BP14" || results[1].ID != "BP15" {
		t.Fatalf("results: %+v", results)
	}
	reqs := requests()
	if len(reqs) != 1 {
		t.Fatalf("requests: got %d, want 1", len(reqs))
	}
	body := reqs[0].body
	for _, want := range []string{
		`clientId="0" adtcore:uri="` + testBreakpointsSrc + `#start=14,0"`,
		`clientId="1" adtcore:uri="` + testBreakpointsSrc + `#start=15,0"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %s: %s", want, body)
		}
	}
	// adtcore:type/name are optional and omitted when empty.
	if strings.Contains(body, "adtcore:type") || strings.Contains(body, "adtcore:name") {
		t.Errorf("body carries empty type/name: %s", body)
	}
}

func TestSetBreakpoints_ScopeBodyAndHeader(t *testing.T) {
	tests := []struct {
		scope       adt.BreakpointScope
		wantSession string
	}{
		{adt.BreakpointScopeExternal, ""},
		{adt.BreakpointScopeDebugger, "stateful"},
	}
	for _, tt := range tests {
		t.Run(string(tt.scope), func(t *testing.T) {
			dbg, requests := newBreakpointMock(t, http.StatusOK, bpResponse(`<breakpoint kind="line" clientId="0" id="BP1"/>`))
			if _, err := dbg.SetBreakpoints(context.Background(), tt.scope, []adt.LineBreakpoint{
				{ObjectURI: testBreakpointsSrc, Line: 3, ObjectType: "PROG/P", ObjectName: "ZTEST"},
			}); err != nil {
				t.Fatalf("SetBreakpoints: %v", err)
			}
			req := requests()[0]
			if req.method != http.MethodPost {
				t.Errorf("method: got %s", req.method)
			}
			if req.sessionType != tt.wantSession {
				t.Errorf("%s: got %q, want %q", testHeaderSession, req.sessionType, tt.wantSession)
			}
			for _, want := range []string{
				`scope="` + string(tt.scope) + `"`,
				`debuggingMode="user"`,
				`requestUser="U"`,
				`ideId="ide1"`,
				`adtcore:type="PROG/P"`,
				`adtcore:name="ZTEST"`,
			} {
				if !strings.Contains(req.body, want) {
					t.Errorf("body missing %s: %s", want, req.body)
				}
			}
			// The transformation ignores a syncMode attribute (adtler#200).
			if strings.Contains(req.body, "syncMode") || strings.Contains(req.body, "syncScope") {
				t.Errorf("body carries a sync mode: %s", req.body)
			}
		})
	}
}

// TestSetBreakpoints_MatchesByClientID guards the matching rule: the resource
// re-sorts results and lists rejected breakpoints first, a result can carry an
// ID and an error, and a breakpoint without a result is not set.
func TestSetBreakpoints_MatchesByClientID(t *testing.T) {
	dbg, _ := newBreakpointMock(t, http.StatusOK, bpResponse(
		`<breakpoint kind="line" clientId="2" errorKind="invalidPosition" errorMessage="Cannot create a breakpoint at this position"/>`,
		`<breakpoint kind="line" clientId="1" id="BP_EXISTING" errorKind="existing"/>`,
		`<breakpoint kind="line" clientId="0" id="BP_OK"/>`,
		`<breakpoint kind="line" clientId="0" id="BP_DUPLICATE"/>`,
		`<breakpoint kind="line" clientId="99" id="BP_UNKNOWN"/>`,
		`<breakpoint kind="line" id="BP_NO_CLIENT_ID"/>`,
	))
	bps := make([]adt.LineBreakpoint, 4)
	for i := range bps {
		bps[i] = adt.LineBreakpoint{ObjectURI: testBreakpointsSrc, Line: 10 + i}
	}

	results, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeExternal, bps)
	if err != nil {
		t.Fatalf("SetBreakpoints: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("results: got %d, want 4", len(results))
	}
	if r := results[0]; r.ID != "BP_OK" || !r.IsSet() {
		t.Errorf("[0]: %+v, want BP_OK set", r)
	}
	if r := results[1]; r.ID != "BP_EXISTING" || r.ErrorKind != "existing" || r.IsSet() {
		t.Errorf("[1]: %+v, want ID with errorKind existing, not set", r)
	}
	if r := results[2]; r.ErrorKind != "invalidPosition" || r.ErrorMessage == "" || r.IsSet() {
		t.Errorf("[2]: %+v, want invalidPosition", r)
	}
	if r := results[3]; r.ID != "" || r.ErrorMessage == "" || r.IsSet() {
		t.Errorf("[3]: %+v, want not set (no result)", r)
	}
}

func TestSetBreakpoints_RejectsEmptyAndOversizedLists(t *testing.T) {
	dbg, requests := newBreakpointMock(t, http.StatusOK, bpResponse())
	ctx := context.Background()
	if _, err := dbg.SetBreakpoints(ctx, adt.BreakpointScopeExternal, nil); err == nil {
		t.Error("empty list: want error")
	}
	if _, err := dbg.SetBreakpoints(ctx, adt.BreakpointScopeExternal, make([]adt.LineBreakpoint, 31)); err == nil {
		t.Error("31 breakpoints: want error")
	}
	if n := len(requests()); n != 0 {
		t.Errorf("requests sent: %d, want 0", n)
	}
}

// noSessionAttachedBody is CL_TPDA_ADT_RES_BREAKPOINTS' answer to a
// scope=debugger request without an attached debugger: CX_ADT_REST_DATA_INVALID
// (400, ExceptionInvalidData) with subtype noSessionAttached, which the ADT
// framework transmits as the communicationFramework.subType property.
const noSessionAttachedBody = `<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
	`<namespace id="com.sap.adt"/><type id="ExceptionInvalidData"/>` +
	`<message>Invalid data</message>` +
	`<properties><entry key="com.sap.adt.communicationFramework.subType">noSessionAttached</entry></properties>` +
	`</exc:exception>`

func TestSetBreakpoints_NoSessionAttached(t *testing.T) {
	dbg, _ := newBreakpointMock(t, http.StatusBadRequest, noSessionAttachedBody)
	_, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeDebugger, []adt.LineBreakpoint{
		{ObjectURI: testBreakpointsSrc, Line: 3},
	})
	if !errors.Is(err, adt.ErrNoSessionAttached) {
		t.Fatalf("err: got %v, want ErrNoSessionAttached", err)
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.StatusCode != http.StatusBadRequest {
		t.Errorf("err does not wrap the 400 *ADTError: %v", err)
	}
}

func TestSetBreakpoints_OtherErrorIsNotNoSessionAttached(t *testing.T) {
	dbg, _ := newBreakpointMock(t, http.StatusBadRequest,
		strings.Replace(noSessionAttachedBody, "noSessionAttached", "notAuthorized", 1))
	_, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeDebugger, []adt.LineBreakpoint{
		{ObjectURI: testBreakpointsSrc, Line: 3},
	})
	if err == nil || errors.Is(err, adt.ErrNoSessionAttached) {
		t.Fatalf("err: got %v, want a non-ErrNoSessionAttached error", err)
	}
}

func TestRemoveBreakpoint(t *testing.T) {
	// A namespaced program name puts slashes into the ID; they must be escaped
	// exactly once so the whole ID stays one path segment.
	const id = "KIND=0.MAIN_PROGRAM=/ABC/ZTEST.LINE_NR=14 x"
	for _, tt := range []struct {
		scope       adt.BreakpointScope
		wantSession string
	}{
		{adt.BreakpointScopeExternal, ""},
		{adt.BreakpointScopeDebugger, "stateful"},
	} {
		t.Run(string(tt.scope), func(t *testing.T) {
			dbg, requests := newBreakpointMock(t, http.StatusOK, "")
			if err := dbg.RemoveBreakpoint(context.Background(), tt.scope, id); err != nil {
				t.Fatalf("RemoveBreakpoint: %v", err)
			}
			req := requests()[0]
			if req.method != http.MethodDelete {
				t.Errorf("method: got %s", req.method)
			}
			wantPath := testBreakpointsPath + "/KIND=0.MAIN_PROGRAM=%2FABC%2FZTEST.LINE_NR=14%20x"
			if req.escapedPath != wantPath {
				t.Errorf("path:\n got %s\nwant %s", req.escapedPath, wantPath)
			}
			wantQuery := "debuggingMode=user&ideId=ide1&requestUser=U&scope=" + string(tt.scope) + "&terminalId=MCP01"
			if req.rawQuery != wantQuery {
				t.Errorf("query:\n got %s\nwant %s", req.rawQuery, wantQuery)
			}
			if req.sessionType != tt.wantSession {
				t.Errorf("%s: got %q, want %q", testHeaderSession, req.sessionType, tt.wantSession)
			}
		})
	}
}

func TestRemoveBreakpoint_EmptyID(t *testing.T) {
	dbg, requests := newBreakpointMock(t, http.StatusOK, "")
	if err := dbg.RemoveBreakpoint(context.Background(), adt.BreakpointScopeExternal, ""); err == nil {
		t.Error("want error for empty ID")
	}
	if n := len(requests()); n != 0 {
		t.Errorf("requests sent: %d, want 0", n)
	}
}

func TestRemoveBreakpoint_NoSessionAttached(t *testing.T) {
	dbg, _ := newBreakpointMock(t, http.StatusBadRequest, noSessionAttachedBody)
	err := dbg.RemoveBreakpoint(context.Background(), adt.BreakpointScopeDebugger, "BP1")
	if !errors.Is(err, adt.ErrNoSessionAttached) {
		t.Fatalf("err: got %v, want ErrNoSessionAttached", err)
	}
}
