# Released request is not ErrorNotSupported (#288) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** `AddToTransport`, `CopyTransportObjects` and `AddPackageToTransport` report a released (or release-in-progress) target as a request-specific error instead of `ErrorNotSupported`, without the "use SE09" hint.

**Architecture:** The two capability gates in `adt/transport_copies.go` (`ensureAddObjectSupported`, `ensureAddObjectsSupported`) both call `advertisesRelation`, which reads the request once and discards the body after collecting relations. It also parses the status from the same body (`parseTransportInfo`, already used by `copySources`) and returns the `TransportState`. When the relation is missing and the state is released or release-in-progress, the gates return a new locally synthesised `ADTError` type `ExceptionTypeRequestReleased`, which `ClassifyError` maps to `ErrorBadRequest`. Modifiable or unreadable status keeps today's `ErrorNotSupported` error. The gate still makes one read and still fails closed.

**Tech Stack:** Go, httptest fakes (`copiesServer`, `copiesRequestXML` in `adt/transport_copies_test.go`).

**Spec:** GitHub issue #288.

## Global Constraints

- No new dependency, no new file besides this plan; public API only gains one exported constant.
- Fail closed: no PUT is sent when the relation is missing, whatever the status.
- A status that cannot be read (body does not parse, number not found in body, empty or unknown status letter) keeps the old `ErrorNotSupported` behaviour.
- Public repository: no internal data in code, tests, comments, commit messages.

## Review Focus

- Release in progress (status `O`) must be reported like released (test: table case).
- Released with import protection (status `N`) must be reported like released (table case).
- A released request that advertises the relation anyway is not refused by the gate (existing behaviour: relation present means `nil`; unchanged).
- A modifiable request without the relation (status `D`) stays `ErrorNotSupported` (test).
- ECC worklist body (no readable status for the number) stays `ErrorNotSupported` (existing tests `TestAddToTransport_ECCSendsNoPUT`, `TestCopyTransportObjects_UnsupportedSystemSendsNoPUT`).

### Task 1: Report a released target as ExceptionTypeRequestReleased

