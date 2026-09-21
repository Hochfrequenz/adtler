# Design: `RunClass` — ADT Classrun endpoint client

- **Date:** 2026-07-22
- **Repo:** adtler
- **Status:** Proposed (revised after agent review + live handler verification on HFQ and S4U, 2026-07-23)
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

Verified precondition (2026-07-23, HFQ **and** S4U): the classrun framework is
present on **both** systems. The request handler `CL_OO_ADT_RES_CLASSRUN`
(package `SEO_ADT`) and interface `IF_OO_ADT_CLASSRUN` exist on each; its source
was read on both to confirm the endpoint contract (see "The classrun endpoint"
and "Error handling"). Note the HFQ (ECC) handler is an older variant:
`IF_OO_ADT_CLASSRUN_OUT` is absent there and the console-out object uses
`write_text`, and the ECC handler additionally accepts `PROG`/`DDLS` object
types — none of which affects the `text/plain` client contract.

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
  (Verified 2026-07-23: the handler `TRANSLATE`s the class name `TO UPPER CASE`
  server-side, so lower-casing is a convention/namespace-encoding requirement,
  not a functional one — both forms resolve to the same class.)
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
- **Failure signalling — resolved (2026-07-23) by reading `CL_OO_ADT_RES_CLASSRUN`
  on both systems.** The handler wraps the `main()` call in a `TRY ... CATCH
  cx_sy_create_object_error` only. This produces a **split** contract:
  - (a) **Uncaught runtime exception in `main`** (e.g. `cx_sy_zerodivide`) is
    *not* caught → propagates → the ADT REST framework returns a **non-2xx**
    HTTP error → lands as `ADTError` (body preserved). No code change.
  - (b) **"Soft" failures return HTTP `200` with the error text in the body:**
    missing `S_DEVELOP` authorization, a class that does not implement the
    interface, or a class that cannot be instantiated (`cx_sy_create_object_error`)
    are all written to the response body. **A non-existent class is 200-with-text,
    NOT 404.** `ClassRunResult.ConsoleOutput` carries the text; the caller
    interprets it.
  `RunClass` needs no exception-specific branch either way — it returns an
  `ADTError` for (a) and a `ClassRunResult` for (b). **Consumers (aibap.mcp
  `run_class`) must not treat a successful `RunClass` return as "the class
  succeeded": the console output may be an error string** (SAP `OO 755` auth
  message, `"Error: Class does not implement..."`, etc.). Pre-checking class
  existence/activeness before calling is the consumer's job (already in the
  companion spec).

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

## Verification points

1. **Resolved (2026-07-23).** Failure signalling is split: uncaught runtime
   exception → non-2xx (`ADTError`); soft failures (auth, interface not
   implemented, non-existent class) → HTTP 200 with an error string in the
   body. See Error handling. Runtime confirmation lands with the integration
   test (`TestRunClass_ThrowingClass`).
2. **Resolved (2026-07-23).** The classrun endpoint is available on **both**
   HFQ (ECC/R3) and S4U — handler `CL_OO_ADT_RES_CLASSRUN` and interface
   `IF_OO_ADT_CLASSRUN` exist on each. The HFQ handler is an older variant (no
   `IF_OO_ADT_CLASSRUN_OUT`, uses `write_text`, also serves `PROG`/`DDLS`), but
   the `text/plain` client contract is identical.
3. **Resolved (2026-07-23).** Plain stateless POST, no special session type or
   extra header required. The handler reads only the `classname` URI attribute
   (uppercased server-side), an optional `profilerId` query param, and two
   optional `sap-adt-push-*` headers for async console streaming — none needed
   for the synchronous `RunClass` path.

## Rollout / linkage

- **Fixture first:** `ZCL_ADT_MCP_CLASSRUN_TST` (+ throwing + namespaced
  variants) must be delivered to `Z_ADT_MCP_TEST` before the integration tests
  can run — explicit ordering dependency.
- Release `RunClass` in the next adtler minor tag. The aibap.mcp `run_class` tool
  then consumes `adt.Client.RunClass`; that PR references this PR + tag and
  carries `blocked-by-adtler` until the bump lands.
