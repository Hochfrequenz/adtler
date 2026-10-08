# RunUnitTests zero-test results (adtler#212) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `RunUnitTests` result that executed no test method tells the caller why, as far as SAP lets us know: an unparsable response becomes an error, ABAP Unit alerts outside test methods (run, program and test-class level) are surfaced with their language-independent kind, and inactive parts related to the tested object URI are listed.

**Architecture:** `RunUnitTests` (`adt/unittest.go`) stops discarding the `xml.Unmarshal` error and starts reading the `<alerts>` that SAP attaches to the run, to each program and to each test class. When the parsed run contains no test method, it reads `GetInactiveObjects` once and keeps the entries that `objectURIMatches` (`adt/activate.go`) relates to the requested URI. Two new fields on `TestResult` carry the result; the error contract is unchanged except for the parse failure.

**Tech Stack:** Go 1.25, `net/http/httptest` for unit tests, the `integration` build tag with `eachSystem(t)` for the live test.

**Spec:** [adtler#212](https://github.com/Hochfrequenz/adtler/issues/212), plus the measurement below, which settles the issue's third "possible direction".

**Branch:** `fix/212-unittest-zero-tests`, from `main` @ `ac7e97c`.

Revision 2, after an independent plan review. Alerts are exposed as `TestResult.Alerts []TestAlert` (kind, severity, title) instead of bare titles; test-class alerts are included; the godoc states the URI-nesting limits; the formatting check works on a CRLF checkout; the "Finish" section lists the PR and issue steps.

## Background

`RunUnitTests` returns `{0, 0, 0, nil}` with a nil error whenever no test method ran. The issue names three causes a caller cannot tell apart: the object has no test classes (case 1), its test-classes include is still inactive so the active version has no tests (case 2), and the response could not be parsed (case 3, `adt/unittest.go:73` ignores the `xml.Unmarshal` error).

### Measurement (2026-10-08)

The raw `POST /sap/bc/adt/abapunit/testruns` response was captured on SAP ERP 6.0 EHP8 (SAP_BASIS 750) and SAP S/4HANA 2025 on-premise (SAP_BASIS 816), with the request body `RunUnitTests` sends today. All responses were HTTP 200, `application/xml`.

| Situation | SAP_BASIS 750 | SAP_BASIS 816 |
|---|---|---|
| Active class, no test-classes include | run-level `<alert kind="noTestClasses" severity="tolerable">`, title "Program '…CP' Does not Contain any Test Classes." | `<aunit:runResult …/>` (empty element) |
| Test-classes include written but inactive (class URI or include URI) | identical to the row above | identical to the row above |
| Class URI naming no existing object | `<aunit:runResult …><alerts/></aunit:runResult>` | empty element |
| URI outside any object collection | run-level `noTestClasses` alert, title "The task definition does not refer to any test" | empty element |
| After activating the include | one `<testMethod>`, 1 passed | one `<testMethod>`, 1 passed |

Consequences for the design:

- **The response cannot tell case 1 from case 2 on either release.** SAP_BASIS 750 sends the same alert for both, SAP_BASIS 816 sends nothing for either. Distinguishing them needs the extra `GetInactiveObjects` read the issue proposed. After the include was written, `GetInactiveObjects` listed the class, `…/includes/testclasses`, and two `…/source/main#type=…` entries on both releases.
- **The run-level alert is still worth surfacing**, with its `kind`: on SAP_BASIS 750 it separates "the object exists and has no tests" from "the URI refers to no test at all". Today it is parsed into nothing because `adtxml.RunResult` has no field for it. The `kind` attribute is language-independent; the title is in the logon language, so a consumer must be able to branch on the kind.
- **An empty `<aunit:runResult/>` is a valid answer**, not a parse failure. The parse-error change must keep accepting it.
- **A URI naming a non-existent object yields a 200 without test methods on both releases.** Detecting that would need an existence check whose URI forms (packages, includes, fragments) have not been measured. It is out of scope here; the godoc states the limitation, and it is reported back on the issue as a follow-up candidate.

## Global Constraints

- No new dependency.
- The repository is public: no host names, system aliases, transport numbers, client numbers, or object names from a registered customer namespace in code, comments, tests, commit messages or PR text. Unit-test fixtures use `zcl_test`-style placeholders. The integration test creates its own `$TMP` class with a generated `ZCL_ADT212_<n>` name and deletes it afterwards.
- Comments and commit messages: neutral, English, factual.
- After every task: `go test ./...`, `go build -tags integration ./adt/...`, `go vet -tags integration ./adt/...` and `golangci-lint run --enable dupl,goconst,gocyclo ./...` pass, and the changed Go files are gofmt-clean. This checkout uses `core.autocrlf=true`, so plain `gofmt -l ./adt` and golangci-lint's gofmt formatter flag every CRLF file. Check formatting on LF content instead — this must print nothing: `for f in $(git diff --name-only origin/main -- '*.go'); do tr -d '\r' < "$f" | gofmt -d; done`. CRLF-only gofmt findings from golangci-lint are expected locally; CI on Linux is authoritative.
- Never use Go raw (backtick) string literals for ABAP source. XML fixtures in raw strings are fine and are the existing convention.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Integration runs: the integration `TestMain` creates a transport on the default system on every run (pre-existing behaviour, out of scope). Run the integration test as few times as needed, always with `-run`, and do not set `SAP_INTEGRATION_SYSTEM`.

## Review Focus

1. **S/4's empty `<aunit:runResult/>`** must parse without error and go on to the inactive check. Pinned in Task 1 (`TestRunUnitTests_EmptyRunResult`) and Task 2 (`TestRunUnitTests_ZeroTests_InactiveReadFails` uses the same body).
2. **A sibling class whose name extends the requested one** (`zcl_test` vs `zcl_test2`) must not be reported as inactive under it. Pinned in Task 2 (`TestRunUnitTests_ZeroTests_ReportsInactiveParts`).
3. **Upper-case requested URI against SAP's lower-case inactive list**: agents pass upper-case names. Pinned in the same Task 2 test, which requests `ZCL_TEST` in upper case.
4. **A failing `GetInactiveObjects` read** must not turn a valid test result into an error. Pinned in Task 2 (`TestRunUnitTests_ZeroTests_InactiveReadFails`).
5. **The test-classes include URI as the run target** (`…/includes/testclasses`) must still report the inactive parts, because `objectURIMatches` matches in both nesting directions. Pinned live in the Task 2 integration test.

---

### Task 1: Report parse failures and surface alerts outside test methods

**Files:**
- Modify: `adt/adtxml/unittest.go` (`RunResult`, `Program`, `TestClass`)
- Modify: `adt/types.go:53-66` (new `TestAlert`, `TestResult`)
- Modify: `adt/unittest.go:72-95`
- Modify: `adt/client_test.go` (constant block at the top)
- Test: `adt/unittest_test.go`

**Interfaces:**
- Produces: `adt.TestAlert{Kind, Severity, Title string}`; `TestResult.Alerts []TestAlert`; `adtxml.RunResult.Alerts`, `adtxml.Program.Alerts`, `adtxml.TestClass.Alerts` (all `[]adtxml.Alert`, tag `xml:"alerts>alert"`); the unexported helper `toTestAlerts(alerts []adtxml.Alert) []TestAlert` in `adt/unittest.go`; in package `adt_test` the constants `aunitTestRunsPath`, `inactiveObjectsPath`, `emptyRunResult816` and the helpers `unitTestServer`, `runUnitTestsAgainst`.

- [ ] **Step 1: Add the test constants and the shared mock server**

In `adt/client_test.go`, next to `activationPath`, add:

```go
// aunitTestRunsPath is the ABAP Unit run endpoint. Hoisted for goconst.
const aunitTestRunsPath = "/sap/bc/adt/abapunit/testruns"

// inactiveObjectsPath is the activation inactive-objects endpoint. Hoisted
// for goconst.
const inactiveObjectsPath = "/sap/bc/adt/activation/inactiveobjects"
```

Replace the literal `"/sap/bc/adt/abapunit/testruns"` in the existing `TestRunUnitTests` (`adt/unittest_test.go`) with `aunitTestRunsPath`.

Append to `adt/unittest_test.go`:

```go
// emptyRunResult816 is SAP_BASIS 816's answer for a run that executed no
// test method, captured verbatim.
const emptyRunResult816 = `<?xml version="1.0" encoding="utf-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"/>`

// unitTestServer mocks the ABAP Unit run endpoint and the inactive-objects
// read. runBody is written verbatim as the run response. inactiveBody is the
// inactive-objects response; inactiveStatus, if non-zero, makes that read fail
// with the given status instead. inactiveReads counts the inactive-objects
// requests.
func unitTestServer(t *testing.T, runBody, inactiveBody string, inactiveStatus int, inactiveReads *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case aunitTestRunsPath:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(runBody))
		case inactiveObjectsPath:
			inactiveReads.Add(1)
			if inactiveStatus != 0 {
				w.WriteHeader(inactiveStatus)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(inactiveBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// runUnitTestsAgainst runs RunUnitTests for objectURI against srv.
func runUnitTestsAgainst(t *testing.T, srv *httptest.Server, objectURI string) (*adt.TestResult, error) {
	t.Helper()
	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	return adt.NewClient(cfg).RunUnitTests(context.Background(), objectURI, 30)
}
```

Add `"slices"` and `"sync/atomic"` to the imports of `adt/unittest_test.go`.

- [ ] **Step 2: Write the failing tests**

Append to `adt/unittest_test.go`:

```go
// TestRunUnitTests_UnparsableBody pins case 3 of #212: a response that is not
// an ABAP Unit run result must be an error, not an empty result.
func TestRunUnitTests_UnparsableBody(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, "<html><body>gateway error</body></html>", "", 0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err == nil {
		t.Fatalf("expected a parse error, got result %+v", result)
	}
}

// TestRunUnitTests_EmptyRunResult pins that S/4's answer for a run without
// tests, an empty <aunit:runResult/> element, is a valid result and not a
// parse failure.
func TestRunUnitTests_EmptyRunResult(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, emptyRunResult816,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"/>`,
		0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.TestCases) != 0 || len(result.Alerts) != 0 {
		t.Errorf("expected an empty result, got %+v", result)
	}
}

