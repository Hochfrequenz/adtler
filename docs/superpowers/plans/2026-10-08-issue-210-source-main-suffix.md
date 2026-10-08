# Source URI suffix normalisation (adtler#210) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every public method that builds a sub-path from an object URI accepts both the bare object URI and the source URI (`…/source/main`, optionally with trailing slash, `#start=…` fragment or query), and sends exactly one `/source/main` (or the correct other sub-path) to SAP.

**Architecture:** One unexported helper, `bareObjectURI`, reduces an incoming URI to the bare object URI. It builds on the existing `normalizeObjectURI` (`adt/activate.go:109`, strips query, fragment and trailing slash) and additionally strips a trailing `/source/main`. Each affected public entry point calls it once, at its top, before any path is built or any URI-keyed lookup (`sourceContentType`, `isDDLSourceURI`, `isClassURI`) runs. Internal helpers keep receiving a bare URI and stay unchanged.

**Tech Stack:** Go 1.25, `net/http/httptest` for unit tests, the `integration` build tag with `eachSystem(t)` for live tests.

**Spec:** [adtler#210](https://github.com/Hochfrequenz/adtler/issues/210) (issue body and the follow-up comment confirming function modules and function group includes).

**Branch:** `fix/210-source-main-suffix`, from `main` @ `515a10f`.

Revision 2, after an independent plan review. Its findings are folded into the task text below.

## Background

`GetSource` builds `objectURI + "/source/main"` unconditionally. ADT itself hands out source URIs that already carry that suffix — syntax-check messages (`…/source/main#start=42,5`, see `adt/syntaxcheck.go:172`), navigation targets, debugger stack frames (`adt/debugger.go:100`). A caller that passes such a URI back gets `…/source/main/source/main`, and SAP answers 404 `No suitable resource found`, which reads like a missing object. Verified live on an ECC system for programs, classes, interfaces, function modules and function group includes; the path is built client-side, so it is release-independent.

The fragment form fails differently. The client builds the request with `http.NewRequestWithContext(ctx, method, c.cfg.Host+path, …)` (`adt/client.go:468`), which parses `#` as the start of a fragment and drops everything after it. For `GetSource` that accidentally lands on the right path; for `SetSource` and `SetIncludeSource` it silently drops `?corrNr=` and `?lockHandle=`; for `DiffActiveInactive` it drops `?version=`; and `GetCompletions` builds a `uri=` parameter with two fragments.

### Affected entry points (all in package `adt`)

| Method | File | Paths built today |
|---|---|---|
| `GetSource` | `adt/source.go:102` | `+ "/source/main"` |
| `GetClassDefinition` | `adt/source.go:161` | `+ "/objectstructure"`, and `GetSource` |
| `GetIncludeSource` | `adt/source.go:238` | `classIncludePath` → `+ "/includes/<inc>"` |
| `SetIncludeSource` | `adt/source.go:261` | `classIncludePath` → `+ "/includes/<inc>"` |
| `CreateTestInclude` | `adt/source.go:317` | `+ "/includes?…"` |
| `SetSource` | `adt/source.go:344` | `trySetSource` → `setSourceWithLock{Header,Param}` → `+ "/source/main"` |
| `GetVersionHistory` | `adt/version.go:36` | `+ "/source/main/versions"` or `+ "/includes/<inc>/versions"` |
| `DiffActiveInactive` | `adt/version.go:158` | `getSourceWithVersion` → `+ "/source/main?version=…"` |
| `GetCompletions` | `adt/completion.go:15` | `+ "/source/main#start=L,C"` as `uri=` parameter |
| `GetTextElements`, `SetTextElements`, `TextElementLockURI` | `adt/textelements.go` | all via `resolveTextElementPath` (`:148`) → `/sap/bc/adt/textelements/<kind>/<rest of URI>` |

Callers reached through these and therefore covered without their own change: `LockMap.ResolveETag` (`adt/lockmap.go:93`), `RollbackTransport` (`adt/rollback.go:170`), `VerifySource` (`adt/verify.go:41`), `ClientRegistry` delegates (`adt/registry.go`).

### Deliberately out of scope

- `FetchETag` (`adt/source.go:26`) GETs the URI as given; a source URI is a valid target there, so it needs no change.
- `GetVersionSource` takes a version *content* URI, not an object URI; it must not be normalised.
- `LockObject`, `UnlockObject`, `DeleteObject`, `ActivateObjects`: these send the URI to SAP as given (no client-side sub-path), and what SAP does with a source URI there has not been measured. Normalising them would be an unmeasured behaviour change.

### Query strings

`bareObjectURI` drops a query, via `normalizeObjectURI`. No query form ever worked at these entry points: every one appends a sub-path after the input, so `…/source/main?version=inactive` became `…?version=inactive/source/main`. Dropping it yields the object's main source, which is what the methods are documented to return. The godoc says so.

## Global Constraints

- No new dependency.
- The repository is public: no host names, system aliases, transport numbers, client numbers, or object names from a registered customer namespace in code, comments, tests, commit messages or PR text. Unit-test fixtures use `ztest` / `zcl_test` and the transport placeholder `T1`; the integration test discovers its objects at runtime and logs counts and types only, never names, and passes every error through `redact` before logging it.
- Comments and commit messages: neutral, English, factual.
- `go test ./...`, `go build -tags integration ./adt/...`, `go vet -tags integration ./adt/...`, `gofmt -l ./adt` (must print nothing) and `golangci-lint run --enable dupl,goconst,gocyclo ./...` must all pass after every task.
- Commit messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Upper-case suffix** (`…/SOURCE/MAIN`): agents pass upper-case names; the strip must be case-insensitive. Pinned by the `bareObjectURI` table and the `…/SOURCE/MAIN` input form in Task 1.
2. **Fragment-carrying source URI** (`…/source/main#start=42,5`), the exact shape syntax-check messages produce: must collapse to the bare URI; `SetSource` must keep its `?corrNr=`; `GetCompletions` must emit exactly one `#start=L,C` with the caller's line/column. Pinned by the input-form table in Task 1 (`SetSource` / `SetIncludeSource` / `CreateTestInclude` pass transport `T1` so the dropped query is visible).
3. **A name that merely ends in `source`** (`…/programs/programs/zsource`, `…/source/mainx`): must not be shortened. Pinned by negative rows of the `bareObjectURI` table.
4. **Function group include / function module source URIs** (`…/includes/<inc>/source/main`, `…/fmodules/<fm>/source/main`): must reduce to the include / function-module object URI, not further. Pinned by the `bareObjectURI` table (unit) and the `FUGR/I` / `FUGR/FF` fixtures (live, Task 2).
5. **Query on the input** (`…/source/main?version=inactive`): dropped, see "Query strings". Pinned by a `bareObjectURI` table row so a later change to that decision is a visible test edit.

---

### Task 1: `bareObjectURI` and normalisation at every affected entry point

**Files:**
- Modify: `adt/source.go` — add `sourceMainSuffix` and `bareObjectURI` after `isDDLSourceURI` (around line 368); call it at the top of `GetSource`, `GetClassDefinition`, `GetIncludeSource`, `SetIncludeSource`, `CreateTestInclude`, `SetSource`
- Modify: `adt/version.go` — call it at the top of `GetVersionHistory` and `DiffActiveInactive`
- Modify: `adt/completion.go` — call it at the top of `GetCompletions`
- Modify: `adt/textelements.go` — call it at the top of `resolveTextElementPath`
- Modify: `adt/client.go` — godoc on `SourceClient` (line 18) and `VersionClient` (line 116)
- Modify: `adt/textelements_test.go` — two rows in `TestResolveTextElementPath`
- Create: `adt/source_uri_internal_test.go` (package `adt`)
- Create: `adt/source_uri_suffix_test.go` (package `adt_test`)

**Interfaces:**
- Consumes: `normalizeObjectURI(uri string) string` (`adt/activate.go:109`, existing).
- Produces: `func bareObjectURI(uri string) string` (unexported, package `adt`); `const sourceMainSuffix = "/source/main"` (unexported, package `adt`). Public API signatures unchanged.

- [ ] **Step 1: Write the failing entry-point test**

Create `adt/source_uri_suffix_test.go`:

```go
package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

const (
	suffixTestProgURI   = "/sap/bc/adt/programs/programs/ztest"
	suffixTestClassURI  = "/sap/bc/adt/oo/classes/zcl_test"
	suffixTestInclude   = "testclasses"
	suffixTestTransport = "T1"
)

// suffixTestObjectStructure is the smallest objectstructure body
// GetClassDefinition can parse: one definitionBlock link ending on line 2.
const suffixTestObjectStructure = `<?xml version="1.0" encoding="utf-8"?>
<abapsource:objectStructureElement xmlns:abapsource="http://www.sap.com/adt/abapsource" xmlns:atom="http://www.w3.org/2005/Atom">
  <atom:link href="./source/main#start=1,0;end=2,8" rel="http://www.sap.com/adt/relations/source/definitionBlock"/>
</abapsource:objectStructureElement>`

// sourceURIInputForms lists the URIs a caller may hold for one object: the
// bare object URI, and the source URI forms ADT itself hands out
// (syntax-check messages, navigation targets, debugger stack frames).
func sourceURIInputForms(bare string) []string {
	return []string{
		bare,
		bare + "/",
		bare + "/source/main",
		bare + "/source/main/",
		bare + "/SOURCE/MAIN",
		bare + "/source/main#start=42,5",
	}
}

// newSourcePathRecorder starts a test server that answers every source
// request with a minimal valid body and records "METHOD path?query" for each
// request except the discovery / CSRF preflight. take returns the recorded
// requests sorted, and resets the record.
func newSourcePathRecorder(t *testing.T) (srv *httptest.Server, take func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("ETag", `"etag"`)
		switch {
		case strings.HasSuffix(r.URL.Path, "/objectstructure"):
			_, _ = w.Write([]byte(suffixTestObjectStructure))
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_, _ = w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"/>`))
		case strings.HasSuffix(r.URL.Path, "/codecompletion/proposal"):
			// Empty body: GetCompletions reports no proposals.
		default:
			_, _ = w.Write([]byte("REPORT ztest.\nWRITE 'x'."))
		}
	}))
	t.Cleanup(srv.Close)
	take = func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := seen
		seen = nil
		slices.Sort(out)
		return out
	}
	return srv, take
}

