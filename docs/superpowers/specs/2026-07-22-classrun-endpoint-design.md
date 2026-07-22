# Design: `RunClass` — ADT Classrun endpoint client

- **Date:** 2026-07-22
- **Repo:** adtler
- **Status:** Proposed
- **Companion spec:** aibap.mcp `run_class` tool — `docs/superpowers/specs/2026-07-22-run-class-tool-design.md` (consumer of this endpoint; that PR is `blocked-by-adtler` until this ships in a tagged release).

## Motivation

ADT exposes a "Run as ABAP Application" capability (Eclipse F9) via the classrun
endpoint: any global class implementing `IF_OO_ADT_CLASSRUN` can be executed
server-side, and whatever its `main` method writes to the console handler is
returned to the client. adtler has no client for this endpoint today.

The immediate downstream driver is a headless lock-cleanup helper (aibap.mcp
issue #383): the only way to invoke `cl_enq_admin~remove_locks` — or any other
helper class — without a SAP GUI is to deploy a class and execute it via
classrun. `RunClass` is the generic primitive that makes that (and other
diagnostic/helper flows) possible. This spec covers **only** the generic
endpoint client; no lock-specific logic lives here.

Verified precondition (S4U, 2026-07-22): interface `IF_OO_ADT_CLASSRUN`
(package `SEO_ADT`, "Implement this interface to execute an ABAP class
(Classrun)") exists, so the classrun framework is present on the target system.

## Scope

**In scope:** a new `RunClass(ctx, className)` client method that POSTs to the
classrun endpoint and returns the console output; a `ClassRunResult` type; a
`ClassRunClient` interface; content-type negotiation via discovery; unit and
integration tests.

**Out of scope:** any validation of whether the class exists / is active /
implements the interface (the consumer performs those pre-checks using existing
adtler reads — see the companion spec); creating or activating classes; any
lock-specific behaviour.

## The classrun endpoint

```
POST /sap/bc/adt/oo/classrun/{name}        name = lower-cased class name
Accept: text/plain                          console output (out->write(...))
(no request body)
→ 200 text/plain: the class's console output
```

Notes / decisions:

- **URI construction:** lower-case the class name and append to
  `/sap/bc/adt/oo/classrun/`. Mirror the existing URI-escaping helper used for
  other object paths (see `client.go` namespace/`%2f` handling) so
  namespaced classes (`/foo/bar`) work.
- **Content negotiation:** resolve the `Accept`/content type via
  `NegotiateContentType` / discovery rather than hard-coding, per the repo
  convention. `text/plain` is the expected default; the discovery document may
  advertise a versioned type on some systems. Hard-coded `text/plain` remains
  the fallback when discovery is empty.
- **Session:** stateless. No lock is acquired — classrun only executes. Any
  locking/commit the executed class performs is the class's own concern.

## API design

```go
// adt/classrun.go
type ClassRunResult struct {
    ClassName     string `json:"class_name"`
    ConsoleOutput string `json:"console_output"`
}

type ClassRunClient interface {
    RunClass(ctx context.Context, className string) (*ClassRunResult, error)
}

func (c *httpClient) RunClass(ctx context.Context, className string) (*ClassRunResult, error)
```

`RunClass` builds the URI, POSTs with the negotiated `Accept`, runs
`checkResponse`, and on success returns `ClassRunResult{ClassName: className,
ConsoleOutput: <body>}`. Follows the shape of existing methods in
`adt/activate.go` (`doMutate` → `checkResponse` → parse body).

## Error handling

- **HTTP errors** (403 auth, 404, 500, …): surface through `checkResponse` →
  `ADTError`, **preserving the response body text** so the caller sees the SAP
  message. No special casing beyond the existing error path.
- **Uncaught runtime exception in `main`** — OPEN VERIFICATION POINT. Two
  possible behaviours; the integration test decides which holds on the target:
  - (a) classrun returns a non-2xx status with the dump/exception text → it
    lands as an `ADTError` (body preserved). No code change needed.
  - (b) classrun returns `200` with the error text in the body → it is a
    *successful run with error output*; `RunClassResult.ConsoleOutput` carries
    the text and it is up to the caller to interpret. No code change needed.
  Either way `RunClass` itself needs no exception-specific branch; the spec
  records the expectation and the test pins the actual behaviour.

## Testing

**Unit (`httptest` mock, no build tag):**
- Successful POST → `ConsoleOutput` parsed from a `200` `text/plain` body.
- URI construction: lower-cased name, correct `/sap/bc/adt/oo/classrun/` path,
  namespaced-name escaping.
- Content negotiation: discovery present (versioned type) vs. empty (falls back
  to `text/plain`).
- HTTP error (403 / 404 / 500) → `ADTError` with body text retained.

**Integration (`//go:build integration`, live S4U):**
- `RunClass` against a real Z classrun class in package `Z_ADT_MCP_TEST`
  (e.g. `ZCL_ADT_MCP_CLASSRUN_TST`, implements `IF_OO_ADT_CLASSRUN`, writes a
  known string via `out->write`) → asserts the known string comes back.
- A throwing variant of the fixture class → **resolves the open verification
  point** above (records whether the runtime exception arrives as HTTP error or
  200-with-text; adjust the doc + any test assertion to match).
- Fixture class added to the [Z_ADT_MCP_TEST](https://github.com/Hochfrequenz/Z_ADT_MCP_TEST)
  repo so CI and other consumers have it.

## Open verification points

1. Runtime-exception signalling (HTTP error vs. 200-with-text) — see Error
   handling.
2. Exact `Accept`/content type the endpoint negotiates on S4U vs. ECC (HFQ) —
   confirm `text/plain` and whether a versioned type is advertised.
3. Whether classrun requires any specific session type or extra header on the
   target systems (expected: plain stateless POST).

## Downstream / linkage

Once released (target tag: next adtler minor), the aibap.mcp `run_class` tool
consumes `ClassRunClient.RunClass`. The aibap.mcp tool PR references this PR and
tag and carries the `blocked-by-adtler` label until the bump lands.