// TestRunUnitTests_OutsideMethodAlerts pins that alerts SAP attaches to the
// run, to a program or to a test class, outside any test method, reach
// TestResult.Alerts with their kind, in document order. The run-level alert
// is the SAP_BASIS 750 capture for a class without active test classes, with
// the class name replaced by a placeholder; the program- and class-level
// alerts are synthetic.
func TestRunUnitTests_OutsideMethodAlerts(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, `<?xml version="1.0" encoding="utf-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"><alerts><alert kind="noTestClasses" severity="tolerable"><title>Program 'ZCL_TEST======================CP' Does not Contain any Test Classes.</title><details><detail text="You can find further informations in document &lt;CHAP&gt; &lt;SAUNIT_NO_TEST_CLASS&gt;"><link rel=""/></detail></details><stack/></alert></alerts>
  <program adtcore:uri="/sap/bc/adt/oo/classes/zcl_test" adtcore:name="ZCL_TEST" xmlns:adtcore="http://www.sap.com/adt/core">
    <alerts><alert kind="warning" severity="tolerable"><title>Program-level alert</title></alert></alerts>
    <testClasses><testClass adtcore:name="LTC_TEST">
      <alerts><alert kind="exception" severity="critical"><title>Class-level alert</title></alert></alerts>
    </testClass></testClasses>
  </program>
</aunit:runResult>`,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"/>`,
		0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []adt.TestAlert{
		{Kind: "noTestClasses", Severity: "tolerable", Title: "Program 'ZCL_TEST======================CP' Does not Contain any Test Classes."},
		{Kind: "warning", Severity: "tolerable", Title: "Program-level alert"},
		{Kind: "exception", Severity: "critical", Title: "Class-level alert"},
	}
	if !slices.Equal(result.Alerts, want) {
		t.Errorf("Alerts = %+v, want %+v", result.Alerts, want)
	}
}
```

- [ ] **Step 3: Add only the new types, then run the tests to verify they fail**

In `adt/types.go`, add above `TestResult`:

```go
// TestAlert is an ABAP Unit alert that SAP attached outside a test method:
// to the run itself, to a tested program, or to a test class.
type TestAlert struct {
	// Kind is SAP's language-independent alert kind, e.g. "noTestClasses".
	Kind string
	// Severity is SAP's severity, e.g. "tolerable" or "critical".
	Severity string
	// Title is the alert text in the logon language.
	Title string
}
```

and add to `TestResult`, after `TestCases`:

```go
	// Alerts holds the alerts SAP attached outside a test method, in document
	// order: run level, then per program, then per test class. On SAP_BASIS
	// 750 a run that executed no test method usually carries one of kind
	// "noTestClasses"; SAP_BASIS 816 sends none in the same situations.
	Alerts []TestAlert