// TestSourceMethods_AcceptSourceURI is the regression test for adtler#210:
// every method that builds a sub-path from an object URI must send the same
// request for the bare object URI and for every source URI form of it — in
// particular, never ".../source/main" twice, and never lose its query to a
// fragment in the input.
func TestSourceMethods_AcceptSourceURI(t *testing.T) {
	ctx := context.Background()
	completionURI := "/sap/bc/adt/abapsource/codecompletion/proposal?" +
		url.Values{"uri": {suffixTestProgURI + "/source/main#start=3,4"}}.Encode()
	corrNr := "?corrNr=" + suffixTestTransport

	tests := []struct {
		name string
		bare string
		call func(c adt.Client, uri string) error
		want []string
	}{
		{
			name: "GetSource",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetSource(ctx, uri); return err },
			want: []string{"GET " + suffixTestProgURI + "/source/main"},
		},
		{
			name: "GetClassDefinition",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetClassDefinition(ctx, uri); return err },
			want: []string{
				"GET " + suffixTestClassURI + "/objectstructure",
				"GET " + suffixTestClassURI + "/source/main",
			},
		},
		{
			name: "GetIncludeSource",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.GetIncludeSource(ctx, uri, suffixTestInclude)
				return err
			},
			want: []string{"GET " + suffixTestClassURI + "/includes/" + suffixTestInclude},
		},
		{
			name: "SetIncludeSource",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.SetIncludeSource(ctx, uri, suffixTestInclude, "CLASS lcl DEFINITION.", "", suffixTestTransport, "")
				return err
			},
			want: []string{"PUT " + suffixTestClassURI + "/includes/" + suffixTestInclude + corrNr},
		},
		{
			name: "CreateTestInclude",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error {
				return c.CreateTestInclude(ctx, uri, "LH", suffixTestTransport)
			},
			want: []string{"POST " + suffixTestClassURI + "/includes?corrNr=" + suffixTestTransport + "&lockHandle=LH"},
		},
		{
			name: "SetSource",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.SetSource(ctx, uri, "REPORT ztest.", "", suffixTestTransport, "")
				return err
			},
			want: []string{"PUT " + suffixTestProgURI + "/source/main" + corrNr},
		},
		{
			name: "GetVersionHistory program",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetVersionHistory(ctx, uri); return err },
			want: []string{"GET " + suffixTestProgURI + "/source/main/versions"},
		},
		{
			name: "GetVersionHistory class",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetVersionHistory(ctx, uri); return err },
			want: []string{
				"GET " + suffixTestClassURI + "/includes/definitions/versions",
				"GET " + suffixTestClassURI + "/includes/implementations/versions",
			},
		},
		{
			name: "DiffActiveInactive",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error { _, err := c.DiffActiveInactive(ctx, uri); return err },
			want: []string{
				"GET " + suffixTestProgURI + "/source/main?version=active",
				"GET " + suffixTestProgURI + "/source/main?version=inactive",
			},
		},
		{
			name: "GetCompletions",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.GetCompletions(ctx, uri, "REPORT ztest.\nWRITE ", 3, 4)
				return err
			},
			want: []string{"POST " + completionURI},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, take := newSourcePathRecorder(t)
			client := adt.NewClient(newTestConfig(srv.URL))
			want := slices.Clone(tt.want)
			slices.Sort(want)
			for _, in := range sourceURIInputForms(tt.bare) {
				if err := tt.call(client, in); err != nil {
					t.Errorf("input %q: unexpected error: %v", in, err)
				}
				if got := take(); !slices.Equal(got, want) {
					t.Errorf("input %q: requests\n got %q\nwant %q", in, got, want)
				}
			}
		})
	}
}
```

Notes for the implementer:
- `csrfEndpoint` and `newTestConfig` already exist in `adt/client_test.go` (package `adt_test`); do not redeclare them.
- The discovery GET goes to `csrfEndpoint` as well, which is why that path is excluded from recording; an empty discovery body makes `sourceContentType` fall back to `text/plain`, which is fine here.
- `url.Values.Encode` sorts keys, so `CreateTestInclude`'s query is `corrNr=…&lockHandle=…`.
- Keep every closure that gofmt would wrap in multi-line form; run `gofmt -l ./adt` before committing.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./adt/ -run TestSourceMethods_AcceptSourceURI`
Expected: FAIL. For every method, the suffixed inputs (`/source/main`, `/source/main/`, `/SOURCE/MAIN`) record a doubled path, e.g.
`input "/sap/bc/adt/programs/programs/ztest/source/main": requests got ["GET /sap/bc/adt/programs/programs/ztest/source/main/source/main"]`.
The `#start=42,5` form fails for every method *except* `GetSource` and `GetClassDefinition`'s source read: there the fragment cuts the URL right after `…/source/main`, so the request is accidentally identical. That case is guarded by `TestBareObjectURI` (Step 3), not by this test. The bare input passes for every method. Record the failing-subtest list in the report.

