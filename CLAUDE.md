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

### ETag resolution

`LockMap.ResolveETag` tries `GetSource` first (hardcodes `/source/main`). If that fails (CLAS 400, DTEL 404), it falls back to `FetchETag` which GETs the bare object URI with `acceptHeaderForURI`. The fallback is discovered via interface assertion — no breaking API changes.

### Stateful sessions

`X-sap-adt-sessiontype: stateful` pins requests to the same SAP work process. Used on `LockObject`, `SetSource`, `UnlockObject` to keep the lock handle valid across calls.

## Coding Pitfalls

- **Never use Go backtick (raw) string literals for ABAP source code** in test fixtures. Backtick strings preserve tab indentation from the Go source file.
- **goconst**: hoist repeated test strings (endpoint paths, object types) into `const` blocks in `client_test.go`.
- **Copyright**: when researching other ADT implementations, never copy code directly. Rewrite based on the observed API pattern. adtler is MIT-licensed.

## Configuration

Credentials live in `~/.config/sap-mcp/systems.json` (never commit). Config format: [sap-mcp-config](https://github.com/Hochfrequenz/sap-mcp-config). Override path via `SAP_CONFIG_FILE` env var.
