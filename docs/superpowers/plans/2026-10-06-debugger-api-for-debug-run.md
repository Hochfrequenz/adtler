# Debugger API for `debug_run` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give adtler's `DebugSession` everything aibap.mcp's `debug_run`/`debug_wait` design needs: correct multi-breakpoint handling with scopes and removal (#200), structured variables (#201), parsed stack frames (#203), and a unit-test trigger bound to the debug session's system (#204).

**Architecture:** All additions are methods on `*DebugSession` in package `adt`, next to the existing debugger code. XML wire types live in `adt/adtxml/debugger.go` (breakpoints, stack) and a new `adt/adtxml/debugger_vars.go` (variables). Each issue ships as its own PR; tasks are grouped by PR.

**Tech Stack:** Go 1.26/1.27, `encoding/xml`, `net/http/httptest` for unit tests, live integration tests behind `//go:build integration`.

**Spec:** aibap.mcp `docs/superpowers/specs/2026-10-06-debug-run-design.md` (branch `docs/558-debug-run-spec`); the evidence is in adtler #200, #201 and aibap.mcp #558 (2026-10-06 comment).

## Global Constraints

- One PR per issue: PR-1 = #204, PR-2 = #203, PR-3 = #200, PR-4 = #201. Branches `feat/<issue>-…` from `origin/main`.
- Existing public API keeps compiling: `SetBreakpoint`, `GetStack`, `GetVariable`, `Step`, `StartListener`, `StopListener` keep their signatures.
- Every request on the stateful debug session that reads or changes the attached debuggee carries `X-sap-adt-sessiontype: stateful`.
- No raw string literals with tabs for ABAP source in tests (use `"…\n" +` concatenation).
- Integration tests: `//go:build integration`, run with `SAP_INTEGRATION_SYSTEM=none SAP_INTEGRATION_SYSTEMS=<r3-key>,<s4-key>` so `TestMain` does not create transports; use `eachSystem(t)`; every throwaway object in `$TMP` is deleted in `t.Cleanup` **without** a lock (#187); every breakpoint set is removed with `RemoveBreakpoint` in `t.Cleanup` (an empty `syncMode="full"` removes nothing on SAP_BASIS 750).
- Windows: when passing ADT URIs as command-line arguments to throwaway programs, `export MSYS_NO_PATHCONV=1` (Git Bash rewrites `/sap/…`).
- `gofmt`, `go vet ./...`, `go test ./...`, `go test -race ./adt/...` (WSL if no cgo), `golangci-lint run` before every commit. Keep LF line endings.
- Public repo: no hostnames, system aliases in prose, user names, SIDs, server names; name systems by type and SAP_BASIS release.

## Review Focus

- **SAP returns results in a different order than requested, or omits one** → `SetBreakpoints` must match by `clientId`, mark unmatched ones as not set, and never shift results onto the wrong breakpoint. (Test in Task 3.)
- **A breakpoint ID with characters that need escaping** (`=`, `.`, `/` from class includes like `CL_X=====CP`) → `RemoveBreakpoint` escapes exactly once. (Test in Task 3.)
- **Variable IDs with `->`, `[`, `]`, `{`, `\`, `*`** → XML bodies built by `encoding/xml`, query parameters by `url.QueryEscape`; never string concatenation. (Tests in Tasks 6 and 8.)
- **Table paging out of range** (`offset < 1`, `offset > lines`, `limit` past the end) → no SAP call for invalid/empty pages, clamped `length` otherwise (SAP_BASIS 750 fails on overrun). (Test in Task 7.)
- **A stack response with no `isActive` frame (SAP_BASIS 750) or missing `adtcore:uri`** → `GetStackFrames` still returns the frames; `ActiveFrame` falls back to the highest `stackPosition`; `SourceURI`/`SourceLine` stay empty/0. (Test in Task 2.)

---

## PR-1 — A5: `DebugSession.RunUnitTests`

### Task 1: Unit-test trigger on an isolated session of the debug session's system

**Files:**
- Modify: `adt/debugger.go` (add method after `StopListener`)
- Test: `adt/debugger_rununittests_internal_test.go` (new, package `adt`)

**Interfaces:**
- Consumes: `(*httpClient).freshSession() *httpClient`, `(*httpClient).RunUnitTests(ctx, objectURI string, timeoutSeconds int) (*TestResult, error)` (existing).
- Produces: `func (d *DebugSession) RunUnitTests(ctx context.Context, objectURI string, timeoutSeconds int) (*TestResult, error)`.

- [ ] **Step 1: Write the failing test**

```go
package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const minimalRunResultXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"><program><testClasses><testClass name="LCL_TEST">` +
	`<testMethods><testMethod name="TEST_HELLO" executionTime="0.1"/></testMethods></testClass></testClasses></program></aunit:runResult>`

// RunUnitTests on a DebugSession must use a NEW isolated session of the debug
// session's own system: not the debug session's stateful cookie jar, and not
// whatever system a ClientRegistry has active at call time.
func TestDebugSessionRunUnitTests_IsolatedAndBoundToSystem(t *testing.T) {
	var mu sync.Mutex
	var hitsA, hitsB int
	var sawStateful, sawDebugCookie bool
	newSrv := func(hits *int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == discoveryPath {
				http.SetCookie(w, &http.Cookie{Name: "SAP_SESSIONID_X", Value: "s-" + r.Host})
				w.Header().Set("X-CSRF-Token", "token")
				return
			}
			if r.URL.Path == "/sap/bc/adt/abapunit/testruns" {
				mu.Lock()
				*hits++
				if r.Header.Get("X-sap-adt-sessiontype") == "stateful" {
					sawStateful = true
				}
				if c, err := r.Cookie("debugsession"); err == nil && c.Value != "" {
					sawDebugCookie = true
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(minimalRunResultXML))
				return
			}
			// Any debugger call on the debug session sets a marker cookie in ITS jar.
			http.SetCookie(w, &http.Cookie{Name: "debugsession", Value: "1"})
			w.WriteHeader(http.StatusOK)
		}))
	}
	srvA, srvB := newSrv(&hitsA), newSrv(&hitsB)
	defer srvA.Close()
	defer srvB.Close()

	reg, err := NewClientRegistry(map[string]Client{
		"sysA": NewClient(sapmcpconfig.SAPSystem{Host: srvA.URL, User: "U", Password: "P", Client: "100"}),
		"sysB": NewClient(sapmcpconfig.SAPSystem{Host: srvB.URL, User: "U", Password: "P", Client: "100"}),
	}, "sysA")
	if err != nil {
		t.Fatal(err)
	}
	dbg := NewDebugSession(reg, "U")
	// Put a cookie into the debug session's own jar.
	if _, err := dbg.GetStack(context.Background()); err != nil {
		t.Fatalf("GetStack: %v", err)
	}
	if _, err := reg.Select("sysB"); err != nil {
		t.Fatal(err)
	}

	res, err := dbg.RunUnitTests(context.Background(), "/sap/bc/adt/programs/programs/ztest", 60)
	if err != nil {
		t.Fatalf("RunUnitTests: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	mu.Lock()
	defer mu.Unlock()
	if hitsA != 1 || hitsB != 0 {
		t.Errorf("test run went to sysA=%d sysB=%d, want 1/0 (bound to the debug session's system)", hitsA, hitsB)
	}
	if sawStateful {
		t.Error("unit-test request must not carry X-sap-adt-sessiontype: stateful")
	}
	if sawDebugCookie {
		t.Error("unit-test request must not reuse the debug session's cookie jar")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./adt/ -run TestDebugSessionRunUnitTests_IsolatedAndBoundToSystem -v`
Expected: FAIL to compile with `dbg.RunUnitTests undefined`.

- [ ] **Step 3: Write minimal implementation** (in `adt/debugger.go`)

```go
// RunUnitTests runs the ABAP Unit tests of objectURI as a breakpoint trigger for
// this debug session. It uses a NEW isolated session (freshSession) of the same
// system and credentials as d — never d's own stateful session, and never the
// parent client, so it cannot wedge behind a stateful lock session and is not
// redirected by a later ClientRegistry.Select. timeoutSeconds bounds the whole
// run, including the time the debuggee is halted at a breakpoint.
func (d *DebugSession) RunUnitTests(ctx context.Context, objectURI string, timeoutSeconds int) (*TestResult, error) {
	return d.client.freshSession().RunUnitTests(ctx, objectURI, timeoutSeconds)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./adt/ -run TestDebugSessionRunUnitTests_IsolatedAndBoundToSystem -v`
Expected: PASS. If the parse of `minimalRunResultXML` fails, copy the smallest passing fixture from `adt/unittest_test.go` instead.

- [ ] **Step 5: Integration test** — add to `adt/unittest_long_run_integration_test.go` a test `TestDebugSessionRunUnitTests_Catches_MultiSystem_Integration`: for each system, `createReportWithTestClass`, `NewDebugSession`, `SetBreakpoint(line 14)`, `StartListener` in a goroutine, sleep 4 s, `dbg.RunUnitTests(ctx, uri, 120)` in a goroutine, assert the listener returns `attached`, `Attach`, `Step(ctx, "detachDebugger")`, assert the run result has 1 passed test; cleanup `StopListener` and remove the breakpoint (`SetBreakpoint` result ID via `RemoveBreakpoint` once PR-3 is merged; until then `StopListener` only).

- [ ] **Step 6: Run full checks and commit**

```bash
gofmt -l . ; go vet ./... && go test ./... && golangci-lint run
git add adt/debugger.go adt/debugger_rununittests_internal_test.go adt/unittest_long_run_integration_test.go
git commit -m "feat(#204): DebugSession.RunUnitTests on an isolated session of the debug session's system"
```

---

## PR-2 — A4: parsed stack frames

### Task 2: `GetStackFrames`

**Files:**
- Modify: `adt/adtxml/debugger.go` (add stack types)
- Modify: `adt/debugger.go` (add `StackFrame`, `GetStackFrames`, `ActiveFrame`)
- Test: `adt/debugger_stackframes_test.go` (new, package `adt_test`)

**Interfaces:**
- Produces:
  - `type StackFrame struct { Position int; Program, Include string; Line int; EventType, EventName, SourceURI string; SourceLine int; SystemProgram, Active bool }`
  - `func (d *DebugSession) GetStackFrames(ctx context.Context) ([]StackFrame, error)`
  - `func ActiveFrame(frames []StackFrame) (StackFrame, bool)` — the frame with `Active`, else the one with the highest `Position`; `false` for an empty slice.

- [ ] **Step 1: Write the failing tests**

```go
package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// Anonymised shapes of live responses: 816 lists stackPosition etc. first and
// carries debugCursorStackIndex; 750 lists programName first.
const stack816 = `<?xml version="1.0" encoding="utf-8"?><dbg:stack isRfc="false" debugCursorStackIndex="0" isSameSystem="true" xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<stackEntry stackPosition="1" stackType="ABAP" programName="SAPLAUNIT" includeName="LAUNITU01" line="10" eventType="FUNCTION" eventName="RUN" sourceType="ABAP" systemProgram="true" isActive="false" adtcore:uri="/sap/bc/adt/functions/groups/aunit/fmodules/run/source/main#start=10,0"/>` +
	`<stackEntry stackPosition="2" stackType="ABAP" programName="ZREP" includeName="ZREP" line="14" eventType="METHOD" eventName="TEST_HELLO" sourceType="ABAP" systemProgram="false" isActive="true" adtcore:uri="/sap/bc/adt/programs/programs/zrep/source/main#start=14,0"/>` +
	`</dbg:stack>`

const stack750 = `<?xml version="1.0" encoding="utf-8"?><dbg:stack isRfc="true" isSameSystem="true" xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core">` +
	`<stackEntry programName="SAPLSTFC" includeName="LSTFCU01" line="13" eventType="FUNCTION" eventName="STFC_CONNECTION" stackPosition="9" systemProgram="false" adtcore:uri="/sap/bc/adt/functions/groups/stfc/fmodules/stfc_connection/source/main#start=13,0"/>` +
	`<stackEntry programName="SAPMHTTP" includeName="SAPMHTTP" line="5" eventType="MODULE" eventName="X" stackPosition="1" systemProgram="true"/>` +
	`</dbg:stack>`

func stackServer(t *testing.T, body string) *adt.DebugSession {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		if r.URL.Query().Get("method") == "getStack" {
			if r.Header.Get("X-sap-adt-sessiontype") != "stateful" {
				t.Error("getStack must be stateful")
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U")
}

func TestGetStackFrames_816(t *testing.T) {
	frames, err := stackServer(t, stack816).GetStackFrames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatalf("got %d frames", len(frames))
	}
	f, ok := adt.ActiveFrame(frames)
	if !ok || f.Program != "ZREP" || f.Line != 14 || f.EventName != "TEST_HELLO" ||
		f.SourceURI != "/sap/bc/adt/programs/programs/zrep/source/main" || f.SourceLine != 14 || f.SystemProgram {
		t.Errorf("active frame: %+v", f)
	}
}

// 750 has no isActive: the top of the stack (highest stackPosition) is active.
// A frame without adtcore:uri keeps an empty SourceURI.
func TestGetStackFrames_750_NoIsActive(t *testing.T) {
	frames, err := stackServer(t, stack750).GetStackFrames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f, ok := adt.ActiveFrame(frames)
	if !ok || f.Position != 9 || f.Include != "LSTFCU01" || f.Line != 13 ||
		f.SourceURI != "/sap/bc/adt/functions/groups/stfc/fmodules/stfc_connection/source/main" {
		t.Errorf("active frame: %+v", f)
	}
	if frames[1].SourceURI != "" || frames[1].SourceLine != 0 || !frames[1].SystemProgram {
		t.Errorf("second frame: %+v", frames[1])
	}
	if _, ok := adt.ActiveFrame(nil); ok {
		t.Error("ActiveFrame(nil) must report false")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./adt/ -run TestGetStackFrames -v`
Expected: FAIL to compile (`GetStackFrames`, `ActiveFrame` undefined).

- [ ] **Step 3: Implement** — `adt/adtxml/debugger.go`:

```go
// StackResponse is the response of POST /sap/bc/adt/debugger?method=getStack.
type StackResponse struct {
	XMLName xml.Name     `xml:"stack"`
	Entries []StackEntry `xml:"stackEntry"`
}

// StackEntry is one frame. Attribute order differs between releases; isActive
// is absent on SAP_BASIS 750.
type StackEntry struct {
	Position      int    `xml:"stackPosition,attr"`
	Program       string `xml:"programName,attr"`
	Include       string `xml:"includeName,attr"`
	Line          int    `xml:"line,attr"`
	EventType     string `xml:"eventType,attr"`
	EventName     string `xml:"eventName,attr"`
	SystemProgram bool   `xml:"systemProgram,attr"`
	IsActive      bool   `xml:"isActive,attr"`
	URI           string `xml:"uri,attr"` // adtcore:uri
}
```

`adt/debugger.go` (add `strconv` to the imports):

```go
// StackFrame is one parsed debugger stack frame.
type StackFrame struct {
	Position      int
	Program       string
	Include       string
	Line          int
	EventType     string
	EventName     string
	SourceURI     string // adtcore:uri without the #start fragment; empty when SAP sends none
	SourceLine    int    // line from the #start=<line>,<col> fragment; 0 when absent
	SystemProgram bool
	Active        bool
}

// GetStackFrames returns the parsed call stack (see GetStack for the raw XML).
func (d *DebugSession) GetStackFrames(ctx context.Context) ([]StackFrame, error) {
	data, err := d.GetStack(ctx)
	if err != nil {
		return nil, err
	}
	var resp adtxml.StackResponse
	if err := xml.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("GetStackFrames unmarshal: %w", err)
	}
	frames := make([]StackFrame, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		uri, srcLine := e.URI, 0
		if i := strings.IndexByte(uri, '#'); i >= 0 {
			frag := uri[i+1:]
			uri = uri[:i]
			if rest, ok := strings.CutPrefix(frag, "start="); ok {
				if j := strings.IndexAny(rest, ",;"); j >= 0 {
					rest = rest[:j]
				}
				srcLine, _ = strconv.Atoi(rest)
			}
		}
		frames = append(frames, StackFrame{
			Position: e.Position, Program: e.Program, Include: e.Include, Line: e.Line,
			EventType: e.EventType, EventName: e.EventName, SourceURI: uri, SourceLine: srcLine,
			SystemProgram: e.SystemProgram, Active: e.IsActive,
		})
	}
	return frames, nil
}

// ActiveFrame returns the frame the debuggee stands in: the one marked active,
// or — when SAP marks none (SAP_BASIS 750) — the top of the stack, i.e. the
// highest Position.
func ActiveFrame(frames []StackFrame) (StackFrame, bool) {
	if len(frames) == 0 {
		return StackFrame{}, false
	}
	top := frames[0]
	for _, f := range frames {
		if f.Active {
			return f, true
		}
		if f.Position > top.Position {
			top = f
		}
	}
	return top, true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./adt/ -run TestGetStackFrames -v` → PASS.

- [ ] **Step 5: Integration test** — in `adt/debugger_integration_test.go` (or a new `adt/debugger_stackframes_integration_test.go`), per system: report with test class, breakpoint line 14, listener, unit-test trigger, attach, `GetStackFrames`, assert `ActiveFrame` has `Line == 14`, `SourceLine == 14`, `Program == <report>`, and the source read with `client.GetSource(ctx, strings.TrimSuffix(frame.SourceURI, "/source/main"))` has the breakpoint statement at line `SourceLine`. Repeat with a breakpoint in a global class method (a throwaway `$TMP` class whose method is called by its local test class), to confirm `SourceLine` points into the class's `source/main` and that on 750 the highest `stackPosition` is the current frame; `detachDebugger`; cleanup.

- [ ] **Step 6: Full checks and commit**

```bash
gofmt -l . ; go vet ./... && go test ./... && golangci-lint run
git add adt/adtxml/debugger.go adt/debugger.go adt/debugger_stackframes_test.go adt/*stackframes*integration_test.go
git commit -m "feat(#203): parsed debugger stack frames (GetStackFrames, ActiveFrame)"
```

---

## PR-3 — #200: breakpoints

### Task 3: `SetBreakpoints` and `RemoveBreakpoint` with scopes

**Files:**
- Modify: `adt/adtxml/debugger.go` (`BreakpointRequest.ClientID`, `BreakpointResponse.ClientID/ErrorKind`)
- Modify: `adt/debugger.go`
- Test: `adt/debugger_breakpoints_test.go` (new, package `adt_test`)

**Interfaces:**
- Produces:
  - `type BreakpointScope string`; `const BreakpointScopeExternal BreakpointScope = "external"`, `BreakpointScopeDebugger BreakpointScope = "debugger"`
  - `type LineBreakpoint struct { ObjectURI string; Line int; ObjectType, ObjectName string }`
  - `BreakpointResult` gains `ErrorKind string` and method `func (r BreakpointResult) IsSet() bool`
  - `func (d *DebugSession) SetBreakpoints(ctx context.Context, scope BreakpointScope, bps []LineBreakpoint) ([]BreakpointResult, error)` — results in **request order**
  - `func (d *DebugSession) RemoveBreakpoint(ctx context.Context, scope BreakpointScope, id string) error`
  - `var ErrNoDebuggerAttached = errors.New("no debugger attached in this session")` — returned (wrapped) for `scope=debugger` when SAP answers with the `no_Session_Attached` subtype

- [ ] **Step 1: Write the failing tests**

```go
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

type bpRecorder struct {
	mu       sync.Mutex
	bodies   []string
	stateful []bool
	deletes  []string // RawPath+"?"+RawQuery of DELETEs
	answer   string   // response body for POST /breakpoints
	status   int      // response status for POST /breakpoints (0 = 200)
}

func (rec *bpRecorder) session(t *testing.T) *adt.DebugSession {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/sap/bc/adt/debugger/breakpoints") {
			rec.mu.Lock()
			defer rec.mu.Unlock()
			if r.Method == http.MethodDelete {
				p := r.URL.RawPath
				if p == "" {
					p = r.URL.Path
				}
				rec.deletes = append(rec.deletes, p+"?"+r.URL.RawQuery)
				return
			}
			b, _ := io.ReadAll(r.Body)
			rec.bodies = append(rec.bodies, string(b))
			rec.stateful = append(rec.stateful, r.Header.Get("X-sap-adt-sessiontype") == "stateful")
			if rec.status != 0 {
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(rec.status)
			}
			_, _ = w.Write([]byte(rec.answer))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U", "IDE1")
}

const bpNS = `xmlns:dbg="http://www.sap.com/adt/debugger" xmlns:adtcore="http://www.sap.com/adt/core"`

// SAP lists rejected breakpoints first and may omit some: results must be
// matched by clientId and returned in request order.
func TestSetBreakpoints_MatchesByClientIDInRequestOrder(t *testing.T) {
	rec := &bpRecorder{answer: `<?xml version="1.0"?><dbg:breakpoints ` + bpNS + `>` +
		`<breakpoint kind="line" clientId="bp2" errorKind="invalidPosition" errorMessage="Cannot create a breakpoint at this position"/>` +
		`<breakpoint kind="line" clientId="bp0" id="ID-A"/>` +
		`<breakpoint kind="line" clientId="bp3" errorKind="existing" errorMessage="Breakpoint already exists"/>` +
		`</dbg:breakpoints>`}
	dbg := rec.session(t)
	u := "/sap/bc/adt/programs/programs/zrep/source/main"
	res, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeExternal, []adt.LineBreakpoint{
		{ObjectURI: u, Line: 14}, {ObjectURI: u, Line: 15}, {ObjectURI: u, Line: 99}, {ObjectURI: u, Line: 16},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 {
		t.Fatalf("got %d results", len(res))
	}
	if res[0].ID != "ID-A" || !res[0].IsSet() {
		t.Errorf("bp0: %+v", res[0])
	}
	if res[1].IsSet() || res[1].ErrorKind != "notReturned" {
		t.Errorf("bp1 (omitted by SAP) must be not set: %+v", res[1])
	}
	if res[2].IsSet() || res[2].ErrorKind != "invalidPosition" {
		t.Errorf("bp2: %+v", res[2])
	}
	if !res[3].IsSet() || res[3].ErrorKind != "existing" {
		t.Errorf("bp3 (existing counts as set): %+v", res[3])
	}
	body := rec.bodies[0]
	for _, want := range []string{`scope="external"`, `syncMode="full"`, `ideId="IDE1"`, `clientId="bp0"`, `clientId="bp3"`, `#start=99,0`} {
		if !strings.Contains(body, want) {
			t.Errorf("request body missing %s: %s", want, body)
		}
	}
	if strings.Count(body, "<breakpoint ") != 4 {
		t.Errorf("all breakpoints must go in ONE request: %s", body)
	}
	if rec.stateful[0] {
		t.Error("external scope must not be sent stateful")
	}
}

func TestSetBreakpoints_DebuggerScopeIsStatefulAndAdds(t *testing.T) {
	rec := &bpRecorder{answer: `<dbg:breakpoints ` + bpNS + `><breakpoint kind="line" clientId="bp0" id="ID-D"/></dbg:breakpoints>`}
	dbg := rec.session(t)
	if _, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeDebugger,
		[]adt.LineBreakpoint{{ObjectURI: "/sap/bc/adt/programs/programs/zrep/source/main", Line: 15}}); err != nil {
		t.Fatal(err)
	}
	if !rec.stateful[0] {
		t.Error("debugger scope must carry X-sap-adt-sessiontype: stateful")
	}
	if !strings.Contains(rec.bodies[0], `scope="debugger"`) || strings.Contains(rec.bodies[0], "syncMode") {
		t.Errorf("debugger scope must add (no syncMode): %s", rec.bodies[0])
	}
}

func TestSetBreakpoints_NoSessionAttached(t *testing.T) {
	rec := &bpRecorder{status: http.StatusInternalServerError, answer: `<?xml version="1.0" encoding="utf-8"?>` +
		`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><namespace id="com.sap.adt"/><type id="AdiFailed"/>` +
		`<message lang="EN">no session attached</message><properties>` +
		`<entry key="com.sap.adt.communicationFramework.subType">no_Session_Attached</entry></properties></exc:exception>`}
	dbg := rec.session(t)
	_, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeDebugger,
		[]adt.LineBreakpoint{{ObjectURI: "/sap/bc/adt/programs/programs/zrep/source/main", Line: 15}})
	if !errors.Is(err, adt.ErrNoDebuggerAttached) {
		t.Fatalf("want ErrNoDebuggerAttached, got %v", err)
	}
}

func TestRemoveBreakpoint_EscapesIDOnceAndSendsKeys(t *testing.T) {
	rec := &bpRecorder{}
	dbg := rec.session(t)
	id := "KIND=0.SOURCETYPE=ABAP.MAIN_PROGRAM=CL_X==========CP.INCLUDE=CL_X==========CM001.LINE_NR=5"
	if err := dbg.RemoveBreakpoint(context.Background(), adt.BreakpointScopeExternal, id); err != nil {
		t.Fatal(err)
	}
	got := rec.deletes[0]
	if !strings.Contains(got, "/sap/bc/adt/debugger/breakpoints/KIND%3D0.SOURCETYPE%3DABAP") {
		t.Errorf("ID must be path-escaped exactly once: %s", got)
	}
	for _, want := range []string{"debuggingMode=user", "requestUser=U", "terminalId=MCP01", "ideId=IDE1", "scope=external"} {
		if !strings.Contains(got, want) {
			t.Errorf("DELETE missing %s: %s", want, got)
		}
	}
}
```

Note: if `url.PathEscape` leaves `=` unescaped (it does — `=` is allowed in paths), adjust the expected substring to the actual single-escaped form produced by `url.PathEscape(id)` and assert that no `%25` (double escape) occurs. Keep the "exactly once" assertion via `!strings.Contains(got, "%25")`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./adt/ -run 'TestSetBreakpoints_|TestRemoveBreakpoint_' -v`
Expected: FAIL to compile.

- [ ] **Step 3: Implement** — `adt/adtxml/debugger.go`: add `ClientID string` to `BreakpointRequest` and write it in `MarshalXML` when non-empty (`{Name: xml.Name{Local: "clientId"}, Value: b.ClientID}` appended after `kind`); add to `BreakpointResponse`:

```go
	ClientID  string `xml:"clientId,attr"`
	ErrorKind string `xml:"errorKind,attr"`
```

`adt/debugger.go`:

```go
// BreakpointScope selects which breakpoint set a request works on.
type BreakpointScope string

const (
	// BreakpointScopeExternal: user breakpoints matched by a waiting listener.
	// Set them before a run. With several requests the server REPLACES the set
	// on SAP_BASIS 816 and ADDS to it on 750 (#200): send the full list in one
	// SetBreakpoints call.
	BreakpointScopeExternal BreakpointScope = "external"
	// BreakpointScopeDebugger: breakpoints of the debugger attached in THIS
	// session; sent stateful, added (no syncMode). Use while attached — an
	// external-scope request while attached detaches the debugger (#200).
	BreakpointScopeDebugger BreakpointScope = "debugger"
)

// ErrNoDebuggerAttached is returned (wrapped) when a debugger-scope request
// reaches a session with no attached debugger.
var ErrNoDebuggerAttached = errors.New("no debugger attached in this session")

// LineBreakpoint is one line breakpoint to set.
type LineBreakpoint struct {
	ObjectURI  string // source URI, e.g. …/source/main
	Line       int
	ObjectType string // optional adtcore:type, e.g. PROG/P
	ObjectName string // optional adtcore:name
}
```

Extend `BreakpointResult`:

```go
// BreakpointResult holds the outcome for one requested breakpoint.
type BreakpointResult struct {
	ID           string
	ErrorKind    string // SAP errorKind (existing, invalidPosition, tooManyBreakpoints, …) or "notReturned"
	ErrorMessage string
}

// IsSet reports whether the breakpoint is active on the server. "existing"
// (already set) counts as set.
func (r BreakpointResult) IsSet() bool {
	if r.ErrorKind == "existing" {
		return true
	}
	return r.ErrorKind == "" && r.ErrorMessage == "" && r.ID != ""
}
```

```go
// SetBreakpoints sets all bps in ONE request and returns one result per
// requested breakpoint, in request order (matched by clientId; SAP reorders).
// Breakpoints are keyed by the logged-on user, requestUser and ideId.
func (d *DebugSession) SetBreakpoints(ctx context.Context, scope BreakpointScope, bps []LineBreakpoint) ([]BreakpointResult, error) {
	req := adtxml.BreakpointsRequest{
		NSDebug: "http://www.sap.com/adt/debugger", NSCore: nsADTCore,
		Scope: string(scope), DebuggingMode: "user",
		RequestUser: d.user, TerminalID: d.terminalID, IdeID: d.ideID,
	}
	if scope == BreakpointScopeExternal {
		req.SyncMode = "full"
	}
	for i, bp := range bps {
		req.Breakpoints = append(req.Breakpoints, adtxml.BreakpointRequest{
			Kind: "line", ClientID: fmt.Sprintf("bp%d", i),
			URI:  fmt.Sprintf("%s#start=%d,0", bp.ObjectURI, bp.Line),
			Type: bp.ObjectType, Name: bp.ObjectName,
		})
	}
	bodyXML, err := xml.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("SetBreakpoints marshal: %w", err)
	}
	headers := map[string]string{"Content-Type": contentTypeXML, "Accept": "application/xml"}
	if scope == BreakpointScopeDebugger {
		headers["X-sap-adt-sessiontype"] = "stateful"
	}
	resp, err := d.client.doMutate(ctx, http.MethodPost, "/sap/bc/adt/debugger/breakpoints",
		strings.NewReader(xml.Header+string(bodyXML)), headers)
	if err != nil {
		return nil, fmt.Errorf("SetBreakpoints: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, wrapNoDebuggerAttached(err)
	}
	data, _ := io.ReadAll(resp.Body)
	var bpResp adtxml.BreakpointsResponse
	if err := xml.Unmarshal(data, &bpResp); err != nil {
		return nil, fmt.Errorf("SetBreakpoints unmarshal: %w", err)
	}
	byClient := make(map[string]adtxml.BreakpointResponse, len(bpResp.Breakpoints))
	for _, b := range bpResp.Breakpoints {
		byClient[b.ClientID] = b
	}
	out := make([]BreakpointResult, len(bps))
	for i := range bps {
		b, ok := byClient[fmt.Sprintf("bp%d", i)]
		if !ok {
			out[i] = BreakpointResult{ErrorKind: "notReturned", ErrorMessage: "SAP returned no result for this breakpoint"}
			continue
		}
		out[i] = BreakpointResult{ID: b.ID, ErrorKind: b.ErrorKind, ErrorMessage: b.ErrorMessage}
	}
	return out, nil
}

// RemoveBreakpoint deletes one breakpoint by the ID SAP returned.
func (d *DebugSession) RemoveBreakpoint(ctx context.Context, scope BreakpointScope, id string) error {
	q := url.Values{}
	q.Set("debuggingMode", "user")
	q.Set("requestUser", d.user)
	q.Set("terminalId", d.terminalID)
	q.Set("ideId", d.ideID)
	q.Set("scope", string(scope))
	path := "/sap/bc/adt/debugger/breakpoints/" + url.PathEscape(id) + "?" + q.Encode()
	var headers map[string]string
	if scope == BreakpointScopeDebugger {
		headers = map[string]string{"X-sap-adt-sessiontype": "stateful"}
	}
	resp, err := d.client.doMutate(ctx, http.MethodDelete, path, nil, headers)
	if err != nil {
		return fmt.Errorf("RemoveBreakpoint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return wrapNoDebuggerAttached(checkResponse(resp))
}

func wrapNoDebuggerAttached(err error) error {
	var ae *ADTError
	if errors.As(err, &ae) && strings.EqualFold(
		strings.ReplaceAll(ae.Properties["com.sap.adt.communicationFramework.subType"], "_", ""), "nosessionattached") {
		return fmt.Errorf("%w: %v", ErrNoDebuggerAttached, err)
	}
	return err
}
```

Before finishing, confirm the `ADTError` field holding properties is named `Properties` and keyed exactly as above (see `adt/types.go`); if the key constant already exists, use it. Confirm `doMutate` builds the request URL from `path` without re-escaping (`url.Parse` keeps `%XX`); if it re-escapes, build the URL with `RawPath` accordingly.

Rewrite `SetBreakpoint` as a wrapper:

```go
// SetBreakpoint sets one external line breakpoint. Each call REPLACES all
// external breakpoints of this user/IDE ID on SAP_BASIS 816 and ADDS on 750
// (#200); to set several, use SetBreakpoints with the full list.
func (d *DebugSession) SetBreakpoint(ctx context.Context, objectURI string, line int, objectType, objectName string) (*BreakpointResult, error) {
	res, err := d.SetBreakpoints(ctx, BreakpointScopeExternal,
		[]LineBreakpoint{{ObjectURI: objectURI, Line: line, ObjectType: objectType, ObjectName: objectName}})
	if err != nil {
		return nil, fmt.Errorf("SetBreakpoint: %w", err)
	}
	r := res[0]
	return &r, nil
}
```

Run the existing `TestDebugSessionSetBreakpoint` too: its fake response has no `clientId` — update that fixture to include `clientId="bp0"` (SAP echoes it).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./adt/ -run 'Breakpoint' -v` → PASS (including the updated `TestDebugSessionSetBreakpoint`).

- [ ] **Step 5: Commit**

```bash
gofmt -l . ; go vet ./... && go test ./... && golangci-lint run
git add adt/adtxml/debugger.go adt/debugger.go adt/debugger_breakpoints_test.go adt/debugger_test.go
git commit -m "feat(#200): SetBreakpoints with scopes and clientId matching, RemoveBreakpoint"
```

### Task 4: Breakpoint integration tests on both releases

**Files:**
- Create: `adt/debugger_breakpoints_integration_test.go` (`//go:build integration`, package `adt_test`)
- Modify: `adt/unittest_long_run_integration_test.go` — extract `createReportWithSource(t, client, name, uri, source string)` from `createReportWithTestClass`, which then calls it with its current source.

**Interfaces:**
- Consumes: Task 3 API, `eachSystem`, `createReportWithTestClass`.

- [ ] **Step 1: Write the integration tests** — per system (`for _, sys := range eachSystem(t)` with `t.Run(sys.Name, …)`), with a throwaway report `Z_ADT_200_<6 random digits>` from `createReportWithTestClass` (lines 14 and 15 are the two statements of the test method):

1. `TestSetBreakpoints_TwoInOneRequest_StopsAtBoth_Integration`:
   `SetBreakpoints(External, [{uri,15},{uri,14}])` → both `IsSet`; register cleanup `RemoveBreakpoint(External, id)` for both; listener goroutine; sleep 4 s; `client.RunUnitTests` goroutine; listener `attached`; `Attach`; `ActiveFrame(GetStackFrames)` line 14 (if PR-2 not merged yet, assert `strings.Contains(stack, `line="14"`)`); `Step("stepContinue")` returns no error; frame line 15; `Step("detachDebugger")`; unit-test result arrives.
2. `TestSetBreakpoints_DebuggerScopeWhileHalted_Integration`:
   external `[{uri,14}]`; trigger; attach; `SetBreakpoints(Debugger, [{uri,15}])` → set; `Step("stepContinue")` → frame line 15; `detachDebugger`.
3. `TestRemoveBreakpoint_NextRunNotCaught_Integration`:
   external `[{uri,14}]`; `RemoveBreakpoint`; listener with 15 s; trigger; listener status `timeout`.

- [ ] **Step 2: Run them live**

Run: `SAP_INTEGRATION_SYSTEM=none SAP_INTEGRATION_SYSTEMS=<r3-key>,<s4-key> go test -tags integration -run 'Integration' -run 'TestSetBreakpoints_|TestRemoveBreakpoint_' -count=1 -v ./adt/`
Expected: PASS on both systems. Record the timings in the PR body.

- [ ] **Step 3: Commit**

```bash
git add adt/debugger_breakpoints_integration_test.go adt/unittest_long_run_integration_test.go
git commit -m "test(#200): live breakpoint tests (one request, debugger scope while halted, removal)"
```

---

## PR-4 — #201: variables

### Task 5: Wire types for the asXML variable methods

**Files:**
- Create: `adt/adtxml/debugger_vars.go`
- Test: `adt/adtxml/debugger_vars_test.go`

**Interfaces:**
- Produces (package `adtxml`):
  - `func ChildVariablesRequest(parentIDs []string) ([]byte, error)` — asXML body, `@ROOT` when empty slice → empty `<HIERARCHIES/>`
  - `func VariablesRequest(ids []string) ([]byte, error)`
  - `type ASXVariable struct` (fields below), `type ASXHierarchy struct { ParentID, ChildID, ChildName string }`
  - `func ParseChildVariables(data []byte) ([]ASXVariable, []ASXHierarchy, error)`
  - `func ParseVariables(data []byte) ([]ASXVariable, error)`
  - `func VariableDataRequest(name string, offset, length int, fields []string) ([]byte, error)`
  - `type DataTable struct { Name string; TotalLines int; Lines []DataLine }`, `type DataLine struct { Index int; Fields []DataField }`, `type DataField struct { Path, Value string }`
  - `func ParseVariableData(data []byte) (*DataTable, error)` — `nil, nil` for an empty `<dbg:data/>`

- [ ] **Step 1: Write the failing tests**

```go
package adtxml_test

import (
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func TestChildVariablesRequest_EscapesAndWraps(t *testing.T) {
	b, err := adtxml.ChildVariablesRequest([]string{"@LOCALS", "LO->ATTR", `{O:19*\PROGRAM=Z\CLASS=LCL}-MV`})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0">`,
		`<PARENT_ID>@LOCALS</PARENT_ID>`,
		`<PARENT_ID>LO-&gt;ATTR</PARENT_ID>`,
		`<PARENT_ID>{O:19*\PROGRAM=Z\CLASS=LCL}-MV</PARENT_ID>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
}

const childVarsResp = `<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>` +
	`<VARIABLES><STPDA_ADT_VARIABLE><ID>LS_ROW</ID><NAME>LS_ROW</NAME><META_TYPE>structure</META_TYPE><VALUE>Structure</VALUE><TABLE_LINES>0</TABLE_LINES><READ_ONLY>X</READ_ONLY></STPDA_ADT_VARIABLE>` +
	`<STPDA_ADT_VARIABLE><ID>LT_ROWS</ID><NAME>LT_ROWS</NAME><META_TYPE>table</META_TYPE><VALUE>[5x3(16)]Standard Table</VALUE><TABLE_LINES>5</TABLE_LINES><IS_VALUE_INCOMPLETE/></STPDA_ADT_VARIABLE></VARIABLES>` +
	`<HIERARCHIES><STPDA_ADT_VARIABLE_HIERARCHY><PARENT_ID>@ROOT</PARENT_ID><CHILD_ID>@LOCALS</CHILD_ID><CHILD_NAME>Locals</CHILD_NAME></STPDA_ADT_VARIABLE_HIERARCHY>` +
	`<STPDA_ADT_VARIABLE_HIERARCHY><PARENT_ID>ME</PARENT_ID><CHILD_ID/></STPDA_ADT_VARIABLE_HIERARCHY></HIERARCHIES>` +
	`</DATA></asx:values></asx:abap>`

func TestParseChildVariables(t *testing.T) {
	vars, links, err := adtxml.ParseChildVariables([]byte(childVarsResp))
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 2 || vars[1].ID != "LT_ROWS" || vars[1].MetaType != "table" || vars[1].TableLines != 5 || vars[0].ReadOnly != "X" {
		t.Errorf("vars: %+v", vars)
	}
	if len(links) != 2 || links[0].ChildName != "Locals" || links[1].ChildID != "" {
		t.Errorf("links: %+v", links)
	}
}

func TestVariableDataRequestAndParse(t *testing.T) {
	b, err := adtxml.VariableDataRequest("LT_ROWS", 2, 2, []string{"TEXT"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `<table name="LT_ROWS" offset="2" length="2"><field path="TEXT"></field></table>`) &&
		!strings.Contains(string(b), `<table name="LT_ROWS" offset="2" length="2"><field path="TEXT"/></table>`) {
		t.Errorf("request: %s", b)
	}
	tbl, err := adtxml.ParseVariableData([]byte(`<dbg:data xmlns:dbg="http://www.sap.com/adt/debugger"><table name="LT_ROWS" totalLines="5">` +
		`<tableLine index="2"><field path="TEXT"><value>row 2</value></field></tableLine>` +
		`<tableLine index="3"><field path="TEXT"><value>row 3</value></field></tableLine></table></dbg:data>`))
	if err != nil {
		t.Fatal(err)
	}
	if tbl == nil || tbl.TotalLines != 5 || len(tbl.Lines) != 2 || tbl.Lines[1].Index != 3 || tbl.Lines[1].Fields[0].Value != "row 3" {
		t.Errorf("table: %+v", tbl)
	}
	empty, err := adtxml.ParseVariableData([]byte(`<dbg:data xmlns:dbg="http://www.sap.com/adt/debugger"/>`))
	if err != nil || empty != nil {
		t.Errorf("empty data: %v %v", empty, err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./adt/adtxml/ -run 'ChildVariables|VariableData' -v` → FAIL to compile.

- [ ] **Step 3: Implement** `adt/adtxml/debugger_vars.go`:

```go
package adtxml

import "encoding/xml"

// asXML request/response types for the debugger methods getChildVariables,
// getVariables and getVariableData (POST /sap/bc/adt/debugger?method=…).
// Verified live 2026-10-06 on SAP_BASIS 816 and 750 (adtler#201).

const nsASX = "http://www.sap.com/abapxml"

type asxHierarchyReq struct {
	ParentID string `xml:"PARENT_ID"`
}

type asxChildVarsReq struct {
	XMLName     xml.Name          `xml:"asx:abap"`
	NS          string            `xml:"xmlns:asx,attr"`
	Version     string            `xml:"version,attr"`
	Hierarchies []asxHierarchyReq `xml:"asx:values>DATA>HIERARCHIES>STPDA_ADT_VARIABLE_HIERARCHY"`
}

// ChildVariablesRequest builds the getChildVariables body. An empty slice
// sends no parent, which the server treats as @ROOT.
func ChildVariablesRequest(parentIDs []string) ([]byte, error) {
	req := asxChildVarsReq{NS: nsASX, Version: "1.0"}
	for _, id := range parentIDs {
		req.Hierarchies = append(req.Hierarchies, asxHierarchyReq{ParentID: id})
	}
	b, err := xml.Marshal(req)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}

type asxVarIDReq struct {
	ID string `xml:"ID"`
}

type asxVariablesReq struct {
	XMLName xml.Name      `xml:"asx:abap"`
	NS      string        `xml:"xmlns:asx,attr"`
	Version string        `xml:"version,attr"`
	Vars    []asxVarIDReq `xml:"asx:values>DATA>STPDA_ADT_VARIABLE"`
}

// VariablesRequest builds the getVariables body.
func VariablesRequest(ids []string) ([]byte, error) {
	req := asxVariablesReq{NS: nsASX, Version: "1.0"}
	for _, id := range ids {
		req.Vars = append(req.Vars, asxVarIDReq{ID: id})
	}
	b, err := xml.Marshal(req)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}

// ASXVariable is one STPDA_ADT_VARIABLE entry. Flags are 'X' or empty.
type ASXVariable struct {
	ID                string `xml:"ID"`
	Name              string `xml:"NAME"`
	DeclaredTypeName  string `xml:"DECLARED_TYPE_NAME"`
	ActualTypeName    string `xml:"ACTUAL_TYPE_NAME"`
	Kind              string `xml:"KIND"`
	InstantiationKind string `xml:"INSTANTIATION_KIND"`
	AccessKind        string `xml:"ACCESS_KIND"`
	ParameterKind     string `xml:"PARAMETER_KIND"`
	MetaType          string `xml:"META_TYPE"`
	Value             string `xml:"VALUE"`
	IsValueIncomplete string `xml:"IS_VALUE_INCOMPLETE"`
	ReadOnly          string `xml:"READ_ONLY"`
	TechnicalType     string `xml:"TECHNICAL_TYPE"`
	Length            int    `xml:"LENGTH"`
	TableLines        int    `xml:"TABLE_LINES"`
	IsException       string `xml:"IS_EXCEPTION"`
	InheritanceClass  string `xml:"INHERITANCE_CLASS"`
}

// ASXHierarchy is one parent/child link.
type ASXHierarchy struct {
	ParentID  string `xml:"PARENT_ID"`
	ChildID   string `xml:"CHILD_ID"`
	ChildName string `xml:"CHILD_NAME"`
}

type asxChildVarsResp struct {
	Vars  []ASXVariable  `xml:"values>DATA>VARIABLES>STPDA_ADT_VARIABLE"`
	Links []ASXHierarchy `xml:"values>DATA>HIERARCHIES>STPDA_ADT_VARIABLE_HIERARCHY"`
}

// ParseChildVariables parses a getChildVariables response.
func ParseChildVariables(data []byte) ([]ASXVariable, []ASXHierarchy, error) {
	var r asxChildVarsResp
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, nil, err
	}
	return r.Vars, r.Links, nil
}

type asxVariablesResp struct {
	Vars []ASXVariable `xml:"values>DATA>STPDA_ADT_VARIABLE"`
}

// ParseVariables parses a getVariables response. Unknown IDs are simply absent.
func ParseVariables(data []byte) ([]ASXVariable, error) {
	var r asxVariablesResp
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return r.Vars, nil
}

type dataFieldReq struct {
	Path string `xml:"path,attr"`
}

type dataTableReq struct {
	Name   string         `xml:"name,attr"`
	Offset int            `xml:"offset,attr"`
	Length int            `xml:"length,attr"`
	Fields []dataFieldReq `xml:"field"`
}

type dataRequest struct {
	XMLName xml.Name     `xml:"dbg:dataRequest"`
	NS      string       `xml:"xmlns:dbg,attr"`
	Table   dataTableReq `xml:"table"`
}

// VariableDataRequest builds the getVariableData body for ONE table (the
// server does not reset its field buffer between tables). offset is 1-based.
func VariableDataRequest(name string, offset, length int, fields []string) ([]byte, error) {
	req := dataRequest{NS: "http://www.sap.com/adt/debugger", Table: dataTableReq{Name: name, Offset: offset, Length: length}}
	for _, f := range fields {
		req.Table.Fields = append(req.Table.Fields, dataFieldReq{Path: f})
	}
	return xml.Marshal(req)
}

// DataField is one cell of a table line.
type DataField struct {
	Path  string `xml:"path,attr"`
	Value string `xml:"value"`
}

// DataLine is one table line; Index is 1-based.
type DataLine struct {
	Index  int         `xml:"index,attr"`
	Fields []DataField `xml:"field"`
}

// DataTable is one page of an internal table. TotalLines is 0 on SAP_BASIS 750.
type DataTable struct {
	Name       string     `xml:"name,attr"`
	TotalLines int        `xml:"totalLines,attr"`
	Lines      []DataLine `xml:"tableLine"`
}

type dataResp struct {
	Tables []DataTable `xml:"table"`
}

// ParseVariableData parses a getVariableData response; nil for an empty one.
func ParseVariableData(data []byte) (*DataTable, error) {
	var r dataResp
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if len(r.Tables) == 0 {
		return nil, nil
	}
	return &r.Tables[0], nil
}
```

- [ ] **Step 4: Run tests** → PASS. If the `asx:values>…` request path produces `<asx:values>` correctly but the response unmarshal fails because of the `asx:` prefix, the response tags `values>DATA>…` (local names) are correct; do not add prefixes on the response side.

- [ ] **Step 5: Commit**

```bash
git add adt/adtxml/debugger_vars.go adt/adtxml/debugger_vars_test.go
git commit -m "feat(#201): asXML wire types for debugger variable methods"
```

### Task 6: `GetChildVariables` and `GetVariables`

**Files:**
- Modify: `adt/debugger.go` (or new `adt/debugger_vars.go` in package `adt` — prefer the new file)
- Test: `adt/debugger_vars_test.go` (new, package `adt_test`)

**Interfaces:**
- Consumes: Task 5.
- Produces:
  - `type DebugVariable struct { ID, Name, MetaType, DeclaredType, ActualType, TechnicalType, Value string; ValueIncomplete bool; TableLines int; Kind, AccessKind, InstantiationKind, ParameterKind string; Length int; ReadOnly, IsException bool; InheritanceClass string }`
  - `type DebugVariableLink struct { ParentID, ChildID, ChildName string }`
  - `type DebugChildVariables struct { Variables []DebugVariable; Links []DebugVariableLink }`
  - `func (d *DebugSession) GetChildVariables(ctx context.Context, parentIDs ...string) (*DebugChildVariables, error)` — links with empty `ChildID` dropped
  - `func (d *DebugSession) GetVariables(ctx context.Context, ids ...string) ([]DebugVariable, error)`

- [ ] **Step 1: Failing test** — a server that checks for `method=getChildVariables`, the `Content-Type` `application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.ChildVariables`, `Accept: application/vnd.sap.as+xml`, `X-sap-adt-sessiontype: stateful`, and answers `childVarsResp` (copy the constant from Task 5's test). Assert two variables, `TableLines == 5` for `LT_ROWS`, `ReadOnly == true` for `LS_ROW`, and exactly one link (the empty-`CHILD_ID` link for `ME` dropped). Same for `GetVariables` with `dataname=com.sap.adt.debugger.Variables` and a response of `<DATA><STPDA_ADT_VARIABLE>…</DATA>`.

```go
func TestGetChildVariables(t *testing.T) {
	var gotCT, gotAccept, gotStateful, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		if r.URL.Query().Get("method") == "getChildVariables" {
			b, _ := io.ReadAll(r.Body)
			gotBody, gotCT, gotAccept, gotStateful = string(b), r.Header.Get("Content-Type"), r.Header.Get("Accept"), r.Header.Get("X-sap-adt-sessiontype")
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = w.Write([]byte(childVarsResp))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	dbg := adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U")
	res, err := dbg.GetChildVariables(context.Background(), "@LOCALS")
	if err != nil {
		t.Fatal(err)
	}
	if gotStateful != "stateful" || gotAccept != "application/vnd.sap.as+xml" ||
		gotCT != "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.ChildVariables" ||
		!strings.Contains(gotBody, "<PARENT_ID>@LOCALS</PARENT_ID>") {
		t.Errorf("request: ct=%q accept=%q stateful=%q body=%s", gotCT, gotAccept, gotStateful, gotBody)
	}
	if len(res.Variables) != 2 || res.Variables[1].TableLines != 5 || !res.Variables[0].ReadOnly {
		t.Errorf("variables: %+v", res.Variables)
	}
	if len(res.Links) != 1 || res.Links[0].ChildID != "@LOCALS" {
		t.Errorf("links (empty CHILD_ID dropped): %+v", res.Links)
	}
}
```

- [ ] **Step 2: Run** → FAIL to compile.

- [ ] **Step 3: Implement**

```go
const (
	ctChildVariables = "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.ChildVariables"
	ctVariables      = "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.Variables"
	acceptASXML      = "application/vnd.sap.as+xml"
)

func (d *DebugSession) postASX(ctx context.Context, method, contentType string, body []byte) ([]byte, error) {
	resp, err := d.client.doMutate(ctx, http.MethodPost, "/sap/bc/adt/debugger?method="+method,
		bytes.NewReader(body), map[string]string{
			"Content-Type":          contentType,
			"Accept":                acceptASXML,
			"X-sap-adt-sessiontype": "stateful",
		})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

func toDebugVariable(v adtxml.ASXVariable) DebugVariable {
	return DebugVariable{
		ID: v.ID, Name: v.Name, MetaType: v.MetaType,
		DeclaredType: v.DeclaredTypeName, ActualType: v.ActualTypeName, TechnicalType: v.TechnicalType,
		Value: v.Value, ValueIncomplete: v.IsValueIncomplete == "X", TableLines: v.TableLines,
		Kind: v.Kind, AccessKind: v.AccessKind, InstantiationKind: v.InstantiationKind, ParameterKind: v.ParameterKind,
		Length: v.Length, ReadOnly: v.ReadOnly == "X", IsException: v.IsException == "X",
		InheritanceClass: v.InheritanceClass,
	}
}

// GetChildVariables expands "@ROOT", "@LOCALS", "@PARAMETERS", "@GLOBALS",
// "@SYSTEM" or any variable ID (structure → components, object reference →
// attributes, "REF->*" → dereferenced value, "ITAB[n]" → row). Internal tables
// have no children here; use GetTableRows. With no parentIDs the server
// treats the request as "@ROOT". VALUE keeps ABAP padding.
func (d *DebugSession) GetChildVariables(ctx context.Context, parentIDs ...string) (*DebugChildVariables, error) {
	body, err := adtxml.ChildVariablesRequest(parentIDs)
	if err != nil {
		return nil, fmt.Errorf("GetChildVariables marshal: %w", err)
	}
	data, err := d.postASX(ctx, "getChildVariables", ctChildVariables, body)
	if err != nil {
		return nil, err
	}
	vars, links, err := adtxml.ParseChildVariables(data)
	if err != nil {
		return nil, fmt.Errorf("GetChildVariables unmarshal: %w", err)
	}
	out := &DebugChildVariables{}
	for _, v := range vars {
		out.Variables = append(out.Variables, toDebugVariable(v))
	}
	for _, l := range links {
		if l.ChildID == "" {
			continue
		}
		out.Links = append(out.Links, DebugVariableLink{ParentID: l.ParentID, ChildID: l.ChildID, ChildName: l.ChildName})
	}
	return out, nil
}

// GetVariables returns metadata for ids ("A-B", "OBJ->ATTR", "ITAB[n]", "ITAB[]",
// "REF->COMP"). IDs the server does not know are omitted — match by ID.
func (d *DebugSession) GetVariables(ctx context.Context, ids ...string) ([]DebugVariable, error) {
	body, err := adtxml.VariablesRequest(ids)
	if err != nil {
		return nil, fmt.Errorf("GetVariables marshal: %w", err)
	}
	data, err := d.postASX(ctx, "getVariables", ctVariables, body)
	if err != nil {
		return nil, err
	}
	vars, err := adtxml.ParseVariables(data)
	if err != nil {
		return nil, fmt.Errorf("GetVariables unmarshal: %w", err)
	}
	out := make([]DebugVariable, 0, len(vars))
	for _, v := range vars {
		out = append(out, toDebugVariable(v))
	}
	return out, nil
}
```

Add the type declarations from **Interfaces** above the functions.

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add adt/debugger_vars.go adt/debugger_vars_test.go
git commit -m "feat(#201): GetChildVariables and GetVariables"
```

### Task 7: `GetTableRows`

**Files:**
- Modify: `adt/debugger_vars.go`
- Test: `adt/debugger_vars_test.go`

**Interfaces:**
- Produces:
  - `type DebugTableField struct { Path, Value string }`, `type DebugTableRow struct { Index int; Fields []DebugTableField }`, `type DebugTablePage struct { Name string; TotalLines, Offset int; Rows []DebugTableRow }`
  - `var ErrNotATable = errors.New("variable is not an internal table")`
  - `func (d *DebugSession) GetTableRows(ctx context.Context, name string, offset, limit int, fields ...string) (*DebugTablePage, error)`

- [ ] **Step 1: Failing tests** (table-driven, one fake server counting `getVariableData` calls and recording the body; `getVariables` answers `LT_ROWS` with `META_TYPE` `table` and `TABLE_LINES` 5, and `LS_ROW` with `structure`):

| case | args | want |
|---|---|---|
| clamp | offset 4, limit 10 | request `offset="4" length="2"` |
| beyond end | offset 6, limit 3 | empty page, `TotalLines` 5, **no** `getVariableData` call |
| offset < 1 | offset 0 | error mentioning "offset", no SAP call |
| not a table | name `LS_ROW` | `errors.Is(err, adt.ErrNotATable)` |
| unknown | name `NOPE` (absent from getVariables answer) | error mentioning "unknown variable" |
| limit ≤ 0 | limit 0 | error mentioning "limit" |

```go
func TestGetTableRows(t *testing.T) {
	var dataCalls int
	var lastData string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Query().Get("method") {
		case "getVariables":
			var out strings.Builder
			out.WriteString(`<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>`)
			if strings.Contains(string(b), "<ID>LT_ROWS</ID>") {
				out.WriteString(`<STPDA_ADT_VARIABLE><ID>LT_ROWS</ID><META_TYPE>table</META_TYPE><TABLE_LINES>5</TABLE_LINES></STPDA_ADT_VARIABLE>`)
			}
			if strings.Contains(string(b), "<ID>LS_ROW</ID>") {
				out.WriteString(`<STPDA_ADT_VARIABLE><ID>LS_ROW</ID><META_TYPE>structure</META_TYPE></STPDA_ADT_VARIABLE>`)
			}
			out.WriteString(`</DATA></asx:values></asx:abap>`)
			_, _ = w.Write([]byte(out.String()))
		case "getVariableData":
			dataCalls++
			lastData = string(b)
			_, _ = w.Write([]byte(`<dbg:data xmlns:dbg="http://www.sap.com/adt/debugger"><table name="LT_ROWS">` +
				`<tableLine index="4"><field path="TEXT"><value>r4</value></field></tableLine>` +
				`<tableLine index="5"><field path="TEXT"><value>r5</value></field></tableLine></table></dbg:data>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	dbg := adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U")
	ctx := context.Background()

	page, err := dbg.GetTableRows(ctx, "LT_ROWS", 4, 10, "TEXT")
	if err != nil || !strings.Contains(lastData, `offset="4" length="2"`) || page.TotalLines != 5 || len(page.Rows) != 2 || page.Rows[1].Fields[0].Value != "r5" {
		t.Errorf("clamp: page=%+v err=%v body=%s", page, err, lastData)
	}
	before := dataCalls
	page, err = dbg.GetTableRows(ctx, "LT_ROWS", 6, 3)
	if err != nil || len(page.Rows) != 0 || page.TotalLines != 5 || dataCalls != before {
		t.Errorf("beyond end: page=%+v err=%v calls=%d", page, err, dataCalls-before)
	}
	if _, err := dbg.GetTableRows(ctx, "LT_ROWS", 0, 3); err == nil || !strings.Contains(err.Error(), "offset") {
		t.Errorf("offset 0: %v", err)
	}
	if _, err := dbg.GetTableRows(ctx, "LT_ROWS", 1, 0); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("limit 0: %v", err)
	}
	if _, err := dbg.GetTableRows(ctx, "LS_ROW", 1, 3); !errors.Is(err, adt.ErrNotATable) {
		t.Errorf("structure: %v", err)
	}
	if _, err := dbg.GetTableRows(ctx, "NOPE", 1, 3); err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Errorf("unknown: %v", err)
	}
}
```

- [ ] **Step 2: Run** → FAIL to compile.

- [ ] **Step 3: Implement**

```go
// ErrNotATable is returned by GetTableRows for a variable that is not an internal table.
var ErrNotATable = errors.New("variable is not an internal table")

// GetTableRows reads one page of internal table name. offset is 1-based; limit
// is clamped to the row count (SAP_BASIS 750 fails when a page runs past the
// end). An offset beyond the last row returns an empty page without calling
// SAP. One table per request (the server keeps its field buffer between tables).
func (d *DebugSession) GetTableRows(ctx context.Context, name string, offset, limit int, fields ...string) (*DebugTablePage, error) {
	if offset < 1 {
		return nil, fmt.Errorf("GetTableRows: offset must be >= 1 (1-based), got %d", offset)
	}
	if limit < 1 {
		return nil, fmt.Errorf("GetTableRows: limit must be >= 1, got %d", limit)
	}
	vars, err := d.GetVariables(ctx, name)
	if err != nil {
		return nil, err
	}
	var meta *DebugVariable
	for i := range vars {
		if vars[i].ID == name {
			meta = &vars[i]
		}
	}
	if meta == nil {
		return nil, fmt.Errorf("GetTableRows: unknown variable %q", name)
	}
	if meta.MetaType != "table" {
		return nil, fmt.Errorf("GetTableRows %q (%s): %w", name, meta.MetaType, ErrNotATable)
	}
	page := &DebugTablePage{Name: name, TotalLines: meta.TableLines, Offset: offset}
	if offset > meta.TableLines {
		return page, nil
	}
	if max := meta.TableLines - offset + 1; limit > max {
		limit = max
	}
	body, err := adtxml.VariableDataRequest(name, offset, limit, fields)
	if err != nil {
		return nil, fmt.Errorf("GetTableRows marshal: %w", err)
	}
	resp, err := d.client.doMutate(ctx, http.MethodPost, "/sap/bc/adt/debugger?method=getVariableData",
		bytes.NewReader(body), map[string]string{
			"Content-Type":          contentTypeXML,
			"Accept":                "application/xml",
			"X-sap-adt-sessiontype": "stateful",
		})
	if err != nil {
		return nil, fmt.Errorf("GetTableRows: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	data, _ := io.ReadAll(resp.Body)
	tbl, err := adtxml.ParseVariableData(data)
	if err != nil {
		return nil, fmt.Errorf("GetTableRows unmarshal: %w", err)
	}
	if tbl == nil {
		return page, nil
	}
	for _, l := range tbl.Lines {
		row := DebugTableRow{Index: l.Index}
		for _, f := range l.Fields {
			row.Fields = append(row.Fields, DebugTableField{Path: f.Path, Value: f.Value})
		}
		page.Rows = append(page.Rows, row)
	}
	return page, nil
}
```

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit**

```bash
git add adt/debugger_vars.go adt/debugger_vars_test.go
git commit -m "feat(#201): GetTableRows with 1-based offset and clamped paging"
```

### Task 8: Fix `GetVariable` escaping and doc; live variable tests

**Files:**
- Modify: `adt/debugger.go` (`GetVariable`)
- Test: `adt/debugger_test.go` (escaping), `adt/debugger_vars_integration_test.go` (new, `//go:build integration`)
- Modify: `adt/unittest_long_run_integration_test.go` (use `createReportWithSource` from Task 4; if PR-3 is not merged yet, add the extraction here instead)

- [ ] **Step 1: Failing unit test** — `GetVariable(ctx, "LO_ITEM->MT_TAGS[2]")` must reach the server with `r.URL.Query().Get("variableName") == "LO_ITEM->MT_TAGS[2]"` (today `>` and `[` are sent raw and `&`/`#` would break the query).

- [ ] **Step 2: Implement** — in `GetVariable`: `path := "/sap/bc/adt/debugger?method=getVariableValue&variableName=" + url.QueryEscape(name)`; fix the doc comment to say `method=getVariableValue`.

- [ ] **Step 3: Integration test** — per system, a throwaway report `Z_ADT_201_<digits>` created with `createReportWithSource` and this source (built with `"…\n" +`):

```
REPORT <name>.
CLASS lcl_item DEFINITION.
  PUBLIC SECTION.
    DATA mv_name TYPE string.
    DATA mt_tags TYPE string_table.
ENDCLASS.
CLASS lcl_item IMPLEMENTATION.
ENDCLASS.
CLASS lcl_test DEFINITION FOR TESTING RISK LEVEL HARMLESS DURATION SHORT.
  PRIVATE SECTION.
    TYPES: BEGIN OF ty_row, id TYPE i, text TYPE string, flag TYPE abap_bool, END OF ty_row.
    METHODS test_vars FOR TESTING.
ENDCLASS.
CLASS lcl_test IMPLEMENTATION.
  METHOD test_vars.
    DATA ls_row TYPE ty_row.
    DATA lt_rows TYPE STANDARD TABLE OF ty_row WITH EMPTY KEY.
    DATA lo_item TYPE REF TO lcl_item.
    DATA lr_data TYPE REF TO ty_row.
    ls_row = VALUE #( id = 7 text = `seven` flag = abap_true ).
    DO 5 TIMES.
      APPEND VALUE #( id = sy-index text = |row { sy-index }| ) TO lt_rows.
    ENDDO.
    lo_item = NEW #( ).
    lo_item->mv_name = `item`.
    APPEND `a` TO lo_item->mt_tags.
    APPEND `b` TO lo_item->mt_tags.
    lr_data = REF #( ls_row ).
    cl_abap_unit_assert=>assert_equals( act = lines( lt_rows ) exp = 5 ).
  ENDMETHOD.
ENDCLASS.
```

Breakpoint on the `cl_abap_unit_assert` line (compute the line number from the source slice, do not hard-code). Trigger with unit tests, attach, then assert:
- `GetChildVariables("@LOCALS")` contains `LS_ROW` (structure), `LT_ROWS` (table, `TableLines` 5), `LO_ITEM` (objectref), `LR_DATA` (dataref);
- `GetChildVariables("LS_ROW")` has `LS_ROW-TEXT` with `Value` `seven` (trim right spaces only for the comparison);
- `GetChildVariables("LO_ITEM")` has an attribute ending in `-MV_NAME` with value `item`;
- `GetChildVariables("LR_DATA")` has `LR_DATA->*`; expanding that gives `LR_DATA->TEXT`;
- `GetTableRows("LT_ROWS", 2, 2, "TEXT")` returns indexes 2 and 3 with `row 2`/`row 3`;
- `GetVariables("LO_ITEM->MT_TAGS[2]")` returns one variable;
- then `detachDebugger`; cleanup removes the breakpoint (PR-3 API, or `StopListener` if PR-3 is not merged yet) and deletes the report.

- [ ] **Step 4: Run live**

Run: `SAP_INTEGRATION_SYSTEM=none SAP_INTEGRATION_SYSTEMS=<r3-key>,<s4-key> go test -tags integration -run TestDebugVariables -count=1 -v ./adt/` → PASS on both.

- [ ] **Step 5: Commit**

```bash
gofmt -l . ; go vet ./... && go test ./... && golangci-lint run
git add adt/debugger.go adt/debugger_test.go adt/debugger_vars_integration_test.go adt/unittest_long_run_integration_test.go
git commit -m "fix(#201): escape GetVariable's variableName; live tests for structured variables"
```

---

## Wrap-up per PR

For each PR: independent subagent review of diff and PR body (public-repo rules, accuracy, cold-reader), body ends with `Closes #<issue>` and `Related: Hochfrequenz/aibap.mcp#558`, the integration-test output (both systems) pasted with system aliases replaced by type/release. Then the adtler release containing PR-1…PR-4 and #196.