- [ ] **Step 3: Write the failing helper test**

Create `adt/source_uri_internal_test.go`:

```go
package adt

import "testing"

// TestBareObjectURI pins which inputs bareObjectURI reduces to the bare
// object URI and which it must leave alone (adtler#210).
func TestBareObjectURI(t *testing.T) {
	const prog = "/sap/bc/adt/programs/programs/ztest"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare", prog, prog},
		{"bare with trailing slash", prog + "/", prog},
		{"source suffix", prog + "/source/main", prog},
		{"source suffix with trailing slash", prog + "/source/main/", prog},
		{"upper-case source suffix", prog + "/SOURCE/MAIN", prog},
		{"source suffix with position fragment", prog + "/source/main#start=42,5", prog},
		{"bare with fragment", prog + "#start=1,0", prog},
		// No query form ever worked here (every entry point appends a
		// sub-path after the input), so a query is dropped.
		{"source suffix with query", prog + "/source/main?version=inactive", prog},
		{"function group include", "/sap/bc/adt/programs/includes/zinclude/source/main", "/sap/bc/adt/programs/includes/zinclude"},
		{"function module", "/sap/bc/adt/functions/groups/zfg/fmodules/zfm/source/main", "/sap/bc/adt/functions/groups/zfg/fmodules/zfm"},
		{"name ending in source is not a suffix", "/sap/bc/adt/programs/programs/zsource", "/sap/bc/adt/programs/programs/zsource"},
		{"longer last segment is not a suffix", prog + "/source/mainx", prog + "/source/mainx"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bareObjectURI(tt.in); got != tt.want {
				t.Errorf("bareObjectURI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
```

