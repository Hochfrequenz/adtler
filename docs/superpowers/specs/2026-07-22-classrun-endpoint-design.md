# Design: `RunClass` — ADT Classrun endpoint client

- **Date:** 2026-07-22
- **Repo:** adtler
- **Status:** Proposed (revised after agent review)
- **Companion spec:** aibap.mcp `run_class` tool — `<aibap.mcp>/docs/superpowers/specs/2026-07-22-run-class-tool-design.md` (consumer of this endpoint; that PR is `blocked-by-adtler` until this ships in a tagged release).

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
(package `SEO_ADT`) exists, so the classrun framework is present on that system.
Its presence on HFQ (ECC/R3) is **not yet verified** — see verification points.

## Scope

**In scope:** a new `RunClass(ctx, className)` client method that POSTs to the
classrun endpoint and returns the console output; a `ClassRunResult` type; a
`ClassRunClient` interface **embedded into the aggregate `Client` interface**;
unit and integration tests.

**Out of scope:** any validation of whether the class exists / is active /
implements the interface (the consumer does the class-exists pre-check using
existing adtler reads — see companion spec); creating or activating classes; any
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
  `/sap/bc/adt/oo/classrun/` (mirrors `object.go` `endpoint + "/" +
  strings.ToLower(name)`). No manual escaping call is needed: `doMutate`
  applies `encodeNamespacePath` automatically (it triggers on `//`), so a
  namespaced class built as `/sap/bc/adt/oo/classrun//na2/foo` is encoded to
  `%2fna2%2ffoo` by the client. Just build the raw URI and let `doMutate` handle it.
- **Content type:** hard-code `contentTypeTextPlain` for `Accept`. classrun
  returns plain console text; a versioned content type is not expected, and
  `NegotiateContentType` does an **exact** discovery-key lookup (`discovery.go`),
  not the longest-prefix match `sourceContentType` uses — passing the per-object
  URI would never match the collection-href key and would silently fall back to
  `text/plain` anyway. Hard-coding is honest and avoids a misleading negotiation
  call. (If discovery ever advertises a versioned classrun type, revisit.)
- **Session:** stateless. No lock is acquired — classrun only executes. Any
  locking/commit the executed class performs is the class's own concern.
- **Timeout:** use the standard `doMutate` (30 s) client. A diagnostic helper
  could in principle run long, but `doMutateLong` (context-driven) is only
  warranted if a concrete use case needs it; default to `doMutate` and note the
  30 s ceiling. Large console outputs are read in full via `io.ReadAll` (same as
  `GetSource`); no truncation client-side.
- **Charset:** decode the body as `GetSource` does — `string(body)`, honouring
  `text/plain; charset=utf-8` — so non-ASCII console output (Umlaute) survives.

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

**`ClassRunClient` MUST be embedded in the aggregate `Client` interface**
(`client.go`, alongside `ObjectClient`, `DependencyClient`, …). Every capability
in adtler is reachable through `Client`; the integration test helper returns
`adt.Client`, and the aibap.mcp consumer needs `RunClass` on the `adt.Client` it
receives — without embedding it would only be reachable via a type assertion,
breaking the established pattern.

`RunClass` builds the URI, POSTs with `Accept: text/plain` and an empty body,
runs `checkResponse`, and on success returns `ClassRunResult{ClassName: className,
ConsoleOutput: string(body)}`. Follows `ActivateObjects` / `GetSource`
(`doMutate` → `checkResponse` → parse body).

## Error handling

- **HTTP errors** (403 auth, 404, 500, …): surface through `checkResponse` →
  `ADTError`, preserving the response body text so the caller sees the SAP
  message. The existing 4-layer `parseADTError` already handles SAP HTML dump
  pages — no new error class needed.
- **Uncaught runtime exception in `main`** — OPEN VERIFICATION POINT. Two
  possible behaviours; the integration test decides which holds:
  - (a) non-2xx status with the dump/exception text → lands as `ADTError`
    (body preserved). No code change.
  - (b) `200` with the error text in the body → a *successful run with error
    output*; `ClassRunResult.ConsoleOutput` carries the text, caller interprets.
    No code change.
  `RunClass` needs no exception-specific branch either way; the test pins the
  actual behaviour and this doc is updated to match.

## Testing

**Unit (`httptest` mock, no build tag):**
- Successful run → asserts request method is `POST`, body is empty, the
  `X-CSRF-Token` header is set, and `ConsoleOutput` is parsed from a `200`
  `text/plain` body.
- URI construction: lower-cased name, correct `/sap/bc/adt/oo/classrun/` path.
- UTF-8 console output (Umlaute) round-trips intact.
- HTTP error (403 / 404 / 500) → `ADTError` with body text retained.

**Integration (`//go:build integration`, via `eachSystem(t)` over R/3 **and**
S/4, per the repo convention):**
- `RunClass` against a real Z classrun class in package `Z_ADT_MCP_TEST`
  (e.g. `ZCL_ADT_MCP_CLASSRUN_TST`, implements `IF_OO_ADT_CLASSRUN`, writes a
  known string via `out->write`) → asserts the known string comes back.
- A **namespaced** class variant → exercises the `//`-encoding path against a
  live system (HFQ `/NA2/` context relevant).
- A **throwing** variant of the fixture class → **resolves the runtime-exception
  verification point** (records whether it arrives as HTTP error or 200-with-text;
  update this doc + any assertion to match).
- `eachSystem(t)` also confirms whether the classrun framework
  (`IF_OO_ADT_CLASSRUN`) exists on HFQ/ECC, not just S4U.
- Fixture class added to [Z_ADT_MCP_TEST](https://github.com/Hochfrequenz/Z_ADT_MCP_TEST).

## Open verification points

1. Runtime-exception signalling (HTTP error vs. 200-with-text) — see Error handling.
2. `IF_OO_ADT_CLASSRUN` / classrun endpoint availability on HFQ (ECC/R3) — only S4U verified so far.
3. Whether classrun needs any specific session type or extra header (expected: plain stateless POST).

## Rollout / linkage

- **Fixture first:** `ZCL_ADT_MCP_CLASSRUN_TST` (+ throwing + namespaced
  variants) must be delivered to `Z_ADT_MCP_TEST` before the integration tests
  can run — explicit ordering dependency.
- Release `RunClass` in the next adtler minor tag. The aibap.mcp `run_class` tool
  then consumes `adt.Client.RunClass`; that PR references this PR + tag and
  carries `blocked-by-adtler` until the bump lands.
