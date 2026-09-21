# adtler

Go client library for SAP ABAP Development Tools (ADT). Provides a typed Go interface to the SAP ADT REST API. Consumed by [mcp-server-abap](https://github.com/Hochfrequenz/mcp-server-abap).

## Build & Test

- **Build**: `go build ./...`
- **Lint**: `golangci-lint run --enable dupl,goconst,gocyclo ./...`
- **Unit tests**: `go test ./...` — must pass before committing.
- **Integration tests**: `go test -tags=integration ./adt/...` — requires SAP credentials, see README.md "Testing" section.

## Workflow

One PR per issue. Feature branches from `main` (`fix/`, `feat/`, `test/`, `refactor/`). Never commit directly to `main`.

### The established fix/review/test/merge cycle

Every fix or feature follows this cycle.

#### 1. Build the fix on a feature branch

- Branch from `main`: `git checkout -b fix/<N>-short-description`
- Implement the fix with unit tests (httptest mocks for ADT endpoints)
- Add a multi-system integration test using `eachSystem(t)` — the test must exercise the bug's exact failure path against both R/3 and S/4
- Run `go test ./...`, `go build -tags integration ./adt/...`, `go vet -tags integration ./adt/...`
- Commit, push, open PR linking the adtler issue AND the mcp-server-abap consumer issue, add the `needs:integration-test` label

#### 2. Independent reviewer agent confirms the code

Spawn a fresh Claude agent with no prior conversation context. Point it at the PR diff, the adtler issue, and the consumer-side mcp-server-abap issue. The agent must:

- Read the PR via `gh pr view` and `gh pr diff`
- Read both issues for context
- Inspect the changed files in a dedicated git worktree (to avoid race conditions with parallel reviewers)
- Run `go test ./...` and `go build -tags integration ./adt/...`
- Evaluate correctness, test coverage, edge cases, idiomatic Go, and SAP/ADT semantics
- Post a structured review comment on the PR: **GO** / **GO with notes** / **NO-GO**

The reviewer has no memory of the development conversation and reviews purely on the merits.

#### 3. CI must be green

All GitHub Actions checks must pass before proceeding: lint (`golangci-lint` with `dupl`, `goconst`, `gocyclo`), unit tests (Go 1.25 + 1.26), coverage, CodeQL. Common lint issues: `goconst` (hoist repeated strings to constants), `gofmt` (godoc code blocks need tab indent after `//`), `staticcheck` (use tagged switch).

#### 4. Local agent with SAP access runs integration tests

A separate Claude instance with access to real SAP systems (R/3 and S/4 via `~/.config/sap-mcp/systems.json`) runs the integration test:

- Checks out the fix branch
- Runs: `SAP_INTEGRATION_SYSTEMS="<r3-key>,<s4-key>" go test -tags=integration -v -run <TestName> ./adt/...`
- For transport-relevant changes, also runs: `go test -tags='integration transport' ./adt/...` (these create and release real transports — protected by the separate `transport` build tag)
- Posts a structured result comment on the PR with:
  - Per-system PASS/FAIL status
  - Relevant test output (no credentials or hostnames)
  - Cleanup notes (leftover test objects in `$TMP`)

This agent does NOT push code — it runs tests and comments only.

#### 5. Merge

If the integration test passes and CI is green:

- Remove the `needs:integration-test` label
- The PR is ready to merge — the author decides when and how to merge

### Labels (workflow-relevant)

| Label | Meaning |
|---|---|
| `needs:integration-test` | PR awaits real-SAP integration test before merge |
| `blocked:eclipse-capture` | Issue needs Eclipse ADT HTTP traffic capture |
| `blocked:design-needed` | Issue needs architectural design discussion |
| `blocked:sap-investigation` | Issue needs SAP-side investigation |

For the full label list, run `gh label list --repo Hochfrequenz/adtler`.

## Public Repository — No Internal Data

This repository and its issue tracker are **public**. Nothing that identifies our internal
environment may land there — that covers commits, source, comments, docs, test fixtures, issue
titles and bodies, issue comments, PR descriptions, and anything a workflow writes on our behalf.

Never publish:

- Host names, FQDNs, IP addresses, or ports of internal systems — SAP or otherwise, including
  auth and identity infrastructure. This applies to source comments recording what an XML shape
  was verified against: record the date and the system type, not the address
- The internal system aliases defined in `systems.json`, including per-client and proxy
  variants, when used to name a system in prose
- Anything that embeds a system ID: transport and task numbers (`<SID>K9…`), lock keys, or log
  lines carrying a host. Write `<request>` / `<task>` instead — an outside reader cannot run a
  reproducer against our request numbers anyway
- Object names in our own or a partner's registered SAP namespace (`/XXX/…`). The namespace
  identifies its owner, the object name usually identifies the business domain, and a partner
  namespace additionally discloses which add-ons we run. Use a neutral placeholder such as
  `/ABC/CL_EXAMPLE`
- An inventory of our landscape: client numbers, which industry or partner add-ons are
  installed, or references to internal wikis and ticket systems. Saying that a finding was
  reproduced "on both systems" is fine; enumerating the landscape is not
- Credentials or tokens of any kind, and client numbers tied to a named system
- Local filesystem paths containing a user name, and SAP logon IDs
- Customer, project, or other company-internal identifiers

Name a SAP system by **type and release level** instead, which is also more useful to an outside
reader than an alias:

- `SAP S/4HANA 2025, on-premise (SAP_BASIS 816, S4CORE 109)`
- `SAP ERP 6.0 EHP8 (SAP_BASIS 750, SAP_APPL 618)`

Read the levels from the system rather than guessing: component levels from `CVERS`
(`SELECT COMPONENT, RELEASE, EXTRELEASE FROM CVERS WHERE COMPONENT IN ('SAP_BASIS', 'SAP_APPL',
'S4CORE')` — note that `LIKE 'SAP%'` silently misses `S4CORE`), and the marketing release from
`PRDVERS` (`SELECT NAME, VERSION, INSTSTATUS, DESCRIPT FROM PRDVERS`, where `INSTSTATUS = '+'`
marks the active version). Publish the release level only — never the support-package level (the
`EXTRELEASE` column, e.g. `SP 0034`), which maps directly onto published SAP Security Notes and
so states which fixes are not yet applied. Where several systems appear in one document,
introduce the type/release form once and refer back to it ("the ECC system", "on both systems").

### Integration tests discover their fixtures, they do not hardcode them

An integration test needs objects that exist on the target system, which is exactly how real
object names end up in this repository. Replacing such a name with a placeholder would leave the
test green and testing nothing, so do neither: have the test **query the system for a suitable
object at runtime** and `t.Skip` when it finds none. Log counts and types, never object names, so
CI logs stay clean too.

Where a test genuinely cannot discover its fixture, take the name from an environment variable
and skip when it is unset. Do not commit the value.

### A reuse fallback must not swallow a request that never worked

An integration test that falls back on an existing object when creation fails — *"create it, and
if that errors, carry on with the one already there"* — passes forever once the object exists.
Where the object cannot be deleted again it always exists after the first run, so the fallback
becomes permanent. That is how #149 shipped: `CreatePackage` could not work on S/4 at all, and
`TestCreatePackage_Integration` reported success anyway, because packages cannot be removed over
ADT (#150) and so the fixture from an earlier run was always there to fall back on.

A fallback is legitimate for failures that say *this object is already in the way* — a duplicate,
a lock, a missing authorization. It is never legitimate for a failure that says *SAP would not
process this request*: a 400 the server refused to parse, an unacceptable media type, a missing
header. Rule out that class explicitly, with `errors.As` on `*adt.ADTError` and its `Type`, before
taking the fallback — the exception ID survives message translation, the text does not.

Where an operation cannot be undone through ADT at all, say so in README.md under "What ADT
prevents a test from covering" and in the pull request, rather than leaving the next reader to
infer it from a test that creates nothing.

### Exceptions

The one allowed exception is a literal config value a reader has to type or the code has to hold:
the `SAP_INTEGRATION_SYSTEMS` default set where it is documented or defined, and integration-test
branches that key off one system's real behaviour. The prose, comments and commit messages
*around* that value are not covered — write "the ECC system", not the alias.

Unit-test fixture strings are not covered either; use `sysA` / `sysB` for system keys and generic
placeholders for object names. This section is bound by the same rule: where an example is needed,
write `<alias>`.

`.mcp.json` is **not** an exception. It is git-ignored and may hold credentials; it must never be
committed at all.

### Before pushing

Grep the diff for the shapes that matter — an internal domain suffix, a `<SID>K9…` transport
number, a `/XXX/` namespace prefix — rather than for the alias names, so the guard itself does not
leak them.

When you find internal data already published, redact it in place (edit the issue body, or open a
PR) rather than only noting it. For a host name, credential or logon ID, assume the value is
already disclosed regardless: editing a file does not remove it from the commit history or from
the notification e-mails that already went out, so rotate or renumber it instead of trusting the
edit.

## Project Structure

- `adt/` — HTTP client for the SAP ADT REST API (source, transports, locks, activation, syntax check, ATC, unit tests, ...)
- `adt/adtxml/` — XML marshalling types
- `adt/custexport/` — SAP customizing-table export (SQLite/JSON)
- `auth/` — OAuth2 PKCE flow and on-disk token storage

## Key Patterns

### SAP system differences (R/3 vs S/4)

R/3 (ECC) and S/4HANA often behave differently for the same ADT endpoint. Always test against both. Known differences:

- **Lock handle delivery**: R/3 reads `X-SAP-Lock-Handle` header; S/4 reads `?lockHandle=` query param. `SetSource` retries with query param on 423.
- **Accept headers**: S/4 is stricter — requires vendor MIME types (e.g. `application/vnd.sap.adt.mc.messageclass+xml`). R/3 often accepts `application/xml`.
- **ESRDIRE enqueue after CreateObject**: S/4 leaves a session-bound enqueue. Workaround: `Logout()` after `CreateObject`.
- **ETag charset**: SAP embeds the source Content-Type into the ETag, so `GetSource` and the validating PUT must agree on the Accept / Content-Type form. `sourceContentType` (discovery-driven, from #35) prefers `text/plain; charset=utf-8` when discovery advertises it; both sides therefore land on the same ETag form. The earlier 412 retry workaround was removed in #42 once the discovery path covered every supported system.
- **DDIC endpoints**: DTEL/DOMA/TABL creation via `/sap/bc/adt/ddic/` requires S/4. R/3 returns 404 or 415.
- **Runtime-load generation vs. session reuse (S/4)**: on S/4, an ADT session that just ran the create → set source → activate lifecycle **cannot generate a class's runtime load** when it then executes the class in that *same* session — classrun's `CREATE OBJECT` soft-fails as `Error: Class does not implement if_oo_adt_classrun~main method!` (issue #106 defect 1), and a changed + re-activated class serves the *stale* previously-generated load (defect 2). A **fresh** session generates the load from the current active source. `RunClass` works around this by running the classrun POST on an isolated single-use session (`freshSession` — own cookie jar + CSRF preflight), never the caller's worn session. R/3 (ECC) regenerates a persistent load on activation, so it is unaffected. **Generalises:** any operation that depends on SAP generating fresh state (a runtime load, etc.) right after a mutating lifecycle may hit this — reach for a fresh session rather than reusing the lifecycle session. Fixed in #106 / v0.3.13.

### ETag resolution

`LockMap.ResolveETag` tries `GetSource` first (hardcodes `/source/main`). If that fails (CLAS 400, DTEL 404), it falls back to `FetchETag` which GETs the bare object URI with `acceptHeaderForURI`. The fallback is discovered via interface assertion — no breaking API changes.

### Stateful sessions

`X-sap-adt-sessiontype: stateful` pins requests to the same SAP work process. Used on `LockObject`, `SetSource`, `UnlockObject` to keep the lock handle valid across calls.

The inverse also matters: some operations need a **fresh** session, not a reused one. `RunClass` runs on a single-use isolated session (`freshSession`) because a session that performed the create/set source/activate lifecycle cannot generate a class's runtime load on S/4 (see "Runtime-load generation vs. session reuse" under SAP system differences). If an operation depends on state SAP only generates in a clean session, give it a fresh session instead of reusing the caller's.

### Long-running ABAP execution (two HTTP clients)

The client holds two `*http.Client`s: `http` with a 30-second timeout for ordinary ADT calls, and `httpLong` with no timeout of its own, where the deadline comes from the context (`doReadLong`, `doMutateLong`).

Any endpoint that executes **open-ended ABAP** — currently `RunQuery` (data preview) and `RunClass` (classrun) — must use both halves of the long path:

1. `doMutateLong` / `doReadLong`, because 30 seconds is arbitrary for user-authored ABAP and consumers cannot raise it (`http.Client.Timeout` and the context deadline combine as `min(...)`, and the client fields are unexported), and
2. `withDefaultDeadline(ctx)`, because the long client imposes no limit at all — without a default deadline a runaway statement hangs the caller forever.

Doing only (1) trades a wrong limit for no limit. The shared default lives in `defaultLongRunTimeout` (5 minutes, just past the usual SAP dialog work-process limit so SAP aborts the step and returns a diagnosable error first); both endpoints reference it so they cannot drift apart. Fixed for `RunClass` in #114.

## Coding Pitfalls

- **Never use Go backtick (raw) string literals for ABAP source code** in test fixtures. Backtick strings preserve tab indentation from the Go source file.
- **goconst**: hoist repeated test strings (endpoint paths, object types) into `const` blocks in `client_test.go`.
- **Copyright**: when researching other ADT implementations, never copy code directly. Rewrite based on the observed API pattern. adtler is MIT-licensed.

## Configuration

Credentials live in `~/.config/sap-mcp/systems.json` (never commit). Config format: [sap-mcp-config](https://github.com/Hochfrequenz/sap-mcp-config). Override path via `SAP_CONFIG_FILE` env var.