Add two rows to the table in `TestResolveTextElementPath` (`adt/textelements_test.go:49`):

```go
		{"/sap/bc/adt/programs/programs/ZTEST/source/main", "/sap/bc/adt/textelements/programs/ZTEST"},
		{"/sap/bc/adt/oo/classes/ZCL_TEST/source/main#start=3,0", "/sap/bc/adt/textelements/classes/ZCL_TEST"},
```

- [ ] **Step 4: Run them to verify they fail**

Add a pass-through stub so the package compiles — at the end of `adt/source.go`:

```go
func bareObjectURI(uri string) string { return uri }
```

Run: `go test ./adt/ -run "TestBareObjectURI|TestResolveTextElementPath"`
Expected: FAIL on every row whose `want` differs from `in`, and on both new `TestResolveTextElementPath` rows (doubled `…/ZTEST/source/main` in the result). Step 5 replaces the stub.

- [ ] **Step 5: Implement `bareObjectURI`**

Replace the stub in `adt/source.go` with this, placed directly after `isDDLSourceURI`:

```go
// sourceMainSuffix is the sub-path ADT serves an object's main source at.
const sourceMainSuffix = "/source/main"

// bareObjectURI reduces a URI that may point at an object's main source to
// the bare object URI the source methods build their paths from. ADT itself
// hands out source URIs — syntax-check messages and navigation targets carry
// ".../source/main#start=L,C" — so callers pass them back, and appending
// "/source/main" again yields a path SAP answers with 404 (adtler#210).
//
// On top of normalizeObjectURI (query, fragment, trailing slash) it strips a
// trailing "/source/main", case-insensitively. Dropping the query loses
// nothing that worked: every caller appends a sub-path after the URI, which
// turned any query into garbage before.
func bareObjectURI(uri string) string {
	uri = normalizeObjectURI(uri)
	if n := len(uri) - len(sourceMainSuffix); n >= 0 && strings.EqualFold(uri[n:], sourceMainSuffix) {
		uri = uri[:n]
	}
	return uri
}
```

