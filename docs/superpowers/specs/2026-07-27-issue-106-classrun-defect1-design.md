# Design: `RunClass` classrun load-generation — defect 1 (fresh-class false "does not implement")

- **Date:** 2026-07-27
- **Repo:** adtler
- **Issue:** [Hochfrequenz/adtler#106](https://github.com/Hochfrequenz/adtler/issues/106)
- **Consumer:** [Hochfrequenz/aibap.mcp#460](https://github.com/Hochfrequenz/aibap.mcp/issues/460) (`blocked-by-adtler`; removes its interim workaround note after the bump)
- **Builds on:** `docs/superpowers/specs/2026-07-22-classrun-endpoint-design.md` (the original `RunClass` client)
- **Status:** Proposed (system scope verified on HFQ **and** S4U, 2026-07-27)

## Background

`RunClass` (`POST /sap/bc/adt/oo/classrun/{name}`) executes a class's **generated
runtime load** and does not itself trigger load generation. On S/4 (verified S4U),
ADT activation does not (re)generate that load either; on ECC (verified HFQ) it
does — so the defects below are S/4-specific (see "Both defects are S/4-specific").
Issue #106 documents two user-visible defects when the whole lifecycle (create →
set source → activate → run) happens over ADT REST with no execution-triggering
step in between, on a system where activation does not generate the load:

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

### Both defects are S/4-specific (verified 2026-07-27)

Running the identical create → set source → activate → run lifecycle on both
connected systems shows the defects are **not** system-neutral:

| Step (pure ADT REST) | HFQ (ECC/R3, older classrun handler) | S4U (S/4, SAP_BASIS 758) |
|---|---|---|
| Fresh class → `RunClass` | ✅ real output (`V1`) | ❌ `Error: Class does not implement...main...` (defect 1) |
| Change source → activate → `RunClass` | ✅ new output (`V2`, `V3`) | ⚠️ stale previous output (defect 2) |

On **HFQ/ECC the activation (re)generates the runtime load**, so classrun runs
correctly across the whole lifecycle — neither defect appears. On **S4U/S/4 the
activation never (re)generates the load** in the classrun context, producing both
defects. This is a known-shape R/3-vs-S/4 divergence (cf. CLAUDE.md "SAP system
differences"). The two systems tested differ in both product (ECC vs S/4) and
classrun-handler variant, so the claim is scoped to "S/4-specific on the tested
systems", not a universal ECC-vs-S/4 law.

**Consequence for this spec:** the fix targets the S/4 failure path; the
`eachSystem` integration test must expect **different** behaviour per system (real
output on ECC, the soft-fail / post-fix error on S/4) rather than asserting the
defect uniformly. See Testing.

## Investigation summary (what constrains the design)

Verified live on S4U (SAP_BASIS 758) 2026-07-24 and 2026-07-27, and on HFQ
(ECC/R3) 2026-07-27 (issue #106 comments). Relevant facts:

- **Both defects reproduce on S4U (S/4) and on neither on HFQ (ECC/R3)** — see
  "Both defects are S/4-specific" above. ECC's activation regenerates the load;
  S/4's does not.
- Defect 1 is purely runtime-load-generation state — a trivial pure-`out->write`
  class with no DB/EML access reproduces it identically. It is **not** a DB/RAP
  problem.
- The `text/plain` soft-fail body is produced by the handler at HTTP 200; there
  is no structured error channel and no ST22 dump.
- **The soft-fail string is cause-ambiguous** (verified 2026-07-27, S4U): a class
  that does *not* implement `IF_OO_ADT_CLASSRUN` but *does* have a generated load
  (load confirmed via a passing instantiating unit test) returns the **identical**
  `Error: Class does not implement if_oo_adt_classrun~main method!`. So the same
  body means load-not-generated *or* genuine non-implementer *or* not-instantiable
  — the classification cannot attribute a single cause. Drives the error naming
  and the Option-B implement-check (see Option A / Option B).
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

When the console body is the known "does not implement …main…" soft-fail, return
a typed error instead of a success result. Proposed error, following the
established sentinel / typed-error pattern (cf. `ErrorObjectLockedInTransport`):

```go
// adt/classrun.go

// ErrClassNotExecutable indicates the classrun handler returned its
// "does not implement if_oo_adt_classrun~main method" soft-failure at HTTP 200
// instead of real console output (see issue #106). This is a HEURISTIC
// classification of a text/plain body — SAP exposes no structured error channel
// here — and the message is CAUSE-AMBIGUOUS. The same body is produced by at
// least: (a) the runtime load not being generated yet (issue #106 defect 1, S/4),
// (b) a class that genuinely does not implement IF_OO_ADT_CLASSRUN, and
// (c) a class that cannot be instantiated for other reasons (cx_sy_create_object_error).
// RunClass performs no pre-check, so it cannot attribute a single cause; the name
// is therefore the OBSERVABLE effect ("not executable via classrun"), not a
// presumed cause. See Detection.
var ErrClassNotExecutable = errors.New("classrun: class not executable via classrun (handler returned 'does not implement ...main...' at HTTP 200)")
```

`RunClass` returns `fmt.Errorf("RunClass %s: %w", className, ErrClassNotExecutable)`
when the body matches, so callers can `errors.Is` it while still seeing the class
name.

**Naming rationale (must-address from PR #107 review):** an earlier draft named
this `ErrClassLoadNotGenerated`, which overclaims — the body cannot prove the load
is the cause. **Verified live 2026-07-27 on S4U:** a class that does *not* implement
`IF_OO_ADT_CLASSRUN` but *does* have a generated runtime load (load confirmed by a
passing instantiating unit test) returns the **identical** string
`Error: Class does not implement if_oo_adt_classrun~main method!`. So the string is
demonstrably not load-specific; a cause-neutral name is required.

### Detection — the one real design question

SAP gives no structured signal; matching is on the body text, and (per the
verification above) the matched string is **cause-ambiguous**. Two questions
follow: *what* to match, and *what the match is allowed to claim*.

**What to match:**

- **Exact/prefix match on the known handler string**
  `Error: Class does not implement if_oo_adt_classrun~main method!` (constant,
  emitted verbatim by `CL_OO_ADT_RES_CLASSRUN`). Narrow, low false-positive risk.
  A class that deliberately `out->write`s that exact string is a pathological,
  accepted edge case.
- Broaden later only if integration testing surfaces other create-object phrasings
  (e.g. the ECC handler variant). Keep the matched strings hoisted as constants.

**What the match may claim — the cause-ambiguity carve-outs.** The same HTTP-200
body is emitted by several distinct conditions (original classrun spec §Error
handling; item (b) below verified live 2026-07-27):

| Condition | Body | Handled how |
|---|---|---|
| (a) Load not generated (S/4 defect 1) | `does not implement …main…` | → `ErrClassNotExecutable` |
| (b) Class genuinely does not implement the interface (even with a load) | **identical** `does not implement …main…` | → `ErrClassNotExecutable` (indistinguishable — verified) |
| (c) Not instantiable for other reasons (`cx_sy_create_object_error`) | `does not implement …main…` | → `ErrClassNotExecutable` |
| (d) Non-existent class | 200-with-text, **not** 404 | consumer's existence pre-check catches this before `RunClass` (aibap.mcp already does `GetObjectInfo` first) |
| (e) Missing `S_DEVELOP` auth (`OO 755`) | *different* auth text | **not** folded in — different string, leave as-is (out of scope); classify separately later if wanted |

Because `RunClass` performs no pre-check, it must **not** attribute the cause: the
error says "not executable via classrun", and callers decide what to do. In
practice the S/4 caller that owns the lifecycle (created the class, knows it
implements the interface and is active) can reasonably infer (a) and reach for
load generation — but that inference lives in the caller, not in adtler's error
name. This is also why **Option B must gate load generation on the class actually
implementing the interface** (otherwise it generates a load for a class that will
never run — condition (b)).

### Why baseline

No object mutation, no transport, no extra round-trips; purely a
response-classification change local to `RunClass`. It removes the "does not
implement" red herring regardless of whether Option B ships. Unblocked.

## Option B — generate the load on demand (opt-in, `blocked:design-needed`)

Make a fresh class actually *run* over pure ADT REST by generating its load via
the verified test-include-activate path, then retrying the run.

### Sequence (all existing adtler primitives)

1. `RunClass` → returns `ErrClassNotExecutable` (Option A must land first).
   Gate load generation on the class actually implementing `IF_OO_ADT_CLASSRUN`
   (a cheap metadata/where-used check), so condition (b) — a genuine
   non-implementer — is not sent down the generate-and-retry path.
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

Adding `EnsureClassLoad` to the exported `ClassRunClient` interface is a breaking
change for any external mock/implementer of that interface — another reason to
treat it as a deliberate, versioned addition rather than folding it into `RunClass`.

Cleanup safety for the dummy test include is supported by the investigation's own
finding: no in-place ADT-REST operation evicts a generated load, so removing the
include (activate again) after generation does not un-generate the load — the
class stays runnable.

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
- **Behavioural compatibility (call out at implementation):** Option A flips
  `RunClass` from `(*ClassRunResult, nil)` to `(nil, error)` for the soft-fail
  body. Any current consumer that reads the soft-fail out of `ConsoleOutput` (the
  pre-fix contract the original classrun spec explicitly warned about) will now
  get an error instead. This is the intended fix, but it is a behavioural change
  and should ship in a minor tag with a changelog note; the aibap.mcp consumer
  bump must adapt to the new error return.

## Testing

**Unit (`httptest` mock, no build tag):**
- 200 body = exact `Error: Class does not implement if_oo_adt_classrun~main method!`
  → `RunClass` returns `ErrClassNotExecutable` (assert `errors.Is`), not a
  success result.
- 200 body = ordinary console output that merely *contains* the word `Error`
  elsewhere → still a success result (guards the matcher against over-broad
  matching).
- Existing success / UTF-8 / HTTP-error cases stay green.
- (Option B, if it lands) `EnsureClassLoad` happy path over mocked
  lock/create-test-include/set-include-source/activate calls.

**Integration (`//go:build integration`, `eachSystem(t)` over R/3 **and** S/4):**

The bug failure path is S/4-only, so the test **must branch on system behaviour**,
not assert the defect uniformly (verified 2026-07-27: ECC returns real output, S/4
returns the soft-fail). Two viable shapes:

- **Behaviour-detecting (preferred):** create + set source + activate a fresh
  classrun class purely over ADT REST, then `RunClass`. Accept **either** outcome
  per system: real output (ECC, load generated on activation) **or**
  `ErrClassNotExecutable` (S/4, defect 1 now surfaced as a typed error). Assert
  that the return is exactly one of those two — never a *success result carrying
  the soft-fail string* (that is the pre-fix bug the change removes). This keeps
  the test green on both systems while still pinning the fix.
- If a strict per-system assertion is wanted, gate it on a capability/known-system
  check rather than hard-coding system keys.
- (Option B, if it lands) after `EnsureClassLoad` on S/4, `RunClass` returns the
  real output; on ECC `EnsureClassLoad` is a no-op-equivalent (load already
  present) and `RunClass` still returns real output.
- Fixtures in `Z_ADT_MCP_TEST`, `$TMP` scratch classes cleaned up (all probe
  classes from this investigation were deleted; no `$TMP` leftovers on either
  system).

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