```

Run: `go test ./adt/ -run 'TestRunUnitTests_(UnparsableBody|EmptyRunResult|OutsideMethodAlerts)' -v`
Expected: `TestRunUnitTests_UnparsableBody` FAILS with "expected a parse error"; `TestRunUnitTests_OutsideMethodAlerts` FAILS with `Alerts = [], want [...]`; `TestRunUnitTests_EmptyRunResult` passes already (it guards the parse-error change in Step 4 against rejecting S/4's empty element).

- [ ] **Step 4: Implement**

`adt/adtxml/unittest.go`, extend the three structs:

```go
// RunResult is the XML response from a unit test run.
type RunResult struct {
	XMLName xml.Name `xml:"runResult"`
	// Alerts are attached to the run itself, outside any program. SAP_BASIS
	// 750 reports "no test classes" here; SAP_BASIS 816 omits it.
	Alerts   []Alert   `xml:"alerts>alert"`
	Programs []Program `xml:"program"`
}

// Program is a single program in a unit test result.
type Program struct {
	Alerts  []Alert     `xml:"alerts>alert"`
	Classes []TestClass `xml:"testClasses>testClass"`
}

// TestClass is a single test class result.
type TestClass struct {
	Name         string       `xml:"name,attr"`
	FailureCount int          `xml:"failureCount,attr"`
	ErrorCount   int          `xml:"errorCount,attr"`
	Alerts       []Alert      `xml:"alerts>alert"`
	Methods      []TestMethod `xml:"testMethods>testMethod"`
}
```

`adt/unittest.go`, replace the read-and-parse lines and the result loop header:

```go
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RunUnitTests reading body: %w", err)
	}
	var runResult adtxml.RunResult
	if err := xml.Unmarshal(data, &runResult); err != nil {
		return nil, fmt.Errorf("RunUnitTests parsing: %w", err)
	}

	result := &TestResult{Alerts: toTestAlerts(runResult.Alerts)}
	for _, prog := range runResult.Programs {
		result.Alerts = append(result.Alerts, toTestAlerts(prog.Alerts)...)
		for _, class := range prog.Classes {
			result.Alerts = append(result.Alerts, toTestAlerts(class.Alerts)...)
```

(the rest of the loop is unchanged), and add below `RunUnitTests`:

```go
// toTestAlerts converts parsed ABAP Unit alerts, returning nil for none.
func toTestAlerts(alerts []adtxml.Alert) []TestAlert {
	var out []TestAlert
	for _, a := range alerts {
		out = append(out, TestAlert{Kind: a.Kind, Severity: a.Severity, Title: a.Title})
	}
	return out
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./adt/ -run 'TestRunUnitTests' -v`
Expected: PASS, including the pre-existing `TestRunUnitTests`.

- [ ] **Step 6: Full checks and commit**

Run the Global Constraints checks, then:

```bash
git add adt/adtxml/unittest.go adt/types.go adt/unittest.go adt/unittest_test.go adt/client_test.go
git commit -m "fix(#212): report unparsable ABAP Unit responses and surface alerts outside test methods

RunUnitTests discarded the xml.Unmarshal error, so an unexpected body
read as a run with zero tests. It now returns the error. Alerts that SAP
attaches to the run, to a program or to a test class, outside any test
method, are returned in TestResult.Alerts with their language-independent
kind; SAP_BASIS 750 uses one of kind noTestClasses to say that an object
contains no test classes.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: List inactive parts when a run executed no test method

**Files:**
- Modify: `adt/types.go` (`TestResult`)
- Modify: `adt/unittest.go` (`RunUnitTests` godoc and tail, new helper)
- Modify: `adt/activate_integration_test.go` (extract the class-creation preamble into a helper)
- Create: `adt/unittest_inactive_integration_test.go`
- Modify: `CLAUDE.md` (one bullet under "SAP system differences")
- Test: `adt/unittest_test.go`

**Interfaces:**
- Consumes: from Task 1, `unitTestServer`, `runUnitTestsAgainst`, `emptyRunResult816` (package `adt_test`) and `TestResult.Alerts`. From existing code, `objectURIMatches(requested, candidate string) bool` and `(*httpClient).GetInactiveObjects` (`adt/activate.go`), and `writeClassPool70`, `classPool70Source`, `inactiveUnder`, `equalFold` (`adt/activate_integration_test.go`).
- Produces: `TestResult.InactiveURIs []string`; the unexported `(*httpClient).inactiveURIsUnder(ctx context.Context, objectURI string) []string`; the integration helper `newActiveClassPool70(t *testing.T, c adt.Client, prefix string) (name, uri string)`.

- [ ] **Step 1: Write the failing unit tests**

Append to `adt/unittest_test.go`:

```go
// TestRunUnitTests_ZeroTests_ReportsInactiveParts is the regression guard for
// case 2 of #212: when no test method ran, the inactive parts related to the
// requested object are listed, so an inactive test-classes include no longer
// looks like an object without tests. A sibling class whose name merely
// extends the requested one, and an unrelated class, must not be listed.
func TestRunUnitTests_ZeroTests_ReportsInactiveParts(t *testing.T) {
	const base = "/sap/bc/adt/oo/classes/zcl_test"
	want := []string{
		base,
		base + "/includes/testclasses",
		base + "/source/main#type=CLAS%2FOM;name=GET",
	}
	var entries strings.Builder
	for _, uri := range append(slices.Clone(want), "/sap/bc/adt/oo/classes/zcl_test2", "/sap/bc/adt/oo/classes/zcl_other") {
		entries.WriteString(`<entry><object><ref uri="` + uri + `" type="CLAS/OC" name="X" packageName="$TMP"/></object></entry>`)
	}
	var reads atomic.Int32
	srv := unitTestServer(t, emptyRunResult816,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core">`+entries.String()+`</ioc:inactiveObjects>`,
		0, &reads)
	defer srv.Close()

	// Upper-case on purpose: callers pass upper-case names, SAP lists lower case.
	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/ZCL_TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(result.InactiveURIs, want) {
		t.Errorf("InactiveURIs = %q, want %q", result.InactiveURIs, want)
	}
}

// TestRunUnitTests_TestsExecuted_SkipsInactiveCheck pins that the extra read
// happens only for a run that executed nothing.
func TestRunUnitTests_TestsExecuted_SkipsInactiveCheck(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, `<?xml version="1.0" encoding="utf-8"?>
<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">
  <program adtcore:uri="/sap/bc/adt/oo/classes/zcl_test" adtcore:name="ZCL_TEST">
    <testClasses><testClass adtcore:name="LTC_TEST">
      <testMethods><testMethod adtcore:name="RUNS" executionTime="0"/></testMethods>
    </testClass></testClasses>
  </program>
</aunit:runResult>`, "", 0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed != 1 {
		t.Errorf("Passed = %d, want 1", result.Passed)
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("inactive-objects reads = %d, want 0", n)
	}
	if result.InactiveURIs != nil {
		t.Errorf("InactiveURIs = %q, want nil", result.InactiveURIs)
	}
}

// TestRunUnitTests_ZeroTests_InactiveReadFails pins that a failing
// inactive-objects read only leaves InactiveURIs empty; the run result itself
// is still returned without an error.
func TestRunUnitTests_ZeroTests_InactiveReadFails(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, emptyRunResult816, "", http.StatusInternalServerError, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("inactive-objects reads = %d, want 1", n)
	}
	if result.InactiveURIs != nil {
		t.Errorf("InactiveURIs = %q, want nil", result.InactiveURIs)
	}
}
```

- [ ] **Step 2: Add only the field, then run the tests to verify they fail**

`adt/types.go`, add to `TestResult` after `Alerts`:

```go
	// InactiveURIs is filled only when the run executed no test method. It
	// lists, as SAP spells them, the GetInactiveObjects entries related to the
	// requested object URI: the object itself, a part nested under it, or an
	// object it is nested under — for example an inactive test-classes
	// include, whose tests a run cannot execute because ABAP Unit runs the
	// active version. The relation is by URI nesting, which holds for a
	// class and its includes (measured); a program's test classes live in a
	// separate include object and a package contains its objects without
	// nesting their URIs, so neither is covered. Nil means no related entry
	// was found or the inactive-objects read failed; it does not prove that
	// the object has no tests.
	InactiveURIs []string