Then replace the literal `"/source/main"` in the path builders with `sourceMainSuffix`: in `adt/source.go` (`GetSource`, `setSourceWithLockHeader`, `setSourceWithLockParam`), in `adt/version.go` (`GetVersionHistory`: `objectURI + sourceMainSuffix + "/versions"`; `getSourceWithVersion`: `objectURI + sourceMainSuffix + "?version=" + version`) and in `adt/completion.go` (`objectURI + sourceMainSuffix + "#start=" + …`). Comments that mention `/source/main` stay as they are.

- [ ] **Step 6: Call it at each entry point**

Add as the first statement of each function below, before anything else uses `objectURI`:

```go
	objectURI = bareObjectURI(objectURI)
```

- `GetSource` (`adt/source.go`)
- `GetClassDefinition` (`adt/source.go`) — it calls `GetSource` with the normalised value; the second normalisation there is a no-op
- `GetIncludeSource` (`adt/source.go`)
- `SetIncludeSource` (`adt/source.go`)
- `CreateTestInclude` (`adt/source.go`)
- `SetSource` (`adt/source.go`) — before `ensureCSRF`, so `trySetSource`, `isDDLSourceURI` and `sourceContentType` all see the bare URI
- `GetVersionHistory` (`adt/version.go`) — before `isClassURI`
- `DiffActiveInactive` (`adt/version.go`)
- `GetCompletions` (`adt/completion.go`) — put the normalisation above the existing SAP-handler comment
- `resolveTextElementPath` (`adt/textelements.go`) — one place covers `GetTextElements`, `SetTextElements` and `TextElementLockURI`

- [ ] **Step 7: Document the accepted forms on the interfaces**

In `adt/client.go`, extend the `SourceClient` godoc:

```go
// SourceClient reads and writes ABAP source code.
//
// Methods taking an objectURI accept either the bare object URI
// ("/sap/bc/adt/programs/programs/ztest") or its main-source URI as ADT hands
// it out (".../source/main", optionally with a "#start=L,C" fragment); both
// address the same object. A query string on objectURI is ignored.
type SourceClient interface {
```

and the `VersionClient` godoc (keep its existing first line if it has one, add the paragraph):

