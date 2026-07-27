# Design: `RunClass` classrun load-generation — defect 1 (fresh-class false "does not implement")

- **Date:** 2026-07-27
- **Repo:** adtler
- **Issue:** [Hochfrequenz/adtler#106](https://github.com/Hochfrequenz/adtler/issues/106)
- **Consumer:** [Hochfrequenz/aibap.mcp#460](https://github.com/Hochfrequenz/aibap.mcp/issues/460) (`blocked-by-adtler`; removes its interim workaround note after the bump)
- **Builds on:** `docs/superpowers/specs/2026-07-22-classrun-endpoint-design.md` (the original `RunClass` client)
- **Status:** Proposed

## Background

`RunClass` (`POST /sap/bc/adt/oo/classrun/{name}`) executes a class's **generated
runtime load** and does not itself trigger load generation; ADT activation does
not (re)generate that load either. Issue #106 documents two user-visible defects
when the whole lifecycle (create → set source → activate → run) happens over ADT
REST with no execution-triggering step in between:

- **Defect 1 — fresh class, false "does not implement".** A class created +
  activated purely over ADT REST that has never had its load generated returns,
  from `CL_OO_ADT_RES_CLASSRUN`, **HTTP 200** with the body
  `Error: Class does not implement if_oo_adt_classrun~main method!` — even though
  it *does* implement the interface. The handler wraps `CREATE OBJECT` in
  `TRY ... CATCH cx_sy_create_object_error` and, because the runtime load is
  absent, masks the create failure as the generic "does not implement main"
  soft-failure.
- **Defect 2 — changed class, stale execution.** After changing the source and
  re-activating over ADT REST, `RunClass` keeps returning the previously
  generated version's output.

**This spec covers defect 1 only.** Defect 2 is deliberately out of scope — see
"Defect 2 is out of scope" below.

## Investigation summary (what constrains the design)

Verified live on S4U (SAP_BASIS 758), 2026-07-24 and 2026-07-27 (issue #106
comments). Relevant facts:

- Defect 1 is purely runtime-load-generation state — a trivial pure-`out->write`
  class with no DB/EML access reproduces it identically. It is **not** a DB/RAP
  problem.
- The `text/plain` soft-fail body is produced by the handler at HTTP 200; there
  is no structured error channel and no ST22 dump.
- **A pure-ADT-REST path generates the load:** adding a (dummy) ABAP Unit test
  include to the class and **activating** it (without running the test) makes
  the subsequent `RunClass` return the real output. A plain re-activation without
  a test include does not. Re-verified 2026-07-27.
- No **in-place** ADT-REST operation busts a stale load (defect 2): not a second
  activation, not a structural signature change, not re-running an instantiating
  unit test. Only `DELETE` evicts the load — which destroys object identity and
  is not a usable fix. This is why defect 2 needs the Eclipse F9 HTTP capture and
  is out of scope here.

## Scope

**In scope:** making `RunClass` behave correctly for a fresh, never-generated
class driven purely over ADT REST. Two independent, composable changes:

1. **Option A — classify the masked soft-fail as a real error** (no object
   mutation). Unblocked; recommended as the baseline fix.
2. **Option B — generate the load on demand** so `RunClass` actually runs a fresh
   class (mild object mutation). `blocked:design-needed`; recommended as an
   opt-in, not default behaviour.

**Out of scope:** defect 2 (stale load) in-place fix; any DDIC/RAP-specific
behaviour; changing the classrun request contract (still stateless
`POST … Accept: text/plain`).

## Option A — surface the masked soft-fail as an error (recommended baseline)

Today `RunClass` returns *any* HTTP 200 body as `ClassRunResult.ConsoleOutput`,
so the create-object soft-fail arrives at the caller as a **success** whose
output happens to be an error string. The original classrun spec already flagged
this ("consumers must not treat a successful `RunClass` return as the class
succeeded"); this option moves that burden off the consumer.

### Behaviour

When the console body is the known create-object soft-fail, return a typed error
instead of a success result. Proposed error, following the established sentinel /
typed-error pattern (cf. `ErrorObjectLockedInTransport`):

```go
// adt/classrun.go

// ErrClassLoadNotGenerated indicates the classrun handler returned its
// create-object soft-failure ("does not implement ...main..."), which for a
// class that provably implements IF_OO_ADT_CLASSRUN means the runtime load has
// not been generated yet (see issue #106). It is a heuristic classification of
// an HTTP 200 text/plain body: SAP exposes no structured error channel here.
var ErrClassLoadNotGenerated = errors.New("classrun: runtime load not generated (class create-object soft-failure)")
```

`RunClass` returns `fmt.Errorf("RunClass %s: %w", className, ErrClassLoadNotGenerated)`
when the body matches, so callers can `errors.Is` it while still seeing the class
name.

### Detection — the one real design question

SAP gives no structured signal; matching is on the body text. Options, safest
first:

- **Exact/prefix match on the known handler string**
  `Error: Class does not implement if_oo_adt_classrun~main method!` (constant,
  emitted verbatim by `CL_OO_ADT_RES_CLASSRUN`). Narrow, low false-positive risk.
  A class that deliberately `out->write`s that exact string is a pathological,
  accepted edge case.
- Broaden later only if integration testing surfaces other create-object phrasings
  (e.g. the ECC handler variant). Keep the matched strings hoisted as constants.

Note the auth soft-fail (`S_DEVELOP` / SAP `OO 755`) is a *different* condition
and should **not** be folded into `ErrClassLoadNotGenerated`; if we classify it,
it is a separate error. Recommended: leave auth as-is for this change (out of
scope) to keep the classification narrow.

### Why baseline

No object mutation, no transport, no extra round-trips; purely a
response-classification change local to `RunClass`. It removes the "does not
implement" red herring regardless of whether Option B ships. Unblocked.

## Option B — generate the load on demand (opt-in, `blocked:design-needed`)

Make a fresh class actually *run* over pure ADT REST by generating its load via
the verified test-include-activate path, then retrying the run.

### Sequence (all existing adtler primitives)

1. `RunClass` → detects `ErrClassLoadNotGenerated` (Option A must land first).
2. `lock` → `create_test_include` → `set_include_source` (dummy
   `FOR TESTING` class doing `CREATE OBJECT`) → `activate`.
3. Retry `RunClass` → returns the real output.

### Design decision: explicit method, not silent mutation

**Do not** bury this in `RunClass` as an automatic side effect. `RunClass` is a
read-like "execute" primitive; silently adding a CCAU include and re-activating
the caller's class violates least-surprise and has real consequences:

- **Object mutation:** a test include appears on the class that the author never
  wrote. Whether to remove it afterward (another activate) or leave it is itself
  an unresolved question — leaving it pollutes the class; removing it adds
  fragility and more round-trips.
- **Transport:** for non-`$TMP` objects, `create_test_include` /
  `set_include_source` / `activate` require a transport. A pure "run" call
  suddenly needing a transport is a surprising contract change.
- **Concurrency / locks:** acquiring an edit lock on someone else's active class
  to run it can collide with real editing sessions.

**Recommendation:** expose a dedicated, explicitly-named method the consumer opts
into — e.g.

```go
type ClassRunClient interface {
    RunClass(ctx context.Context, className string) (*ClassRunResult, error)
    // EnsureClassLoad generates the runtime load for className via the
    // test-include-activate path so a subsequent RunClass on a fresh,
    // never-executed class returns real output instead of the create-object
    // soft-failure (issue #106, defect 1). It MUTATES the class (adds a dummy
    // test include and re-activates) and, for transported objects, requires a
    // transport. Caller opts in deliberately.
    EnsureClassLoad(ctx context.Context, className string, opts EnsureClassLoadOptions) error
}
```

`EnsureClassLoadOptions` carries at least the transport number and a flag for
whether to remove the generated test include afterward. The consumer
(aibap.mcp `run_class`) decides when to call it (e.g. only for `$TMP` fixtures,
or gated behind an explicit "generate load" tool parameter).

This keeps the mutation **visible and deliberate** and leaves `RunClass` pure.

### Why blocked:design-needed

The mutation, transport, lock, and cleanup semantics above need a human decision
before implementation. Option A is independent and should not wait on it.

## Defect 2 is out of scope

No in-place ADT-REST operation invalidates the stale PXA load (investigation
2026-07-27; only `DELETE` evicts it, destroying object identity). A durable
defect-2 fix needs the load-generation/invalidation call Eclipse issues on F9,
which is only knowable from an HTTP capture — tracked under
`blocked:eclipse-capture` on issue #106. Attempting a defect-2 fix here would be
guessing. If the capture shows Eclipse has no in-place trigger either, defect 2
becomes a documented SAP classrun limitation and Option A is the whole deliverable.

## Error handling

- Option A adds one classification branch before returning success; all existing
  HTTP-error handling (`checkResponse` → `ADTError`) is unchanged.
- The classification is explicitly heuristic (HTTP 200 text body); the godoc and
  this spec say so. If SAP ever adds a structured signal, revisit.

## Testing

**Unit (`httptest` mock, no build tag):**
- 200 body = exact `Error: Class does not implement if_oo_adt_classrun~main method!`
  → `RunClass` returns `ErrClassLoadNotGenerated` (assert `errors.Is`), not a
  success result.
- 200 body = ordinary console output that merely *contains* the word `Error`
  elsewhere → still a success result (guards the matcher against over-broad
  matching).
- Existing success / UTF-8 / HTTP-error cases stay green.
- (Option B, if it lands) `EnsureClassLoad` happy path over mocked
  lock/create-test-include/set-include-source/activate calls.

**Integration (`//go:build integration`, `eachSystem(t)` over R/3 **and** S/4):**
- Create + set source + activate a fresh classrun class purely over ADT REST,
  then `RunClass` → asserts `ErrClassLoadNotGenerated` (defect 1 reproduced and
  now surfaced as an error). This is the exact bug failure path per the workflow
  convention.
- (Option B, if it lands) after `EnsureClassLoad`, `RunClass` returns the real
  output on both systems; verify the ECC handler variant behaves the same.
- Fixtures in `Z_ADT_MCP_TEST`, `$TMP` scratch classes cleaned up.

## Rollout / linkage

- **Phase 1 (unblocked):** ship Option A in the next adtler minor tag. This alone
  lets aibap.mcp#460 replace "silently returns a stale/false success" with a real
  error, though the interim workaround note stays until load generation is solved.
- **Phase 2 (`blocked:design-needed`):** implement Option B as `EnsureClassLoad`
  after human sign-off on the mutation/transport/cleanup semantics.
- **Defect 2 (`blocked:eclipse-capture`):** separate work item; not gated on the
  above.
- The aibap.mcp `run_class` PR references the adtler tag and removes its interim
  workaround note once the bump lands.
