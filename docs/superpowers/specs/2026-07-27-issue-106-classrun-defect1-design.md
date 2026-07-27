# Design: `RunClass` classrun load-generation — defect 1 (fresh-class false "does not implement")

- **Date:** 2026-07-27
- **Repo:** adtler
- **Issue:** [Hochfrequenz/adtler#106](https://github.com/Hochfrequenz/adtler/issues/106)
- **Consumer:** [Hochfrequenz/aibap.mcp#460](https://github.com/Hochfrequenz/aibap.mcp/issues/460) (`blocked-by-adtler`; removes its interim workaround note after the bump)
- **Builds on:** `docs/superpowers/specs/2026-07-22-classrun-endpoint-design.md` (the original `RunClass` client)
- **Status:** Proposed — **root cause identified and fix verified live on S4U** (2026-07-27); HFQ regression pending on the fix branch.

## TL;DR — what changed in this revision

The earlier revision assumed defect 1 was "the runtime load is never generated over ADT REST, so `RunClass` needs to mutate the class (add a test include + re-activate) to force generation." **Live re-verification on S4U disproved the premise.** The load *is* generated over pure HTTP — but only in a **fresh SAP session**. The real trigger is **HTTP session reuse**: the ADT session that performed `create → set source → activate` cannot then generate the runtime load when it runs the classrun in that *same* session; a brand-new session generates it cleanly and returns real output.

Consequences:

- **Defect 1 has a trivial, non-mutating fix (Option C):** run the classrun POST on an **isolated fresh HTTP session**. No test include, no transport, no lock, no object mutation.
- **Option B (test-include-activate mutation) is superseded** by Option C and dropped from the recommendation.
- **Option A (classify the soft-fail as a typed error) still stands** as an independent robustness improvement.

## Background

`RunClass` (`POST /sap/bc/adt/oo/classrun/{name}`) executes a class's **generated
runtime load**. Issue #106 documents two user-visible defects when the whole
lifecycle (create → set source → activate → run) happens over ADT REST:

- **Defect 1 — fresh class, false "does not implement".** A class created +
  activated purely over ADT REST returns, from `CL_OO_ADT_RES_CLASSRUN`,
  **HTTP 200** with the body
  `Error: Class does not implement if_oo_adt_classrun~main method!` — even though
  it *does* implement the interface. The handler wraps `CREATE OBJECT` in
  `TRY ... CATCH cx_sy_create_object_error` and, because the runtime load is
  absent **in that session**, masks the create failure as the generic "does not
  implement main" soft-failure.
- **Defect 2 — changed class, stale execution.** After changing the source and
  re-activating over ADT REST, `RunClass` keeps returning the previously
  generated version's output.

**This spec covers defect 1 only.** Defect 2 is deliberately out of scope — see
"Defect 2 is out of scope" below.

### Root cause: HTTP session reuse (verified live 2026-07-27, S4U)

The classrun handler runs `CREATE OBJECT` for the target class. In a **fresh**
SAP session that never touched the class, `CREATE OBJECT` triggers implicit
runtime-load generation and succeeds → real output. In the **same session that
just created + activated the class**, that implicit generation does not happen
and `CREATE OBJECT` raises `cx_sy_create_object_error`, which the handler masks
as the "does not implement …main…" soft-fail.

This was proven with a 2×2 experiment run through the **real adtler Go client**
against S4U on freshly created + activated `$TMP` classrun classes:

| Scenario | HTTP session | classrun result |
|---|---|---|
| **A** — fresh client, `RunClass` immediately (only the discovery CSRF GET precedes the POST) | fresh, never ran the lifecycle | **real output `V1`** ✅ |
| **B** — fresh client, a class-scoped `GetSource` then `RunClass` | fresh | **real output `V1`** ✅ |
| **L1** — one client does `create → set source → activate`, then `RunClass` in that same session | reused (worn) | **soft-fail** ❌ |
| **L2** — same client as L1, but `Logout()` immediately before `RunClass` | fresh (jar + CSRF dropped) | **real output `V1`** ✅ |

A/B prove adtler's HTTP request itself is correct — a fresh adtler session
behaves exactly like the Eclipse F9 path and the raw-PowerShell counter-test
(both return `V1`). L1 reproduces the bug; L2 shows that simply dropping the
session (`Logout`) before the run fixes it. The differentiator is **session
freshness, nothing else** — see "Ruled out" below.

### Both defects are S/4-specific (verified 2026-07-27)

Running the identical create → set source → activate → run lifecycle **in a
single reused session** on both connected systems:

| Step (pure ADT REST, reused session) | HFQ (ECC/R3) | S4U (S/4, SAP_BASIS 758) |
|---|---|---|
| Fresh class → `RunClass` | ✅ real output (`V1`) | ❌ soft-fail (defect 1) |
| Change source → activate → `RunClass` | ✅ new output | ⚠️ stale previous output (defect 2) |

On **HFQ/ECC the activation (re)generates a persistent runtime load**, so
classrun runs correctly regardless of session freshness — neither defect
appears. On **S4U/S/4 the activation does not**, so a reused session hits the
missing-load path. The fix (fresh session, Option C) is a no-op on HFQ (the load
is already persistent) and repairs S4U — so the `eachSystem` test can assert
**real output on both systems**, with HFQ acting as the regression guard.

### Ruled out as the trigger (each verified live)

- **The debugger breakpoint-sync request** (`POST /sap/bc/adt/debugger/breakpoints`
  that Eclipse fires on F9). Reproduced independently on S4U: a fresh session
  with **no breakpoint-sync at all** returns `V1`, and a session where the
  breakpoint-sync itself failed (HTTP 400/403) still returns `V1`. It is
  debugger housekeeping, not a load generator.
- **The warm-up GET target.** `compatibility/graph`, the class `source/main`,
  and the `discovery` preflight all warm the session equally; the GET does not
  even need to succeed (a 400 that still issues a CSRF token is enough). What
  matters is that a *fresh* session performs the classrun, not what the GET was.
- **`X-sap-adt-sessiontype: stateless`** — `V1` with and without it.
- **`sap-client` delivery** (header vs. query param) — a fresh session returns
  `V1` even with `sap-client` sent as adtler sends it (a header).

## Investigation summary (what constrains the design)

Verified live on S4U (SAP_BASIS 758) 2026-07-24 / 2026-07-27 and on HFQ
(ECC/R3) 2026-07-27 (issue #106 comments). Relevant facts:

- **Root cause is session reuse, not "REST cannot generate the load"** (2×2
  above). A fresh session generates the load and returns real output on S4U over
  pure HTTP.
- Defect 1 is purely runtime-load-generation state — a trivial pure-`out->write`
  class with no DB/EML access reproduces it identically. It is **not** a DB/RAP
  problem.
- The `text/plain` soft-fail body is produced by the handler at HTTP 200; there
  is no structured error channel and no ST22 dump.
- **The soft-fail string is cause-ambiguous** (verified 2026-07-27, S4U): a class
  that does *not* implement `IF_OO_ADT_CLASSRUN` but *does* have a generated load
  returns the **identical** `Error: Class does not implement …main…`. So the same
  body means load-not-generated *or* genuine non-implementer *or* not-instantiable.
  Drives the error naming in Option A.
- **The load generated in a fresh session is session-local** (a subsequent run in
  a *different* session on S/4 regenerates it). This is exactly what defect 1
  needs — every `RunClass` on its own fresh session returns correct output — but
  it does **not** produce a persistent load, so it does not by itself address
  defect 2.
- **The MCP consumer reuses one long-lived adtler client** across the whole
  create → activate → run lifecycle, which is why `run_class` lands in the worn
  session and soft-fails. (Confirmed by the fact that a fresh adtler client runs
  the same class fine — scenario A.)

## Scope

**In scope:** making `RunClass` behave correctly for a fresh class driven purely
over ADT REST. Two independent, composable changes:

1. **Option A — classify the masked soft-fail as a real error** (no object
   mutation, no session change). Robustness improvement; still recommended so a
   genuinely non-executable class surfaces as an error rather than a fake success.
2. **Option C — run the classrun on an isolated fresh HTTP session** so a fresh
   class actually executes and returns real output. **Recommended primary fix
   for defect 1.** No object mutation, no transport, no lock.

**Out of scope:** defect 2 (stale load) in-place fix; any DDIC/RAP-specific
behaviour; changing the classrun request contract (still stateless
`POST … Accept: text/plain`). Option B (test-include mutation) is dropped.

## Option C — run the classrun on an isolated fresh session (recommended primary fix)

Because the trigger is session reuse, the fix is to make `RunClass` never run
the classrun in the caller's worn session. Instead it performs the classrun POST
on a **single-use, isolated HTTP session** created just for the run:

- a dedicated `*http.Client` with its **own fresh cookie jar** (not the shared
  `c.http`/`c.httpLong` jar);
- its **own CSRF preflight** (the fresh session fetches its own token);
- the classrun `POST … Accept: text/plain` on that jar;
- the session is discarded when `RunClass` returns.

This is invisible to the caller: it touches **no** locks, no stateful session,
no object, no transport. It is safe under concurrency (a private jar, not a
mutation of the shared one). classrun is already documented as stateless, so an
isolated session is consistent with the contract.

### Why not just `Logout()` before the run?

`Logout()` resets the shared client's jar and CSRF (verified to fix the bug —
scenario L2), but as a side effect it **drops the caller's stateful session and
any locks held under it**. A "run" call must not have that side effect. An
isolated sub-session gives the same freshness surgically.

### Sketch

```go
// freshSession returns a single-use *httpClient that shares cfg/auth but has a
// brand-new cookie jar and no CSRF token, so its first request establishes a
// clean SAP session. Used by RunClass so the classrun never runs in the
// caller's worn session (issue #106 defect 1).
func (c *httpClient) freshSession() *httpClient { /* new jar + clients, copy cfg/token */ }

func (c *httpClient) RunClass(ctx context.Context, className string) (*ClassRunResult, error) {
    fresh := c.freshSession()
    uri := "/sap/bc/adt/oo/classrun/" + strings.ToLower(className)
    resp, err := fresh.doMutate(ctx, http.MethodPost, uri, nil,
        map[string]string{"Accept": contentTypeTextPlain})
    // ... unchanged: checkResponse, read body, (Option A) classify soft-fail ...
}
```

`freshSession` copies `cfg`, `accessToken`, `onTokenRefresh`, and `pollInterval`,
builds a new transport from `cfg.TLSSkipVerify`, and installs a fresh
`cookiejar`. (Caveat: an OAuth token refreshed inside the single-use session is
not propagated back to the parent client — acceptable for a one-shot run; note
it in the godoc.)

### Interaction with Option A

Option C makes a fresh class return real output. Option A still classifies the
soft-fail body as `ErrClassNotExecutable` — which, after Option C, should only
appear for a genuinely non-executable class (real non-implementer, not
instantiable), not for the fresh-load case. Ship both: C fixes the common case,
A gives a correct error for the residual real failures.

## Option A — surface the masked soft-fail as an error (robustness, keep)

Today `RunClass` returns *any* HTTP 200 body as `ClassRunResult.ConsoleOutput`,
so a create-object soft-fail arrives at the caller as a **success** whose output
happens to be an error string. When the console body is the known "does not
implement …main…" soft-fail, return a typed error instead.

```go
// ErrClassNotExecutable indicates the classrun handler returned its
// "does not implement if_oo_adt_classrun~main method" soft-failure at HTTP 200
// instead of real console output (see issue #106). This is a HEURISTIC
// classification of a text/plain body — SAP exposes no structured error channel
// here — and the message is CAUSE-AMBIGUOUS. After the Option-C fresh-session
// fix, the load-not-generated cause should no longer produce it; the residual
// causes are (a) a class that genuinely does not implement IF_OO_ADT_CLASSRUN
// and (b) a class that cannot be instantiated for other reasons
// (cx_sy_create_object_error). The name is therefore the OBSERVABLE effect
// ("not executable via classrun"), not a presumed cause.
var ErrClassNotExecutable = errors.New("classrun: class not executable via classrun (handler returned 'does not implement ...main...' at HTTP 200)")
```

`RunClass` returns `fmt.Errorf("RunClass %s: %w", className, ErrClassNotExecutable)`
when the body matches, so callers can `errors.Is` it while still seeing the class
name.

### Detection

Match on the exact/prefix handler string
`Error: Class does not implement if_oo_adt_classrun~main method!` (emitted
verbatim by `CL_OO_ADT_RES_CLASSRUN`). Narrow, low false-positive risk; a class
that deliberately `out->write`s that exact string is a pathological, accepted
edge case. Keep matched strings hoisted as constants.

Cause-ambiguity carve-outs (unchanged from the prior revision): the same body is
emitted by a genuine non-implementer, a non-instantiable class, and — before
Option C — the missing-load case. A non-existent class returns 200-with-text
(not 404); the consumer's existence pre-check (`GetObjectInfo`) catches that
before `RunClass`. Missing `S_DEVELOP` auth returns a *different* string and is
left as-is.

## Defect 2 is out of scope

The fresh-session load is **session-local**, so Option C does not create a
persistent load and does not fix defect 2 (a re-activated class still serving a
stale persistent load to sessions that already hold one). A durable defect-2 fix
needs whatever in-place invalidation Eclipse triggers on F9; only `DELETE` was
observed to evict a persistent load over REST (destroying object identity, so
not usable). Tracked under `blocked:eclipse-capture` on issue #106.

## Error handling

- Option A adds one classification branch before returning success; all existing
  HTTP-error handling (`checkResponse` → `ADTError`) is unchanged.
- Option C changes only the transport/session the classrun runs on; the request
  contract (`POST … Accept: text/plain`, empty body) is unchanged.
- **Behavioural compatibility:** Option A flips `RunClass` from
  `(*ClassRunResult, nil)` to `(nil, error)` for the soft-fail body. With Option
  C shipped, S/4 fresh classes now return real output instead of the soft-fail,
  so the error path is hit far less often. Ship in a minor tag with a changelog
  note; the aibap.mcp consumer bump adapts to the new error return.

## Testing

**Unit (`httptest` mock, no build tag):**
- **Option C isolation:** after the client has an established session (a prior
  request set `SAP_SESSIONID=PARENT` and cached a CSRF token), `RunClass` must
  perform its **own** CSRF preflight on a **fresh jar** — assert the classrun
  POST does **not** carry the parent session cookie, and that a fresh CSRF Fetch
  GET was issued for the run. Server returns `V1`; assert success.
- **Option A:** 200 body = exact
  `Error: Class does not implement if_oo_adt_classrun~main method!` →
  `RunClass` returns `ErrClassNotExecutable` (assert `errors.Is`), not a success.
- 200 body containing the word `Error` elsewhere → still a success result
  (guards the matcher against over-broad matching).
- Existing success / UTF-8 / HTTP-error cases stay green.

**Integration (`//go:build integration`, `eachSystem(t)` over R/3 **and** S/4):**

Create + set source + activate a fresh classrun class purely over ADT REST **in
one long-lived client** (reproducing the worn-session lifecycle), then
`RunClass` on that same client. With Option C, assert **real output on both
systems**:

- **HFQ (ECC) — regression guard.** classrun already worked here (activation
  regenerates a persistent load); the fresh-session fix must not break it.
  Assert real output.
- **S4U (S/4) — the fix.** Pre-fix this soft-fails in the reused session;
  post-fix Option C runs the classrun on a fresh session and returns real
  output. Assert real output.

Asserting identical correct behaviour on both systems is the point: HFQ proves
no regression, S4U proves the fix. (A pre-fix run of the same test would fail
only on S4U, which is the bug this closes.)

Fixtures in `Z_ADT_MCP_TEST`; `$TMP` scratch classes cleaned up.

## Rollout / linkage

- **Phase 1:** ship Option A **and** Option C together in the next adtler minor
  tag. Option C makes `run_class` return real output for fresh S/4 classes;
  Option A turns any residual soft-fail into a real error instead of a fake
  success.
- The aibap.mcp `run_class` consumer references the adtler tag, drops its interim
  workaround note, and no longer needs to reuse-or-refresh a session itself.
- **Defect 2 (`blocked:eclipse-capture`):** separate work item; not gated on the
  above.

## Note: broken integration build (separate issue)

While verifying the fix, the `-tags=integration` build of package `adt_test` was
found broken since the classrun merge (#100): `classrun_integration_test.go`
references a package-level `ctx` that is declared nowhere, so
`go build -tags integration ./adt/...` and `go vet -tags integration ./adt/...`
fail on `main`. CI does not build the integration suite (needs SAP), so it went
unnoticed. Fixed under a separate small issue/PR (add
`var ctx = context.Background()` in an integration-tagged file); the defect-1
integration test above depends on that build compiling.
