# Plan: ECC transport-response parsing, task attribution, and a removeobject capability gate

Branch: `fix/125-ecc-transport-parsing` (worktree `adtler-125`), from `origin/main` @ 297b618.

Closes [adtler#125](https://github.com/Hochfrequenz/adtler/issues/125). Unblocks — but does
not by itself close — the consumer issues
[aibap.mcp#493](https://github.com/Hochfrequenz/aibap.mcp/issues/493),
[#495](https://github.com/Hochfrequenz/aibap.mcp/issues/495) and
[#496](https://github.com/Hochfrequenz/aibap.mcp/issues/496). #493 additionally needs
consumer-side work (its finding 2) that this plan does not cover.

Revision 2, after an independent plan review. The review's findings are folded into the
task text below; where it forced a decision, the decision is stated rather than left to
whoever reaches it.

## Background

`GET /sap/bc/adt/cts/transportrequests/<number>` answers differently by release:

- **S/4** (measured on SAP_BASIS 816) returns that one request:
  `<tm:root><tm:request tm:number=…><tm:task><tm:abap_object …/></tm:task></tm:request>`,
  with `position` attributes populated and per-object atom links including
  `http://www.sap.com/cts/relations/removeobject`.
- **ECC** (measured on SAP_BASIS 750) **ignores the number in the path** and returns the
  transport-organizer worklist, wrapped in a `<workbench>` group with a section element
  beneath it, with no `position` attribute anywhere and only four atom relations:
  `consistencycheck`, `modify`, `newtask`, `releasejobs`.

Two measured properties of the ECC worklist shape the plan and are easy to get wrong:

- It is **not** user-scoped — a measurement on HFQ returned 8 requests across four
  different owners.
- It **is** modifiable-scoped — all 8 were status `D`. A released request is not in the
  body at all. Task 2 turns "absent from the body" into an error, so released transports
  on ECC become a diagnosable failure rather than a silently wrong answer. That is an
  intended, stated limitation of this plan; see Task 2.

Three defects follow from the response shape:

1. `parseTransportObjectsXML` takes no transport number and iterates every request in the
   worklist, so on ECC `GetTransportObjects` returns every transport's objects regardless
   of which one was asked for.
2. `parseTransportInfo` looks for `request` as a direct child of the root, which the
   workbench wrapper defeats. `GetTransportInfo` therefore always errors on ECC, and
   `ReleaseTransportVerified` treats a failed read as success — so silent-release
   detection, and the consumer's fallback release path, never fire on ECC.
3. Removing an object from a transport is not implemented in ADT below AS ABAP 7.53 SP00.
   On ECC the `PUT` that `RemoveFromTransport` issues reaches a legacy handler that
   unconditionally performs a **change-owner**, reads no request body, and takes the target
   user from a `targetuser` query parameter the library never sends — producing
   `400 TR 809` with a blank user. Evidence and the full call chain:
   https://github.com/Hochfrequenz/adtler/issues/125#issuecomment-5696257590

Separately, `TransportObject` carries no field for the task that recorded it, although
`parseTransportObjectsXML` walks `req.Tasks` and has `task.Number` in hand.

## Global Constraints

- **Never merge, never push to `main`.** All work stays on `fix/125-ecc-transport-parsing`.
  Pushing the feature branch is allowed; opening or merging a PR is not part of this plan.
  Where adtler's `CLAUDE.md` cycle continues (PR → `needs:integration-test` → reviewer agent
  → CI → SAP-access agent → merge), that pickup happens after this plan ends.
- **SAP writes.** No test or probe added by this plan may create, release, delete, or
  reassign a transport, remove an object from one, or write any repository object.
  `RemoveFromTransport` is never invoked against a system that supports it.
  **This is not automatic:** `TestMain` in `adt/fixtures_integration_test.go:103` creates a
  transport via `setupFixtures` (`:167`) on every integration run where a host resolves, and
  only releases it when fixtures were actually created. Every integration or probe run in
  this plan therefore uses this exact env triple, which makes `integrationConfig()` return
  an empty host so `TestMain` skips setup, while `eachSystem` still reads the plural
  variable and runs both systems:

  ```
  SAP_INTEGRATION_SYSTEMS=HFQ,S4U SAP_INTEGRATION_SYSTEM=NONE go test -tags=integration ...
  ```

  If a run prints `=== Integration test setup: ensuring fixtures exist ===` followed by a
  `[transport]` line, the guard did not work — stop and report it rather than continuing.
  The `integration transport` build-tag suite (`adt/transport_remove_integration_test.go`)
  creates and releases real transports and is **out of scope**; it is deferred to the
  post-plan SAP-access agent, which must run it because this plan changes the code path it
  covers.
- Before every commit: `gofmt -l .` prints nothing, `go vet ./...` is clean, and
  `go test ./...` passes. Run `go build -tags integration ./adt/...` and
  `go vet -tags integration ./adt/...` when integration files change.
- `golangci-lint run --enable dupl,goconst,gocyclo ./...` must stay clean — the baseline is
  `0 issues`. Hoist repeated string literals to constants. `dupl` watches the near-identical
  Format-1 / Format-2 walks that Task 2 touches.
- Unit tests live in `package adt_test` and drive the parsers through the public API with
  `httptest` servers and inline XML, matching `adt/transport_test.go`.
- Integration tests use `eachSystem(t)` and exercise both R/3 and S/4.
- **Do not add methods to the exported `Client` / `TransportClient` interfaces**
  (`adt/client.go:124-139`). aibap.mcp's `tools/source_test.go` has a `mockClient`
  implementing the full interface; a new method breaks its build. Test-only access goes
  through `adt.TestClient` in `adt/export_internal_test.go`, which carries no build tag and
  is therefore available to the integration binary too.
- `TransportObject` is consumed by aibap.mcp. Adding a field is fine; renaming or removing
  an existing one is not.
- Do not spawn subagents.

## Task 1 — Capture the two real response shapes as test fixtures

Every later task is tested against these, so they must reflect what the servers actually
send rather than what this plan describes. The plan's prose is a summary and is not
authoritative about element names.

Write a temporary, read-only probe (in-package `package adt`, build tag `integration`, so it
can call `readTransportXML` directly — `adt/transport_syst_cust_investigation_integration_test.go`
shows the pattern and its `issue63Systems` helper can be reused) that fetches
`/sap/bc/adt/cts/transportrequests/<number>` from both systems and writes the raw bodies to
disk **outside the repo**. Run it with the env triple from the Global Constraints, plus
`PROBE_TRANSPORT_HFQ=HFQK902952`. For S4U pick a **small** request — the S/4 bodies measured
so far reached 10.3 MB and `c.http` has a 30-second timeout (`adt/client.go:222`); choose one
by enumerating `GetTransportRequests(user, "D")` and taking a request with a short object
list. (`/ACCGO/ACMS41709FP00` is known to work but is the 10.3 MB one; prefer smaller.)

Reduce each body by hand to a small fixture preserving the structural features later tasks
depend on, and add them as Go string constants in a new file `adt/transport_ecc_test.go`
(`package adt_test`):

- `eccWorklistXML` — the ECC worklist shape **as captured**, including whatever group and
  section elements the real body uses. Do not write a literal `<section>` element from this
  plan's prose; the existing parsers match the section level with `xml:",any"` precisely
  because its real name (likely `tm:modifiable`) is not `section`. Must contain at least two
  `<request>` elements with **different** numbers, each with at least one `<task>` holding
  `<abap_object>` children, **no `position` attribute anywhere**, and the ECC atom links.
- `eccWorklistEmptyNumberXML` — a hand-edited variant of the above in which one request
  carries `number=""` (or no `number` attribute) while still holding objects. Task 2's
  filter rule is defined against this.
- `eccCustomizingXML` — a hand-edited variant in which a request sits under the
  `customizing` group rather than `workbench`. Task 2 must not drop it.
- `s4SingleRequestXML` — the single `<tm:request>` shape with tasks, populated `position`
  attributes, and per-object atom links including the `removeobject` relation.
- `s4RequestNoObjectsXML` — an S/4 request holding **no** `abap_object` at all, keeping the
  request-level atom links. Task 5's capability rule is defined against this; getting it
  wrong makes the gate block a system that supports removal.
- `s4ObjectAtBothLevelsXML` — an S/4 variant in which one object appears **both** directly
  under `<request>` and under a `<task>`, same pgmid/type/name. Hand-built is expected;
  Task 3's dedup rule is defined by it.

While reducing the captures, **record in the task report whether ECC emits its four atom
relations at the `<abap_object>` level or at the `<request>` level.** Task 5 needs this and
it has not been determined.

Delete the probe before committing. Commit only the fixture file.

**Acceptance:** the constants exist and each is well-formed XML; and assertions prove the
structural properties the later tasks rely on, not merely well-formedness —
`eccWorklistXML` contains at least two distinct request numbers and **zero** occurrences of
`position=`; `s4SingleRequestXML` contains the `removeobject` relation and `eccWorklistXML`
does not; `s4RequestNoObjectsXML` contains no `abap_object`; `s4ObjectAtBothLevelsXML`
contains the same object at both levels. `go test ./...` passes.

## Task 2 — Filter the object list by transport number, and bind the whole document

Three changes in one task, because they touch the same struct and the same walk.

**(a) Hoist the document struct.** `parseTransportTaskNumbers` (`adt/transport.go:691-700`)
and `parseTransportObjectsXML` (`:749-758`) each declare their own anonymous struct for the
same document. Hoist one named type both use. Extend `xmlRequest` (`:742-746`) with the
`owner`, `desc` and `status` attributes, which Task 4 needs. Bind the **`customizing` group
as well as `workbench`** — today only `workbench` is bound, so a customizing request's
objects are dropped; `GetTransportRequests` (`:426-430`) already walks both groups and is the
precedent.

**(b) Filter by number.** Change `parseTransportObjectsXML(data []byte)` to
`parseTransportObjectsXML(data []byte, transportNumber string)` and filter the worklist
branch.

**The filter rule, stated exactly, because the existing guard is wrong and must not be
copied:** skip any request whose number does not equal `transportNumber`. A request with an
empty or absent `number` attribute **never matches** and is always skipped. The guard at
`:707` reads `req.Number != transportNumber && req.Number != ""`, whose trailing clause lets
an unnumbered request through into every result — do not reproduce it. Compare
case-insensitively, since aibap.mcp passes the caller's string through unchanged
(`tools/transport.go:282`).

**(c) Distinguish absent from empty.** The addressed request **present** and holding no
objects → empty slice, nil error. The addressed request **not present in the body** → an
error naming the transport and saying why this can happen, because on ECC it will be hit by
every released request:

> transport %s is not in this system's transport-organizer worklist; on ECC that endpoint
> returns only modifiable requests, so released requests cannot be read this way

A silently empty list must not be the answer to "the server did not send me that request".

The single-request (Format 1) branch keeps working when `transportNumber` is empty or
matches; a Format 1 body whose number **differs** is treated as absent, identically to the
worklist branch.

Update `GetTransportObjects` (`:649-655`) to pass its `transportNumber` through. Align
`parseTransportTaskNumbers` onto the same filter rule and the same absent/empty distinction
while you are in there, so the three parsers on this body stop disagreeing.

**Known consequence, accepted:** `RollbackTransport` (`adt/rollback.go:43`) calls
`GetTransportObjects` and is designed for released, imported transports, so on ECC it now
returns this error instead of a wrong object list. An E071-based fallback (mirroring
`getTransportRequestsViaQuery`, `adt/transport.go:460`) is **out of scope** for this plan.

**Acceptance:** over `eccWorklistXML`, asking for each of the two request numbers returns
only that request's objects and the two results differ; a number absent from the body
returns the error; over `eccWorklistEmptyNumberXML`, the unnumbered request contributes no
objects to any result **and** does not make the "present" check succeed; over
`eccCustomizingXML`, the customizing request's objects are returned; `s4SingleRequestXML`
returns that request's objects as before; a lowercase transport number matches. Existing
tests still pass. `go test ./...` passes.

## Task 3 — Carry the owning task on each object

Add `Task string` with tag `json:"task,omitempty"` to `TransportObject`
(`adt/transport.go:616-622`), documented as the task number that recorded the entry, empty
when the response did not attribute it.

Fill it from the enclosing `task.Number` in `addFromRequest` (`:772-781`).

**The dedup rule is the substance of this task.** The `seen` map is keyed
`pgmid/type/name` and is first-wins, and request-level objects are added before task-level
ones, so a plain implementation yields an empty `Task` for exactly the objects recorded at
both levels — the case the field exists for. When an entry already exists without a task and
a task-attributed duplicate arrives, **upgrade the existing entry in place**: set its `Task`,
and leave every other field — including `Position` — as first seen. Keep result order stable.

**Acceptance:** over `s4SingleRequestXML`, each object carries the task number holding it;
over `s4ObjectAtBothLevelsXML`, the doubly recorded object appears exactly once, carries the
task number, and keeps its first-seen position; over `eccWorklistXML`, attribution survives
Task 2's filtering. `go test ./...` passes.

## Task 4 — Make `parseTransportInfo` handle the worklist shape

`parseTransportInfo` (`adt/transport.go:666-681`) must also accept the worklist form and
select the request whose number matches `transportNumber`, using the named document struct
and the extended `xmlRequest` from Task 2 rather than a third spelling of the same shape. It
returns an error only when the addressed request is genuinely absent — reuse Task 2's error
text, since the cause and the caller's remedy are identical.

**Acceptance:** over `eccWorklistXML`, the requested request's number, status, owner and
description come back, and a number absent from the body errors; `s4SingleRequestXML` still
passes. **Plus the regression guard for the defect this actually fixes:** a unit test in the
style of `adt/release_verified_test.go` that serves `eccWorklistXML` on the post-release
status read and asserts `ReleaseTransportVerified` returns `Released: false` — parser-level
tests alone do not cover aibap.mcp#496. `go test ./...` passes.

## Task 5 — Detect `removeobject` support and cache it per client

Parse the `rel` attribute of the `<atom:link>` elements in the transport XML (the attribute
is unprefixed; `encoding/xml` matches on local name, so namespace prefixes are irrelevant
here) and derive whether the server offers
`http://www.sap.com/cts/relations/removeobject`.

**The derivation rule, stated exactly, because the obvious rule is wrong:** the relation is
emitted **per `abap_object`**, not once per request. So:

- A response containing **at least one `abap_object` that carries atom links**, none of
  which is `removeobject` → **unsupported**.
- A response containing **no object-level atom links** — no objects at all, or objects
  without links → **unknown**.

A rule that keys on request-level relations would classify an S/4 request with zero recorded
objects as unsupported and cache that for the life of the client, blocking removal on a
system that supports it. `s4RequestNoObjectsXML` exists to catch that.

Store the result on `httpClient` as a tri-state (unknown / supported / unsupported), guarded
by the existing mutex, in the spirit of the cached discovery document. The capability is a
property of the **system**, not of the addressed request. Populate it as a side effect of any
successful `readTransportXML`, and **skip the derivation entirely once the state is known** —
otherwise every transport read pays a second `xml.Unmarshal` over a body measured at 754 KB
on R/3 and up to 10.3 MB on S/4.

Expose it for tests by adding a method to the `TestClient` interface in
`adt/export_internal_test.go` — **not** to `Client` or `TransportClient` (see Global
Constraints). Note that `freshSession()` (`adt/client.go:255-272`) builds a new `*httpClient`
and copies no cache, so its state starts unknown; that is correct and must not be "fixed" by
sharing the field across sessions.

While in this function, fix `readTransportXML`'s doc comment (`:624-625`), which claims a 406
fallback the body does not implement.

**Acceptance:** `eccWorklistXML` yields unsupported; `s4SingleRequestXML` yields supported;
`s4RequestNoObjectsXML` leaves the state unknown; a body with no links leaves it unknown.
Caching is asserted observably: after one `GetTransportObjects` against the ECC body, a
subsequent `RemoveFromTransport` issues **no** further GET (count the requests the httptest
server receives). `go test ./...` passes.

## Task 6 — Gate `RemoveFromTransport`

Before issuing the `PUT`, `RemoveFromTransport` (`adt/transport.go:543-576`) consults the
capability from Task 5. When the state is unknown, populate it with one read of the parent
transport.

**The gate fails open, stated exactly:** only a **confirmed `unsupported`** blocks the call.
A state that is still unknown after the read, or a capability read that fails, proceeds with
the `PUT`. Failing closed on a system that could not be classified would break setups that
work today; the gate exists to stop a known-bad call, not to demand proof of goodness.

The error must be recognisable by the consumer, which branches on `adt.ErrorKind`
(`aibap.mcp/tools/errors.go:69`, fed by `adt.ClassifyError` at `:134`) and classifies only
`*ADTError` (`adt/errorkind.go:100-107`) — a bare `errors.New` sentinel would arrive as
`ErrorUnknown` and get no hint. So: return an `*ADTError` carrying a synthetic `Type`
(`ADT_TM_REMOVEOBJECT_UNSUPPORTED`), add an `ErrorNotSupported` kind to `adt/errorkind.go`
with its `String()` case and a `classifyByExceptionType` mapping, and wrap with `%w` so
`errors.As` survives `fmt.Errorf`.

Word the message from the capability, not from a version — the gate is capability-derived and
an absolute version claim could be wrong on a system where the relation is missing for
another reason:

> this system's ADT does not advertise a remove-object operation for transport entries
> (added in AS ABAP 7.53 SP00 / ABAP Platform 1809); remove the entry in SE09 instead

Not sending the PUT is the point of this task, not only the better message: on a pre-7.53
system that request is interpreted as a change-owner with a missing parameter.

**Acceptance:** an `httptest` server that fails the test if any `PUT` reaches it, primed with
`eccWorklistXML` for the capability read, makes `RemoveFromTransport` return an error that
`errors.As` resolves to `*adt.ADTError` and `ClassifyError` reports as `ErrorNotSupported`; a
server primed with `s4SingleRequestXML` still issues the `PUT` with the body it issues today;
a server whose capability read fails still issues the `PUT`. **And** the existing
`TestRemoveFromTransport` (`adt/transport_test.go:176`) is updated — its server answers every
path with an empty body, so the new capability GET leaves the state unknown; prime it with
`s4SingleRequestXML` and keep its assertion on the PUT body. `go test ./...` passes.

## Task 7 — Integration tests and an end-to-end run against a local build

Both parts use the env triple from the Global Constraints. Read-only throughout.

**Part A — adtler integration tests** via `eachSystem(t)`:

- `GetTransportObjects` for two different modifiable requests on each system returns
  per-request results that differ. Select the data rather than assuming it: enumerate
  `GetTransportRequests(user, "D")` and walk until two requests with differing, non-empty
  object lists are found; `t.Skip` with a clear message if the system offers fewer than two.
  (S/4 may answer via the E070 fallback at `adt/transport.go:444-447` and hand back empty
  requests, which would fail the assertion spuriously.) On R/3 this is the regression guard
  for adtler#125.
- `GetTransportInfo` succeeds on both systems and returns the number it was asked for — the
  regression guard for aibap.mcp#496.
- The Task 5 capability is unsupported on R/3 and supported on S/4, reached via
  `sys.Client.(adt.TestClient)`.
- `RemoveFromTransport` against **R/3 only** returns the typed error from Task 6, with the
  arguments from aibap.mcp#493: task `HFQK902953`, parent `HFQK902952`, `R3TR CLAS
  ZCL_LOCKREPRO_2`, wbtype `CLAS/OC`, position `000001`. Never run this against S/4. Even if
  the gate were to fail open here, the resulting PUT writes nothing and leaks no enqueue —
  established in the adtler#125 investigation — but assert the typed error, not the outcome.

Record the output in the task report.

**Part B — end-to-end through a locally built consumer.** Work in a **scratch copy** of the
aibap.mcp checkout (the real one is dirty); nothing is committed there and there is nothing
to revert.

- Add `replace github.com/Hochfrequenz/adtler => <path to this worktree>` to the copy's
  `go.mod`.
- `go build .` and `go test ./...` must both pass against the replaced module. The build is
  the real check here: it is what catches an interface change that breaks `mockClient`.
- Then exercise the library through the consumer's own module with a scratch `main.go` in the
  copy that calls `adt.NewClient(...)` and `GetTransportObjects` for two requests per system,
  printing the results. Do **not** attempt to drive the built MCP binary over stdio JSON-RPC:
  the agent's `mcp__sap-adt__*` tools are bound to the already-installed server, not the new
  build, so calling them would report the old behaviour.

**Acceptance:** both parts run and their output is in the task report. Part A is committed to
this branch; Part B leaves no trace in the consumer repository.

## Task 8 — Correct the consumer issues' reproducer expectations

aibap.mcp's bump-PR reproducer-verify step runs each linked issue's reproducer verbatim
(`aibap.mcp/CLAUDE.md`, "Cross-Repo Issue Tracking", point 4). Two of aibap.mcp#493's stated
"fixed" expectations cannot be met by any change, so left as they are they will read as
*still failing* and a correct `Closes #493` line will be pruned.

Post one comment on aibap.mcp#493 that:

- Restates finding 1's fixed expectation as "only that transport's objects, and the two lists
  differ; `position` remains empty on ECC because the server sends no `position` attribute at
  all — a server limitation, not a parse gap."
- Adds the new limitation from Task 2: on ECC only modifiable requests are in the worklist, so
  `get_transport_objects` for a released transport now returns a diagnosable error where it
  previously returned a wrong list.
- Reclassifies finding 3 as not fixable client-side, with the Task 6 typed error as the new
  fixed expectation.
- Notes that finding 2 (`position` staying `mcp.Required()`) is consumer-side work this plan
  does not cover, so #493 is unblocked but not closed by it.

Per `aibap.mcp/CLAUDE.md` ("Issue & PR Comments"), hand the draft to an independent reviewer
before posting.

**Acceptance:** the comment is posted and its URL is in the task report.