```go
// GetVersionHistory and DiffActiveInactive accept either the bare object URI
// or its ".../source/main" URI, like the SourceClient methods.
// GetVersionSource is different: it takes a version content URI from
// VersionInfo and uses it as given.
```

Also add one sentence to the `TextElementLockURI` godoc (`adt/textelements.go:139`): `A ".../source/main" URI is accepted and reduced to the object URI.`

- [ ] **Step 8: Run the tests and verify they pass**

Run: `go test ./adt/ -run "TestBareObjectURI|TestSourceMethods_AcceptSourceURI|TestResolveTextElementPath" -v`
Expected: PASS for every row and every sub-test.

Run: `go test ./...`
Expected: PASS.

Run: `go build -tags integration ./adt/... && go vet -tags integration ./adt/... && gofmt -l ./adt`
Expected: no output.

Run: `golangci-lint run --enable dupl,goconst,gocyclo ./...`
Expected: no findings. If `golangci-lint` is not installed locally, say so in the report; CI runs it.

- [ ] **Step 9: Verify the regression tests guard the fix**

Temporarily make `bareObjectURI` return its input unchanged (`return uri` as its first line), run `go test ./adt/ -run "TestBareObjectURI|TestSourceMethods_AcceptSourceURI|TestResolveTextElementPath"`, confirm all three FAIL, then restore the function. Report the failing sub-test count.

- [ ] **Step 10: Commit**

```bash
git add adt/source.go adt/version.go adt/completion.go adt/textelements.go adt/client.go adt/textelements_test.go adt/source_uri_internal_test.go adt/source_uri_suffix_test.go
git commit -m "fix(#210): accept source URIs in source methods instead of doubling /source/main

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Multi-system integration test

**Files:**
- Create: `adt/source_uri_suffix_integration_test.go` (build tag `integration`, package `adt_test`)

**Interfaces:**
- Consumes: Task 1's behaviour through the public API only. Existing integration helpers: `eachSystem(t) []integrationSystem` (`adt/integration_helpers_test.go:162`); `integrationSystem` with fields `Name string`, `Client adt.Client`, `Config sapmcpconfig.SAPSystem`; `searchFixtures(t, ctx, sys, adtType) []adt.ObjectInfo` (`adt/rap_source_integration_test.go:50`), which fails on an SAP error answer and skips only when SAP was never reached; `redact(text, host, name string) string` (`adt/rap_source_integration_test.go:24`).
- Produces: `TestSourceURISuffix_Integration`.

- [ ] **Step 1: Write the integration test**

Create `adt/source_uri_suffix_integration_test.go`:

```go
//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestSourceURISuffix_Integration is the live half of adtler#210: GetSource,
// and the methods that build further sub-paths, must return the same result
// for an object's bare URI and for its ".../source/main" URI. Before the fix
// the suffixed form produced ".../source/main/source/main" and SAP answered
// 404.
//
// Fixtures are discovered, never hardcoded: the test searches the system for
// objects of each type and uses the first one whose bare URI reads. Only
// counts and types are logged, never object names, and every error passes
// through redact first.
func TestSourceURISuffix_Integration(t *testing.T) {
	types := []string{
		"PROG/P",  // program
		"CLAS/OC", // class
		"INTF/OI", // interface
		"FUGR/FF", // function module
		"FUGR/I",  // function group include
	}
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			for _, adtType := range types {
				t.Run(adtType, func(t *testing.T) {
					obj, bare := findReadableSource(t, ctx, sys, adtType)
					clean := func(err error) string { return redact(err.Error(), sys.Config.Host, obj.Name) }
					suffixed := obj.URI + "/source/main"

					got, err := sys.Client.GetSource(ctx, suffixed)
					if err != nil {
						t.Fatalf("GetSource with the source URI: %s", clean(err))
					}
					if got.Source != bare.Source {
						t.Errorf("GetSource: source differs between bare and source URI (%d vs %d bytes)",
							len(bare.Source), len(got.Source))
					}
					if got.ETag != bare.ETag {
						t.Errorf("GetSource: ETag differs between bare and source URI")
					}

					t.Run("GetVersionHistory", func(t *testing.T) {
						want, err := sys.Client.GetVersionHistory(ctx, obj.URI)
						if err != nil {
							t.Skipf("no version history through the bare URI either, nothing to compare: %s", clean(err))
						}
						have, err := sys.Client.GetVersionHistory(ctx, suffixed)
						if err != nil {
							t.Fatalf("GetVersionHistory with the source URI: %s", clean(err))
						}
						if len(have) != len(want) {
							t.Errorf("GetVersionHistory: %d versions with the source URI, %d with the bare URI", len(have), len(want))
						}
					})

					if adtType == "CLAS/OC" {
						t.Run("GetClassDefinition", func(t *testing.T) {
							want, err := sys.Client.GetClassDefinition(ctx, obj.URI)
							if err != nil {
								t.Fatalf("GetClassDefinition with the bare URI: %s", clean(err))
							}
							have, err := sys.Client.GetClassDefinition(ctx, suffixed)
							if err != nil {
								t.Fatalf("GetClassDefinition with the source URI: %s", clean(err))
							}
							if have.Source != want.Source {
								t.Errorf("GetClassDefinition: definition differs between bare and source URI (%d vs %d bytes)",
									len(want.Source), len(have.Source))
							}
						})
					}
				})
			}
		})
	}
}