```

Run: `go test ./adt/ -run 'TestRunUnitTests_(ZeroTests|TestsExecuted)' -v`
Expected: `TestRunUnitTests_ZeroTests_ReportsInactiveParts` FAILS with `InactiveURIs = [], want [...]`; `TestRunUnitTests_ZeroTests_InactiveReadFails` FAILS with `inactive-objects reads = 0, want 1`; `TestRunUnitTests_TestsExecuted_SkipsInactiveCheck` passes already (it guards Step 3 against reading on every run).

- [ ] **Step 3: Implement**

`adt/unittest.go`, replace the final `return result, nil` of `RunUnitTests` with:

```go
	if len(result.TestCases) == 0 {
		result.InactiveURIs = c.inactiveURIsUnder(ctx, objectURI)
	}
	return result, nil
```

`ctx` (the caller's context) is deliberate: `reqCtx` is sized for the run and may be nearly spent.

Add below `toTestAlerts`:

```go
// inactiveURIsUnder returns the GetInactiveObjects entries that objectURI
// relates to per objectURIMatches: the object itself, a part nested under it,
// or an object it is nested under. A failed read returns nil, because the
// check only annotates a test result and must not turn it into an error.
func (c *httpClient) inactiveURIsUnder(ctx context.Context, objectURI string) []string {
	inactive, err := c.GetInactiveObjects(ctx)
	if err != nil {
		return nil
	}
	var uris []string
	for _, obj := range inactive {
		if objectURIMatches(objectURI, obj.URI) {
			uris = append(uris, obj.URI)
		}
	}
	return uris
}
```

Extend the `RunUnitTests` godoc with a second paragraph, after the existing one:

```go
// SAP answers a run that executes no test method with HTTP 200 and no test
// method on both SAP_BASIS 750 and 816, whatever the reason: the object has
// no test classes, its test-classes include is still inactive (the run uses
// the active version), or the URI names no existing object at all. For such a
// run TestResult.Alerts carries any alert SAP sent (SAP_BASIS 750 sends one
// of kind "noTestClasses"; 816 sends none), and TestResult.InactiveURIs lists
// the inactive entries related to objectURI by URI nesting, which tells the
// inactive-include case apart for classes; see InactiveURIs for its limits.
// A non-existent object is not detected. A response body that is not an ABAP
// Unit run result is returned as an error.
```

- [ ] **Step 4: Run the unit tests to verify they pass**

Run: `go test ./adt/ -run 'TestRunUnitTests' -v`
Expected: PASS for all `TestRunUnitTests*` tests.

- [ ] **Step 5: Extract the class-creation helper in the #70 integration test**

In `adt/activate_integration_test.go`, `TestActivateObjects_ClassPoolSubIncludes_Integration` currently creates the class, registers cleanup, logs out, writes the baseline source and activates it inline. Move exactly that preamble into:

```go
// newActiveClassPool70 creates a $TMP class named prefix plus a timestamp,
// registers its deletion as cleanup, and gives it an active baseline source
// without a test-classes include. It returns the class name and its
// lower-case object URI.
func newActiveClassPool70(t *testing.T, c adt.Client, prefix string) (name, uri string) {
	t.Helper()
	ctx := context.Background()
	name = fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano()%10000000000)
	uri = "/sap/bc/adt/oo/classes/" + strings.ToLower(name)

	if err := c.CreateObject(ctx, "CLAS", name, "$TMP", "adtler class-pool fixture", ""); err != nil {
		t.Fatalf("CreateObject %s: %v", name, err)
	}
	t.Cleanup(func() {
		if err := c.DeleteObject(context.Background(), uri, "", ""); err != nil {
			t.Logf("cleanup delete %s failed: %v", name, err)
		}
	})
	// S/4 leaves a session-bound ESRDIRE enqueue after CreateObject.
	_ = c.Logout(ctx)

	lock, err := c.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	src, err := c.GetSource(ctx, uri)
	if err == nil {
		_, err = c.SetSource(ctx, uri, classPool70Source(name, "baseline"), lock, "", src.ETag)
	}
	_ = c.UnlockObject(ctx, uri, lock)
	if err != nil {
		t.Fatalf("baseline source: %v", err)
	}
	if res, err := c.ActivateObjects(ctx, []string{uri}); err != nil || !res.Success {
		t.Fatalf("baseline activation: result=%+v err=%v", res, err)
	}
	return name, uri
}
```

and replace the inline preamble in the #70 test with `name, uri := newActiveClassPool70(t, c, "ZCL_ADT70")`. Keep the #70 test otherwise unchanged; the description text of the created class changes from "issue70 class-pool activation" to "adtler class-pool fixture", which nothing asserts on.

- [ ] **Step 6: Write the integration test**

Create `adt/unittest_inactive_integration_test.go`:

```go
//go:build integration