**Files:**
- Modify: `adt/types.go` (constant next to `ExceptionTypeAddObjectUnsupported`, with a doc comment: synthesised locally, no write sent, the request or task is released or its release is in progress)
- Modify: `adt/errorkind.go` (`classifyByExceptionType`: `ExceptionTypeRequestReleased` returns `ErrorBadRequest`; add a sentence to the `ErrorNotSupported` doc comment that a released request is a property of one request and is not reported as this kind)
- Modify: `adt/transport_copies.go`
- Modify: `adt/transport.go` (doc comments of `AddToTransport`; `CopyTransportObjects` and `AddPackageToTransport` doc comments in `adt/transport_copies.go`: a released target now yields `ExceptionTypeRequestReleased` / `ErrorBadRequest`)
- Test: `adt/transport_add_test.go`, `adt/transport_copies_test.go`, `adt/errorkind_test.go` (add a row for the new type to the table `TestClassifyError_ByExceptionType`), `adt/transport_add_integration_test.go` (build tag `integration && transport`, add `TestAddToTransport_ReleasedRequest_Integration`: per system via `eachSystem`; skip unless `sapBasisRelease` >= 816; fetch one released request with `GetTransportRequests(ctx, TransportListOptions{User: TransportAllUsers, Filter: TransportFilterReleased, Limit: 1})`, `t.Skip` if none; call `AddToTransport`, `CopyTransportObjects` (source = the same request is refused, so use a second distinct released request if the page has one, else skip that call) and `AddPackageToTransport` against it; the gate refuses before any write, so nothing is created; assert Type `ExceptionTypeRequestReleased`, kind `ErrorBadRequest`; log counts only, never request numbers or names; use the existing `addPgmID`/`addObjType` constants and a plain placeholder object name). It is compile-checked only (`go vet -tags='integration transport' ./...`); never run it.
- Docs: `CLAUDE.md` line 239 ("Filling a transport of copies"): add one sentence that a missing relation on a released or release-in-progress target is reported as `ExceptionTypeRequestReleased` (`ErrorBadRequest`), not `ErrorNotSupported`, when the status is read from the same body (#288). README.md has no mention.
- Stale doc comments to update (they say "or released" and the old kind): `adt/types.go` doc comments of `ExceptionTypeAddObjectsUnsupported` and `ExceptionTypeAddObjectUnsupported`; `adt/transport_copies.go` doc comments of `CopyTransportObjects`, `AddPackageToTransport`, `ensureAddObjectsSupported`, `ensureAddObjectSupported`, `advertisesRelation`; `adt/transport.go` doc comment of `AddToTransport`; `adt/errorkind.go` doc of `ErrorBadRequest` (widen: also a target request that is in the wrong state, such as a released one, see `ExceptionTypeRequestReleased`). State in the gate comments that the status is only used when the body names the addressed number as a request; on ECC a released request is absent from the worklist, so it stays `ErrorNotSupported` there, and a task whose GET body does not carry it as `tm:request` also keeps the old behaviour (shape of a task GET not measured).

**Interfaces:**
- Produces: `const ExceptionTypeRequestReleased = "ADT_TM_REQUEST_RELEASED"` (package `adt`).
- Changes: `func (c *httpClient) advertisesRelation(ctx context.Context, number, rel string) (bool, TransportState, error)`; the state is `TransportStateUnknown` when `parseTransportInfo(data, number)` returns an error. A read error is still returned unwrapped.
- Adds: `func requestReleasedError(number string) *ADTError` with message `transport request or task <number> is released and cannot take new entries` (and for release in progress the same text: "is released or being released" is acceptable if it reads naturally; no SE09 hint).

- [ ] **Step 1: Write the failing tests**
  - In `adt/transport_add_test.go` add `assertRequestReleased(t, err)` (Type == `adt.ExceptionTypeRequestReleased`, `ClassifyError == adt.ErrorBadRequest`, message does not contain `SE09`, message contains the transport number). Change `TestAddToTransport_ReleasedRequestSendsNoPUT` into a table over status `R`, `N`, `O` using `copiesRequestXML(addTask, status, "", "adturi")`, asserting `assertRequestReleased` and zero PUTs.
  - Add `TestAddToTransport_ModifiableWithoutRelationIsNotSupported`: status `D`, rels `modify` only; `assertAddObjectUnsupported`, zero PUTs.
  - In `adt/transport_copies_test.go` change `TestCopyTransportObjects_TargetWithoutRelationSendsNoPUT` to expect the released error (same assertion shape; add `assertRequestReleased` usage, define it once in `transport_add_test.go`), and add `TestAddPackageToTransport_ReleasedTargetSendsNoPUT` with `copiesRequestXML(copiesTarget, "R", "", "adturi")`. Add `TestCopyTransportObjects_ModifiableTargetWithoutRelationIsNotSupported` (status `D`, rels `modify`, `assertNotSupported`).
  - Add the table row for `ExceptionTypeRequestReleased` -> `ErrorBadRequest` in `adt/errorkind_test.go`.
  - Guard tests, not failing first (exception to the failing-test-first rule, named in the PR): `TestAddToTransport_ModifiableWithoutRelationIsNotSupported` and `TestCopyTransportObjects_ModifiableTargetWithoutRelationIsNotSupported` pass before the fix; they pin that status `D` keeps `ErrorNotSupported`. The integration test is compile-checked only (never run here): exception, no SAP access.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `bash <helper> run --gate test ./adt/ -run 'Released|ModifiableWithoutRelation|ModifiableTargetWithoutRelation|RequestReleased'`
Expected: compile error (`ExceptionTypeRequestReleased` undefined), after adding only the constant: the released tests fail with `Type = "ADT_TM_ADDOBJECT_UNSUPPORTED"`.

- [ ] **Step 3: Implement**
  - Constant, classification and doc comments as listed under Files.
  - `advertisesRelation`: after collecting the relations, `info, perr := parseTransportInfo(data, number)`; state is `info.State` when `perr == nil`, else `TransportStateUnknown`.
  - Both gates: `ok, state, err := c.advertisesRelation(...)`; `ok` returns nil; then `if state == TransportStateReleased || state == TransportStateReleaseInProgress { return requestReleasedError(number) }`; else the existing `ADTError`. Remove the SE09 hint from neither of the old messages (they apply to the not-supported case only); but drop "never offered for a released request" / "nor for a released request" from the old messages, since that case no longer ends there.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `bash <helper> run --gate test ./adt/ -run 'Transport|ClassifyError'`
Expected: ok. The implementer also runs `--gate vet` (which uses `-tags=integration transport`). The lead runs the full gate afterwards.

- [ ] **Step 5: Commit**

```bash
git add docs/plans/issue-288-released-request-error.md adt/types.go adt/errorkind.go adt/transport.go adt/transport_copies.go adt/transport_add_test.go adt/transport_copies_test.go adt/errorkind_test.go
git commit -m 'fix(#288): report a released target request as ErrorBadRequest, not ErrorNotSupported' -m 'Bugspecht-Sprint: bs-20261124-2145-tsh5'
```

(Co-author trailer per the harness attribution rule is added as well.)