// findReadableSource returns the first object of adtType whose bare URI
// GetSource can read, together with that read. It fails when SAP answers the
// search with an error or none of the candidates is readable, and skips only
// when the system holds no such object.
func findReadableSource(t *testing.T, ctx context.Context, sys integrationSystem, adtType string) (adt.ObjectInfo, *adt.SourceResult) {
	t.Helper()
	results := searchFixtures(t, ctx, sys, adtType)
	t.Logf("search returned %d %s candidate(s)", len(results), adtType)
	var lastErr string
	for _, r := range results {
		if r.URI == "" {
			continue
		}
		src, err := sys.Client.GetSource(ctx, r.URI)
		if err == nil {
			return r, src
		}
		lastErr = redact(err.Error(), sys.Config.Host, r.Name)
	}
	if lastErr == "" {
		t.Skipf("no %s object on this system", adtType)
	}
	t.Fatalf("none of the %d %s candidates is readable through its bare URI; last error: %s",
		len(results), adtType, lastErr)
	return adt.ObjectInfo{}, nil
}
```

Implementation note: the plan originally listed `PROG/I`. The search for `PROG/I` returned only workbench link entries on the S/4 system, which are not readable through their bare URI, and no hits on the ECC system, so the implementation uses `FUGR/I` (function group includes), the include case named in the issue.

Before writing it, read `redact` in `adt/rap_source_integration_test.go:24` to confirm its parameter order `(text, host, name)`; adjust the calls if it differs.

- [ ] **Step 2: Build, vet and format-check**

Run: `go build -tags integration ./adt/... && go vet -tags integration ./adt/... && gofmt -l ./adt`
Expected: no output.

- [ ] **Step 3: Run it live**

`~/.config/sap-mcp/systems.json` exists on this machine. Read the keys of the ECC and the S/4 system from it locally (never write them into any file, commit, comment or report), then run:

```bash
SAP_INTEGRATION_SYSTEMS="<ecc-key>,<s4-key>" go test -tags=integration -v -run TestSourceURISuffix_Integration ./adt/...
```

Expected: PASS for all five types on both systems. A SKIP is acceptable only with "no … object on this system" or the version-history "nothing to compare" message; report every skip with its reason.

- [ ] **Step 4: Verify the integration test fails without the fix**

Temporarily make `bareObjectURI` return its input unchanged, re-run the command from Step 3, confirm FAIL with `GetSource with the source URI: … 404`, then restore the function and confirm `git diff` shows no change to `adt/source.go`. Report per-system results by system type only (ECC / S/4), never keys or hosts.

- [ ] **Step 5: Commit**

```bash
git add adt/source_uri_suffix_integration_test.go
git commit -m "test(#210): live check that source methods accept the source URI on every system

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## After the tasks (controller, not an implementer task)

Final whole-branch review, then push and open the PR per CLAUDE.md step 1: `Closes #210`, `Related: Hochfrequenz/aibap.mcp#<N>` for a consumer issue (to be filed — the adtler issue names none yet), label `needs:integration-test`, the live integration results in the PR description. Then request a Copilot review and work through its comments.