package adt_test

import (
	"context"
	"slices"
	"testing"
)

// TestRunUnitTests_InactiveTestInclude_Integration is the live regression
// guard for #212: a run against a class whose test-classes include exists
// only in an inactive version executes no test method, and RunUnitTests must
// then list that include in InactiveURIs — whether the run targets the class
// or the include itself. After activation the same run executes the test.
func TestRunUnitTests_InactiveTestInclude_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			c := sys.Client
			name, uri := newActiveClassPool70(t, c, "ZCL_ADT212")
			include := uri + "/includes/testclasses"

			writeClassPool70(t, c, uri, name, "issue212", true)
			if pending := inactiveUnder(t, c, uri); !slices.ContainsFunc(pending, equalFold(include)) {
				t.Fatalf("precondition: expected the test-classes include to be inactive, got %v", pending)
			}

			for _, target := range []string{uri, include} {
				res, err := c.RunUnitTests(ctx, target, 60)
				if err != nil {
					t.Fatalf("RunUnitTests(%s): %v", target, err)
				}
				t.Logf("inactive run via %s: passed=%d failed=%d alerts=%+v inactive=%d",
					target, res.Passed, res.Failed, res.Alerts, len(res.InactiveURIs))
				if len(res.TestCases) != 0 {
					t.Fatalf("RunUnitTests(%s): expected no executed test while the include is inactive, got %d", target, len(res.TestCases))
				}
				if !slices.ContainsFunc(res.InactiveURIs, equalFold(include)) {
					t.Errorf("RunUnitTests(%s): InactiveURIs = %v, want it to contain %s", target, res.InactiveURIs, include)
				}
			}

			if res, err := c.ActivateObjects(ctx, []string{uri}); err != nil || !res.Success {
				t.Fatalf("activation: result=%+v err=%v", res, err)
			}
			res, err := c.RunUnitTests(ctx, uri, 60)
			if err != nil {
				t.Fatalf("RunUnitTests after activation: %v", err)
			}
			if res.Passed != 1 || res.Failed != 0 {
				t.Errorf("after activation: passed=%d failed=%d, want 1/0", res.Passed, res.Failed)
			}
		})
	}
}
```

The logged and asserted URIs and alert titles refer to the generated `$TMP` class only, so they carry no internal data.

- [ ] **Step 7: Run the integration test, and see it fail without the fix**

Run: `SAP_INTEGRATION_SYSTEMS=<ecc-key>,<s4-key> go test -tags=integration -v -count=1 -run 'TestRunUnitTests_InactiveTestInclude_Integration|TestActivateObjects_ClassPoolSubIncludes_Integration' ./adt/`
(`<ecc-key>` and `<s4-key>` are the keys in the local `systems.json`; do not write them into any file.)
Expected: PASS on both systems for both tests. The log line for the ECC system shows one alert of kind `noTestClasses`, the S/4 one none.

Then replace `result.InactiveURIs = c.inactiveURIsUnder(ctx, objectURI)` with `_ = objectURI` temporarily and run only `TestRunUnitTests_InactiveTestInclude_Integration` again. Expected: FAIL with `InactiveURIs = [], want it to contain …/includes/testclasses` on both systems. Restore the line.

- [ ] **Step 8: Record the system difference in CLAUDE.md**

Under "### SAP system differences (R/3 vs S/4)" in `CLAUDE.md`, append:

```markdown
- **ABAP Unit runs without tests**: `POST /sap/bc/adt/abapunit/testruns` answers 200 for every run that executes no test method. R/3 (SAP_BASIS 750) adds a run-level alert of kind `noTestClasses`; S/4 (SAP_BASIS 816) returns an empty `<aunit:runResult/>`. Neither distinguishes an object without tests from one whose test-classes include is still inactive, and both answer the same way for a URI naming no existing object. `RunUnitTests` therefore lists related inactive parts in `TestResult.InactiveURIs` when nothing ran (#212).
```

- [ ] **Step 9: Full checks and commit**

Run the Global Constraints checks, then:

```bash
git add adt/types.go adt/unittest.go adt/unittest_test.go adt/activate_integration_test.go adt/unittest_inactive_integration_test.go CLAUDE.md
git commit -m "fix(#212): list inactive parts when an ABAP Unit run executed no test

A run against a class whose test-classes include is still inactive
executes the active version, finds no tests, and is answered exactly
like a run against a class without tests on both SAP_BASIS 750 and 816.
When a run executed no test method, RunUnitTests now reads the inactive
objects and returns those related to the requested URI by URI nesting in
TestResult.InactiveURIs. A failed read leaves the field empty and does
not fail the run.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Finish (controller, after the final review)

Not an implementer task; listed so the PR carries everything the workflow in `CLAUDE.md` requires.

- PR title `fix(#212): make RunUnitTests results without executed tests explainable`; body links `Closes #212` and, if aibap.mcp tracks the symptom, `Related: Hochfrequenz/aibap.mcp#<n>` (never a closing keyword cross-repo).
- The PR body states the behaviour change for consumers: an unparsable response body, including an empty one, is now an error instead of a 0/0/0 result. That includes the `DebugSession.RunUnitTests` breakpoint-trigger path, whose response when the debuggee is terminated has not been measured.
- Add the `needs:integration-test` label.
- Comment on #212 with the measurement table and the follow-up candidate: a URI naming a non-existent object is answered with 200 and no test method on both releases and stays undetected.
