# Issue #35: Discovery-First Content Negotiation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the ADT discovery cache into source operations, ATC checks, and read-path ETag resolution, so that Accept/Content-Type headers are driven by what the server advertises, with hardcoded fallbacks preserved for compatibility.

**Architecture:** Add a small `sourceContentType` helper that delegates to the existing `NegotiateContentType`. Wire it into all four source-operation functions. Ensure `doReadWith` triggers the CSRF+discovery preflight before any first read (today it only triggers on first mutation). Use `NegotiateContentType` for the ATC POST's `Content-Type`. All new behaviour falls back cleanly to today's hardcoded values when discovery has no entry.

**Tech Stack:** Go 1.25+, `net/http`, `encoding/xml`, `httptest`, `eachSystem(t)` for multi-system integration tests.

**Design doc:** `docs/superpowers/specs/2026-04-10-issue-35-discovery-content-negotiation-design.md`

**Branch:** `refactor/35-discovery-content-negotiation`

---

## File structure

| File | Change |
|---|---|
| `adt/source.go` | Add `sourceContentType` helper; wire into `GetSource`, `GetIncludeSource`, `setSourceWithLockHeader`, `setSourceWithLockParam`, `SetIncludeSource` |
| `adt/client.go` | In `doReadWith`, trigger CSRF+discovery preflight before the first read if discovery cache is empty |
| `adt/atc.go` | Use `NegotiateContentType` for the ATC POST `Content-Type` |
| `adt/source_test.go` | Unit tests: discovery-driven Accept/Content-Type in source ops |
| `adt/client_test.go` | Unit test: eager discovery load on first read |
| `adt/atc_test.go` | Unit test: discovery-driven ATC Content-Type |
| `adt/source_discovery_integration_test.go` | New integration test: source ops against R/3 + S/4 |
| `adt/atc_discovery_integration_test.go` | New integration test: ATC on R/3 + S/4 (may close #12) |

---

## Task 1: Add `sourceContentType` helper + unit test

**Files:**
- Modify: `adt/source.go` (add helper)
- Modify: `adt/source_test.go` (add test)

- [ ] **Step 1.1: Write the failing test**

Add to `adt/source_test.go` (append to existing file):

```go
func TestSourceContentType_DiscoveryEmpty_FallsBackToTextPlain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Empty discovery response
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClientForTest(cfg)

	got := client.SourceContentTypeForTest("/sap/bc/adt/programs/programs/ZTEST")
	if got != "text/plain" {
		t.Errorf("empty discovery: got %q, want %q", got, "text/plain")
	}
}

func TestSourceContentType_DiscoveryAdvertisesType_UsesIt(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain; charset=utf-8</app:accept>
      <app:accept>text/plain</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/discovery" {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClientForTest(cfg)

	// Force discovery load
	_ = client.LoadDiscoveryForTest(context.Background())

	got := client.SourceContentTypeForTest("/sap/bc/adt/programs/programs/ZTEST")
	if got != "text/plain; charset=utf-8" {
		t.Errorf("discovery-advertised: got %q, want %q", got, "text/plain; charset=utf-8")
	}
}
```

These tests need test-only accessors because `sourceContentType` is unexported and `httpClient` is not directly constructible. Before adding the tests, add these exports to a new file `adt/export_test.go`:

```go
package adt

import "context"

// NewClientForTest creates a Client for tests in the external `adt_test` package.
func NewClientForTest(cfg sapmcpconfig.SAPSystem) Client {
	return NewClient(cfg)
}

// SourceContentTypeForTest exposes sourceContentType for unit tests.
func (c *httpClient) SourceContentTypeForTest(endpoint string) string {
	return c.sourceContentType(endpoint)
}

// LoadDiscoveryForTest triggers a CSRF+discovery preflight for unit tests.
func (c *httpClient) LoadDiscoveryForTest(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetchCSRFToken(ctx)
}
```

Note: `export_test.go` is only compiled during tests. If the file already exists, append to it instead. Import `sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"` if needed.

If `Client` is an interface and the test helpers need a concrete `*httpClient`, adjust to return `*httpClient` directly from `NewClientForTest` by casting internally.

- [ ] **Step 1.2: Run the tests to verify they fail**

Run: `go test ./adt/ -run 'TestSourceContentType' -v`
Expected: FAIL with "undefined: adt.NewClientForTest" or "undefined: sourceContentType"

- [ ] **Step 1.3: Implement `sourceContentType`**

Add to `adt/source.go` immediately after the `FetchETag` function (after line 42):

```go
// sourceContentType returns the Accept / Content-Type to use for source
// operations on the given endpoint. It consults the ADT discovery cache
// first and falls back to "text/plain" when the endpoint is absent from
// discovery.
//
// The fallback matches today's hardcoded value, so callers on systems
// where discovery has no source-endpoint entry behave exactly as before.
func (c *httpClient) sourceContentType(endpoint string) string {
	return c.NegotiateContentType(endpoint,
		[]string{"text/plain; charset=utf-8", "text/plain"},
		"text/plain")
}
```

Then create `adt/export_test.go` (or append to it if it exists) with the test helpers from Step 1.1.

- [ ] **Step 1.4: Run the tests to verify they pass**

Run: `go test ./adt/ -run 'TestSourceContentType' -v`
Expected: PASS (both tests)

- [ ] **Step 1.5: Run the full unit test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 1.6: Commit**

```bash
git add adt/source.go adt/source_test.go adt/export_test.go
git commit -m "feat(#35): add sourceContentType helper for discovery-driven source ops

Delegates to NegotiateContentType with text/plain as the hardcoded
fallback. Not yet wired into source operations — subsequent commits
will replace the hardcoded headers in GetSource/SetSource/include ops.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Eager discovery load in `doReadWith`

**Files:**
- Modify: `adt/client.go` (`doReadWith`)
- Modify: `adt/client_test.go` (add test)

- [ ] **Step 2.1: Write the failing test**

Add to `adt/client_test.go`:

```go
func TestDoReadLoadsDiscoveryBeforeFirstRead(t *testing.T) {
	var discoveryCalls atomic.Int32
	var readCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/discovery" {
			discoveryCalls.Add(1)
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0"?><app:service xmlns:app="http://www.w3.org/2007/app"/>`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/source/main") {
			readCalls.Add(1)
			w.Header().Set("ETag", `"etag-xyz"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("REPORT ZTEST."))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := newTestConfig(srv.URL)
	client := adt.NewClient(cfg)

	_, err := client.GetSource(context.Background(), "/sap/bc/adt/programs/programs/ZTEST")
	if err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if discoveryCalls.Load() != 1 {
		t.Errorf("discovery calls: got %d, want 1", discoveryCalls.Load())
	}
	if readCalls.Load() != 1 {
		t.Errorf("read calls: got %d, want 1", readCalls.Load())
	}
}
```

- [ ] **Step 2.2: Run the test to verify it fails**

Run: `go test ./adt/ -run TestDoReadLoadsDiscoveryBeforeFirstRead -v`
Expected: FAIL with `discovery calls: got 0, want 1` — because `doReadWith` currently does not trigger CSRF/discovery preflight.

- [ ] **Step 2.3: Modify `doReadWith` to preflight CSRF/discovery on first use**

Open `adt/client.go`. Find the `doReadWith` function (around line 322). Insert a preflight block at the very top of the function body, before the `makeReq` closure:

**Before** (first few lines of the function):
```go
func (c *httpClient) doReadWith(ctx context.Context, hc *http.Client, path string, headers map[string]string) (*http.Response, error) {
	path = encodeNamespacePath(path)
	makeReq := func() (*http.Request, error) {
```

**After**:
```go
func (c *httpClient) doReadWith(ctx context.Context, hc *http.Client, path string, headers map[string]string) (*http.Response, error) {
	path = encodeNamespacePath(path)

	// Ensure CSRF+discovery have been fetched at least once. This is the
	// same mechanism doMutateWith uses; we apply it to reads as well so
	// that content negotiation (acceptHeaderForURI, sourceContentType)
	// has discovery data available on the very first request.
	c.mu.Lock()
	if c.csrfToken == "" {
		if err := c.fetchCSRFToken(ctx); err != nil {
			c.mu.Unlock()
			return nil, err
		}
	}
	c.mu.Unlock()

	makeReq := func() (*http.Request, error) {
```

- [ ] **Step 2.4: Run the test to verify it passes**

Run: `go test ./adt/ -run TestDoReadLoadsDiscoveryBeforeFirstRead -v`
Expected: PASS

- [ ] **Step 2.5: Run the full unit test suite**

Run: `go test ./...`
Expected: PASS (watch for any existing test that asserts discovery is NOT called on reads — fix it if so by updating the expected call count).

- [ ] **Step 2.6: Commit**

```bash
git add adt/client.go adt/client_test.go
git commit -m "feat(#35): preflight CSRF+discovery in doReadWith

Discovery must be populated before any first request — not just the
first mutation — so that acceptHeaderForURI and sourceContentType
have discovery data on reads too.

Mirrors the same CSRF-empty check doMutateWith already performs.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Wire `sourceContentType` into `GetSource`

**Files:**
- Modify: `adt/source.go` (`GetSource`)
- Modify: `adt/source_test.go` (add test)

- [ ] **Step 3.1: Write the failing test**

Add to `adt/source_test.go`:

```go
func TestGetSource_UsesDiscoveryAdvertisedAcceptHeader(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/discovery" {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/source/main") {
			capturedAccept = r.Header.Get("Accept")
			w.Header().Set("ETag", `"etag-1"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("REPORT ZTEST."))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetSource(context.Background(), "/sap/bc/adt/programs/programs/ZTEST"); err != nil {
		t.Fatalf("GetSource: %v", err)
	}
	if capturedAccept != "text/plain; charset=utf-8" {
		t.Errorf("Accept header: got %q, want %q", capturedAccept, "text/plain; charset=utf-8")
	}
}
```

- [ ] **Step 3.2: Run the test to verify it fails**

Run: `go test ./adt/ -run TestGetSource_UsesDiscoveryAdvertisedAcceptHeader -v`
Expected: FAIL with `Accept header: got "text/plain", want "text/plain; charset=utf-8"`

- [ ] **Step 3.3: Wire `sourceContentType` into `GetSource`**

In `adt/source.go`, change `GetSource` (lines 44-58):

**Before**:
```go
func (c *httpClient) GetSource(ctx context.Context, objectURI string) (*SourceResult, error) {
	resp, err := c.doRead(ctx, objectURI+"/source/main", map[string]string{"Accept": "text/plain"})
```

**After**:
```go
func (c *httpClient) GetSource(ctx context.Context, objectURI string) (*SourceResult, error) {
	accept := c.sourceContentType(objectURI)
	resp, err := c.doRead(ctx, objectURI+"/source/main", map[string]string{"Accept": accept})
```

Note: we pass the parent `objectURI` (not the full `/source/main` path) to `sourceContentType`, because the discovery cache is keyed by the parent collection href.

- [ ] **Step 3.4: Run the test to verify it passes**

Run: `go test ./adt/ -run TestGetSource_UsesDiscoveryAdvertisedAcceptHeader -v`
Expected: PASS

- [ ] **Step 3.5: Run existing GetSource tests to verify no regression**

Run: `go test ./adt/ -run TestGetSource -v`
Expected: PASS for all existing tests (the new one plus any pre-existing ones).

- [ ] **Step 3.6: Commit**

```bash
git add adt/source.go adt/source_test.go
git commit -m "feat(#35): wire sourceContentType into GetSource

GetSource now consults discovery for the Accept header and falls back
to text/plain when discovery has no entry for the endpoint.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Wire `sourceContentType` into `GetIncludeSource`

**Files:**
- Modify: `adt/source.go` (`GetIncludeSource`)
- Modify: `adt/source_test.go` (add test)

- [ ] **Step 4.1: Write the failing test**

Add to `adt/source_test.go`:

```go
func TestGetIncludeSource_UsesDiscoveryAdvertisedAcceptHeader(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/oo/classes">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/discovery" {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/includes/testclasses") {
			capturedAccept = r.Header.Get("Accept")
			w.Header().Set("ETag", `"etag-inc"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("CLASS ltcl_test DEFINITION."))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetIncludeSource(context.Background(), "/sap/bc/adt/oo/classes/ZCL_TEST", "testclasses"); err != nil {
		t.Fatalf("GetIncludeSource: %v", err)
	}
	if capturedAccept != "text/plain; charset=utf-8" {
		t.Errorf("Accept: got %q, want %q", capturedAccept, "text/plain; charset=utf-8")
	}
}
```

- [ ] **Step 4.2: Run the test to verify it fails**

Run: `go test ./adt/ -run TestGetIncludeSource_UsesDiscoveryAdvertisedAcceptHeader -v`
Expected: FAIL with Accept mismatch.

- [ ] **Step 4.3: Wire `sourceContentType` into `GetIncludeSource`**

In `adt/source.go`, modify `GetIncludeSource`:

**Before** (line 152):
```go
	resp, err := c.doRead(ctx, path, map[string]string{"Accept": "text/plain"})
```

**After**:
```go
	accept := c.sourceContentType(objectURI)
	resp, err := c.doRead(ctx, path, map[string]string{"Accept": accept})
```

- [ ] **Step 4.4: Run the test to verify it passes**

Run: `go test ./adt/ -run TestGetIncludeSource_UsesDiscoveryAdvertisedAcceptHeader -v`
Expected: PASS

- [ ] **Step 4.5: Run full unit test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 4.6: Commit**

```bash
git add adt/source.go adt/source_test.go
git commit -m "feat(#35): wire sourceContentType into GetIncludeSource

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Wire `sourceContentType` into `setSourceWithLockHeader` and `setSourceWithLockParam`

**Files:**
- Modify: `adt/source.go` (`setSourceWithLockHeader`, `setSourceWithLockParam`)
- Modify: `adt/source_test.go` (add test)

- [ ] **Step 5.1: Write the failing test**

Add to `adt/source_test.go`:

```go
func TestSetSource_UsesDiscoveryAdvertisedContentType(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/programs/programs">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedContentType, capturedAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/discovery" {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/source/main") {
			capturedContentType = r.Header.Get("Content-Type")
			capturedAccept = r.Header.Get("Accept")
			w.Header().Set("ETag", `"etag-new"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SetSource(context.Background(),
		"/sap/bc/adt/programs/programs/ZTEST",
		"REPORT ZTEST.",
		"lock1", "", `"etag-old"`)
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	if capturedContentType != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type: got %q, want %q", capturedContentType, "text/plain; charset=utf-8")
	}
	if capturedAccept != "text/plain; charset=utf-8" {
		t.Errorf("Accept: got %q, want %q", capturedAccept, "text/plain; charset=utf-8")
	}
}
```

- [ ] **Step 5.2: Run the test to verify it fails**

Run: `go test ./adt/ -run TestSetSource_UsesDiscoveryAdvertisedContentType -v`
Expected: the `Accept` check fails (currently hardcoded to `"text/plain"` even though the test's Content-Type assertion happens to match today's hardcoded value — BUT the fail mode is Accept, and confirms discovery isn't consulted).

If both assertions accidentally pass (because today's hardcoded `text/plain; charset=utf-8` matches), change the test's discovery XML to advertise a type that definitely differs (e.g. `text/plain; charset=utf-8; version=2`). This guarantees the test fails pre-implementation.

- [ ] **Step 5.3: Wire `sourceContentType` into both helpers**

In `adt/source.go`, modify `setSourceWithLockHeader` (lines 315-330):

**Before**:
```go
func (c *httpClient) setSourceWithLockHeader(ctx context.Context, objectURI, source, lockHandle, transport, etag string) (string, error) {
	headers := map[string]string{
		"Content-Type":          "text/plain; charset=utf-8",
		"Accept":                "text/plain",
		"If-Match":              etag,
		"X-sap-adt-sessiontype": "stateful",
	}
```

**After**:
```go
func (c *httpClient) setSourceWithLockHeader(ctx context.Context, objectURI, source, lockHandle, transport, etag string) (string, error) {
	ct := c.sourceContentType(objectURI)
	headers := map[string]string{
		"Content-Type":          ct,
		"Accept":                ct,
		"If-Match":              etag,
		"X-sap-adt-sessiontype": "stateful",
	}
```

Apply the exact same change to `setSourceWithLockParam` (lines 332-351).

- [ ] **Step 5.4: Run the test to verify it passes**

Run: `go test ./adt/ -run TestSetSource_UsesDiscoveryAdvertisedContentType -v`
Expected: PASS

- [ ] **Step 5.5: Run the ETag charset integration-safe test**

The existing `TestSetSource_ETagCharsetRetry` (in `adt/source_etag_charset_integration_test.go` or `source_test.go`) must still pass because the retry path is preserved.

Run: `go test ./adt/ -run SetSource -v`
Expected: PASS for all SetSource-related tests.

- [ ] **Step 5.6: Commit**

```bash
git add adt/source.go adt/source_test.go
git commit -m "feat(#35): wire sourceContentType into SetSource helpers

Both setSourceWithLockHeader and setSourceWithLockParam now derive
Content-Type/Accept from discovery, falling back to text/plain when
the endpoint has no discovery entry.

The 412 ETag charset retry in SetSource itself is unchanged.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Wire `sourceContentType` into `SetIncludeSource`

**Files:**
- Modify: `adt/source.go` (`SetIncludeSource`)
- Modify: `adt/source_test.go` (add test)

- [ ] **Step 6.1: Write the failing test**

Add to `adt/source_test.go`:

```go
func TestSetIncludeSource_UsesDiscoveryAdvertisedContentType(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/oo/classes">
      <app:accept>text/plain; charset=utf-8</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/discovery" {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
			return
		}
		if r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/includes/testclasses") {
			capturedCT = r.Header.Get("Content-Type")
			w.Header().Set("ETag", `"etag-new"`)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.SetIncludeSource(context.Background(),
		"/sap/bc/adt/oo/classes/ZCL_TEST", "testclasses",
		"CLASS ltcl_test DEFINITION.",
		"lock1", "", `"etag-old"`)
	if err != nil {
		t.Fatalf("SetIncludeSource: %v", err)
	}
	if capturedCT != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type: got %q, want %q", capturedCT, "text/plain; charset=utf-8")
	}
}
```

- [ ] **Step 6.2: Run the test to verify it fails**

Run: `go test ./adt/ -run TestSetIncludeSource_UsesDiscoveryAdvertisedContentType -v`
Expected: FAIL with Content-Type mismatch (or verify by changing the discovery-advertised type to something definitely different from today's hardcoded value).

- [ ] **Step 6.3: Wire `sourceContentType` into `SetIncludeSource`**

In `adt/source.go`, modify `SetIncludeSource` (around line 172):

**Before**:
```go
	headers := map[string]string{
		"Content-Type":          "text/plain; charset=utf-8",
		"Accept":                "text/plain",
		"X-sap-adt-sessiontype": "stateful",
	}
```

**After**:
```go
	ct := c.sourceContentType(objectURI)
	headers := map[string]string{
		"Content-Type":          ct,
		"Accept":                ct,
		"X-sap-adt-sessiontype": "stateful",
	}
```

- [ ] **Step 6.4: Run the test to verify it passes**

Run: `go test ./adt/ -run TestSetIncludeSource_UsesDiscoveryAdvertisedContentType -v`
Expected: PASS

- [ ] **Step 6.5: Run full unit test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 6.6: Commit**

```bash
git add adt/source.go adt/source_test.go
git commit -m "feat(#35): wire sourceContentType into SetIncludeSource

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Wire `NegotiateContentType` into `RunATCCheck`

**Files:**
- Modify: `adt/atc.go`
- Modify: `adt/atc_test.go` (create if missing — otherwise append)

- [ ] **Step 7.1: Check whether `adt/atc_test.go` exists**

Run: `ls adt/atc_test.go 2>/dev/null || echo "MISSING"`

If missing, you'll create it in Step 7.2. If it exists, you'll append to it.

- [ ] **Step 7.2: Write the failing test**

If `adt/atc_test.go` does not exist, create it with this full content:

```go
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

func TestRunATCCheck_UsesDiscoveryAdvertisedContentType(t *testing.T) {
	discoveryXML := `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/atc/runs">
      <app:accept>application/vnd.sap.adt.atc.runs.v1+xml</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

	var capturedCT string
	worklistXML := `<?xml version="1.0"?><atcworklist:worklist xmlns:atcworklist="http://www.sap.com/adt/atc/worklist" atcworklist:id="0000000000"/>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sap/bc/adt/discovery":
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discoveryXML))
		case r.Method == http.MethodPost && r.URL.Path == "/sap/bc/adt/atc/runs":
			capturedCT = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sap/bc/adt/atc/worklists/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(worklistXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}
	if capturedCT != "application/vnd.sap.adt.atc.runs.v1+xml" {
		t.Errorf("Content-Type: got %q, want %q", capturedCT, "application/vnd.sap.adt.atc.runs.v1+xml")
	}
}

func TestRunATCCheck_FallbackContentTypeWhenDiscoveryEmpty(t *testing.T) {
	var capturedCT string
	worklistXML := `<?xml version="1.0"?><atcworklist:worklist xmlns:atcworklist="http://www.sap.com/adt/atc/worklist" atcworklist:id="0000000000"/>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/sap/bc/adt/discovery":
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			// Empty discovery body — no entries
		case r.Method == http.MethodPost && r.URL.Path == "/sap/bc/adt/atc/runs":
			capturedCT = r.Header.Get("Content-Type")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/sap/bc/adt/atc/worklists/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(worklistXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.RunATCCheck(context.Background(),
		[]string{"/sap/bc/adt/programs/programs/ZTEST"}, "DEFAULT")
	if err != nil {
		t.Fatalf("RunATCCheck: %v", err)
	}
	if capturedCT != "application/xml" {
		t.Errorf("Content-Type fallback: got %q, want %q", capturedCT, "application/xml")
	}
}
```

- [ ] **Step 7.3: Run the tests to verify they fail**

Run: `go test ./adt/ -run TestRunATCCheck_ -v`
Expected: FAIL for `TestRunATCCheck_UsesDiscoveryAdvertisedContentType` (captured Content-Type is `application/xml`, not the vendor type). The fallback test may already pass since `application/xml` is the current hardcoded value.

- [ ] **Step 7.4: Wire `NegotiateContentType` into `RunATCCheck`**

In `adt/atc.go`, find the POST to `/sap/bc/adt/atc/runs` (around line 76-82):

**Before**:
```go
	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/atc/runs?clientWait=false&worklistId="+worklistID,
		strings.NewReader(body),
		map[string]string{
			"Content-Type": "application/xml",
			"Accept":       "application/xml",
		},
	)
```

**After**:
```go
	ct := c.NegotiateContentType("/sap/bc/adt/atc/runs",
		[]string{"application/vnd.sap.adt.atc.runs.v1+xml", "application/xml"},
		"application/xml")
	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/atc/runs?clientWait=false&worklistId="+worklistID,
		strings.NewReader(body),
		map[string]string{
			"Content-Type": ct,
			"Accept":       "application/xml",
		},
	)
```

Rationale: `Accept` stays at `application/xml` because the ATC run endpoint's response is a small XML acknowledgement that `application/xml` reliably parses on both R/3 and S/4. Only the request body media type (which is the part suspected in #12) is discovery-driven.

- [ ] **Step 7.5: Run the tests to verify they pass**

Run: `go test ./adt/ -run TestRunATCCheck_ -v`
Expected: PASS (both tests)

- [ ] **Step 7.6: Run full unit test suite**

Run: `go test ./...`
Expected: PASS

- [ ] **Step 7.7: Commit**

```bash
git add adt/atc.go adt/atc_test.go
git commit -m "feat(#35, #12): discovery-driven Content-Type for RunATCCheck POST

The ATC runs endpoint may advertise a vendor MIME type in discovery.
Hardcoding application/xml is the root cause suspected for #12
(RunATCCheck HTTP 500 on R/3). Preserve the hardcoded fallback so
systems without a discovery entry behave as before.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Add source-operations integration test (R/3 + S/4)

**Files:**
- Create: `adt/source_discovery_integration_test.go`

- [ ] **Step 8.1: Create the integration test file**

Create `adt/source_discovery_integration_test.go` with this exact content:

```go
//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestSourceOperations_DiscoveryDriven verifies that Get/SetSource succeed
// on both R/3 and S/4 after the discovery-driven content negotiation
// refactor (adtler#35). This test exercises a PROG cycle end-to-end:
// lock → get → set → unlock. Any regression in the Accept/Content-Type
// wiring will surface as a 400/406/415/412 from SAP.
func TestSourceOperations_DiscoveryDriven(t *testing.T) {
	eachSystem(t, func(t *testing.T, client adt.Client, systemName string) {
		ctx := context.Background()

		// Use the shared TMP sandbox program created by the integration
		// fixture (see each_system_smoke_integration_test.go for the
		// convention). If this project uses a different fixture name,
		// adjust accordingly.
		progURI := tmpProgramURI(t, client, systemName)

		lockHandle, err := client.LockObject(ctx, progURI)
		if err != nil {
			t.Fatalf("%s: LockObject: %v", systemName, err)
		}
		defer func() {
			if err := client.UnlockObject(ctx, progURI, lockHandle); err != nil {
				t.Logf("%s: UnlockObject cleanup: %v", systemName, err)
			}
		}()

		result, err := client.GetSource(ctx, progURI)
		if err != nil {
			t.Fatalf("%s: GetSource: %v", systemName, err)
		}
		if result.Source == "" {
			t.Errorf("%s: empty source", systemName)
		}
		if result.ETag == "" {
			t.Errorf("%s: empty ETag", systemName)
		}

		// Append a harmless comment so SetSource has a real diff.
		newSource := strings.TrimRight(result.Source, "\n") + "\n* discovery-refactor-probe " + systemName + "\n"

		newETag, err := client.SetSource(ctx, progURI, newSource, lockHandle, "", result.ETag)
		if err != nil {
			t.Fatalf("%s: SetSource: %v", systemName, err)
		}
		if newETag == "" {
			t.Errorf("%s: empty ETag after SetSource", systemName)
		}
	})
}
```

**Note:** the helper `tmpProgramURI` is referenced but may not exist. Check first:

Run: `grep -l "tmpProgramURI" adt/*_integration_test.go 2>/dev/null`

If it does not exist, you must either:
1. Find the equivalent fixture in an existing integration test (search for `eachSystem` usage and PROG fixtures), or
2. Hardcode a known TMP program URI like `/sap/bc/adt/programs/programs/ZTMP_DISCOVERY_PROBE` and ensure it is created once via `CreateObject` at the top of the test (protecting with `object_exists` check).

Pick whichever matches existing patterns in the repo.

- [ ] **Step 8.2: Build the integration test suite**

Run: `go build -tags integration ./adt/...`
Expected: success — no compile errors.

- [ ] **Step 8.3: Commit (integration test is not run locally without SAP access)**

```bash
git add adt/source_discovery_integration_test.go
git commit -m "test(#35): integration test for discovery-driven source ops

Exercises Lock → GetSource → SetSource → Unlock cycle against R/3 and
S/4 via eachSystem. Runs under -tags=integration only.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Add ATC integration test (R/3 + S/4, may close #12)

**Files:**
- Create: `adt/atc_discovery_integration_test.go`

- [ ] **Step 9.1: Create the integration test**

Create `adt/atc_discovery_integration_test.go`:

```go
//go:build integration

package adt_test

import (
	"context"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRunATCCheck_DiscoveryDriven verifies that the RunATCCheck POST
// succeeds on both R/3 and S/4 after the discovery-driven Content-Type
// refactor (adtler#35). On R/3 this test may close adtler#12 if the
// previous HTTP 500 was caused by a wrong Content-Type.
func TestRunATCCheck_DiscoveryDriven(t *testing.T) {
	eachSystem(t, func(t *testing.T, client adt.Client, systemName string) {
		ctx := context.Background()

		progURI := tmpProgramURI(t, client, systemName)

		result, err := client.RunATCCheck(ctx, []string{progURI}, "DEFAULT")
		if err != nil {
			t.Fatalf("%s: RunATCCheck: %v", systemName, err)
		}
		if result == nil {
			t.Fatalf("%s: nil ATC result", systemName)
		}
		t.Logf("%s: ATC findings: %d", systemName, len(result.Findings))
	})
}
```

(If `tmpProgramURI` is missing, use the same fallback strategy as Task 8.)

- [ ] **Step 9.2: Build the integration test suite**

Run: `go build -tags integration ./adt/...`
Expected: success.

- [ ] **Step 9.3: Commit**

```bash
git add adt/atc_discovery_integration_test.go
git commit -m "test(#35, #12): integration test for discovery-driven RunATCCheck

Runs RunATCCheck against R/3 and S/4 via eachSystem. May close #12 if
the previous HTTP 500 was caused by the hardcoded Content-Type.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Full build, vet, and lint check

**Files:** none (verification only)

- [ ] **Step 10.1: Run the full Go build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 10.2: Run the full unit test suite**

Run: `go test ./...`
Expected: all tests PASS.

- [ ] **Step 10.3: Run go vet on the integration build**

Run: `go vet -tags integration ./adt/...`
Expected: success.

- [ ] **Step 10.4: Run golangci-lint with the CI flags**

Run: `golangci-lint run --enable dupl,goconst,gocyclo ./...`
Expected: zero issues. If `goconst` flags repeated strings (e.g. the discovery XML constant appears in multiple tests), hoist them to a `const` block at the top of `source_test.go`.

- [ ] **Step 10.5: Run the integration build**

Run: `go build -tags integration ./adt/...`
Expected: success.

- [ ] **Step 10.6: Commit any lint cleanups**

If any cleanup commits are needed:

```bash
git add <files>
git commit -m "chore(#35): lint cleanup after discovery refactor

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

Otherwise skip.

---

## Task 11: Open PR and file follow-up enhancement issue

**Files:** none (GitHub operations)

- [ ] **Step 11.1: Push the branch**

Run: `git push -u origin refactor/35-discovery-content-negotiation`
Expected: branch created on origin.

- [ ] **Step 11.2: Open the PR**

Run:

```bash
gh pr create --title "refactor(#35): discovery-first content negotiation for source ops + ATC" --body "$(cat <<'EOF'
## Summary

- Wire the existing ADT discovery cache into source operations (`GetSource`, `SetSource`, `GetIncludeSource`, `SetIncludeSource`) via a new `sourceContentType` helper.
- Wire `NegotiateContentType` into `RunATCCheck` for the POST body Content-Type — may close #12.
- Preflight CSRF+discovery in `doReadWith` so the first read request has discovery data available, not just the first mutation.
- All new behaviour falls back cleanly to today's hardcoded values when discovery has no entry for the endpoint.

Closes #35. May close #12 pending integration test on R/3.

## Test plan
- [x] `go test ./...`
- [x] `go build -tags integration ./adt/...`
- [x] `go vet -tags integration ./adt/...`
- [x] `golangci-lint run --enable dupl,goconst,gocyclo ./...`
- [ ] Integration test on R/3 (`TestSourceOperations_DiscoveryDriven`, `TestRunATCCheck_DiscoveryDriven`)
- [ ] Integration test on S/4 (same)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL returned.

- [ ] **Step 11.3: Label the PR for integration test**

Run: `gh pr edit <PR_NUMBER> --add-label needs:integration-test`

- [ ] **Step 11.4: Open the follow-up enhancement issue**

Run:

```bash
gh issue create --title "enhancement: normalize ETag charset at receive time (replace #15 retry)" --label enhancement --body "$(cat <<'EOF'
Follow-up from #35.

Today, `SetSource` handles the TABL ETag charset mismatch (adtler#15) via a 412 retry that patches the ETag string: if the PUT returns `412 ExceptionPreconditionFailed` and the ETag contains `text/plain` without `charset`, we insert `; charset=utf-8` and retry.

This works but costs an extra round-trip on every TABL write. A cleaner fix is to normalize ETags at receive time: when `GetSource` / `FetchETag` extract the `ETag` header, detect the missing charset and patch it immediately. Then the retry path in `SetSource` becomes dead code and can be deleted.

## Proposed change

1. Add an `normalizeETag(etag, contentType string) string` helper that inserts the charset if missing.
2. Call it in `GetSource` and `FetchETag` where the ETag is extracted from the response header.
3. Delete the 412 retry branch in `SetSource`.
4. Keep the existing `source_etag_charset_integration_test.go` to guard the behaviour.

## Why defer

- Needs validation on both R/3 and S/4 that the same normalization is correct for all object types (not just TABL).
- The existing retry is pessimistic but safe — removing it is a behaviour change that wants its own integration cycle.

Brainstormed during #35 design discussion.
EOF
)"
```

Expected: issue URL returned.

- [ ] **Step 11.5: Done**

Report the PR URL and the follow-up issue URL. Wait for:
1. CI to go green
2. Reviewer agent GO
3. Integration test agent result (R/3 + S/4)

Then remove `needs:integration-test` label and auto-merge per CLAUDE.md workflow.

---

## Post-merge checklist

After the PR merges:

- [ ] Close #35 (auto-closed via PR body)
- [ ] If `TestRunATCCheck_DiscoveryDriven` passed on R/3: close #12 with a comment referencing the passing integration run.
- [ ] If it still fails on R/3: update #12 with the new error output and keep `blocked:sap-investigation`.
