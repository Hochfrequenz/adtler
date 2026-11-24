# Issue 162: DebugSession.Close

Goal: give `DebugSession` a way to end its own isolated SAP session, so a caller that detects a wedged debug session can tear it down. The parent client's `Logout` does not reach the debug session's separate cookie jar (issue #153).

## Design

`func (d *DebugSession) Close(ctx context.Context) error`:

1. `err := d.client.Logout(ctx)`: sends the logoff on the debug session's own cookies, returns a 401 as `*ADTError`.
2. Then, always, drop the local state too: under `d.client.mu`, set `csrfToken = ""` and `d.client.jar.Reset()`.

Step 2 is needed because `httpClient.Logout` returns early on a transport error (timeout, dropped connection) before it resets the jar and CSRF token. That is the likely outcome for a wedged session, whose cookies pin the logoff to the stuck work process. Resetting in `Close` and not in `Logout` leaves the behaviour of every other `Logout` caller unchanged. The reset is idempotent next to the one in `Logout`.

Properties to document in the doc comment of `Close`:

- It ends this DebugSession's SAP session only, not the parent's, and not the single-use sessions of `DebugSession.RunUnitTests` triggers (they are never logged off).
- The logoff goes out with the caller's `ctx`; a wedged session can make it wait until `ctx` ends or the 30-second short-client timeout. Local state is dropped either way.
- It does not cancel calls still in flight: callers cancel their own contexts first, otherwise a late response of a stuck call can store a stale cookie in the fresh jar.
- The DebugSession stays usable afterwards: the next call opens a new SAP session (new CSRF preflight). No claim is made about which SAP-side state the logoff releases beyond "the session ends".
- A 401 of the logoff is returned as `*ADTError`.

Concurrency: `Close` takes `c.mu`, the same mutex `Logout` and `ensureCSRF` use, so it waits only for a CSRF preflight in progress. The `DebugSession` fields stay write-once.

## Task 1: Close with unit tests (test first)

Files: `adt/debugger.go`, `adt/debugger_close_test.go` (new, package `adt_test`, httptest like `debugger_test.go`; constants `csrfEndpoint`, `logoffPath` exist).

Test first: write the tests, run them, show the failure (they do not compile before `Close` exists: that is the expected reason), then implement.

1. `TestDebugSessionClose_ResetsSession`: the server counts hits on `csrfEndpoint` and on `logoffPath`. The debug session makes one call (for example `StopListener`) so a CSRF token is cached; one CSRF hit. `Close` returns nil, exactly one logoff hit. A second call fetches the CSRF token again (two CSRF hits).
2. `TestDebugSessionClose_ReportsUnauthorized`: logoff answers 401; `errors.As` finds `*adt.ADTError`.
3. `TestDebugSessionClose_ResetsSessionWhenLogoffFails`: the logoff handler drops the connection (hijack and close) or the test passes an already-expired context. `Close` returns an error, and the next call still fetches a new CSRF token. This is the guard for the early return of `Logout`.
4. `TestDebugSessionClose_LeavesParentSessionAlone`: the server sets a cookie on every response; the parent makes a request, then `dbg.Close`, then the parent makes a request again and the server sees the cookie on it.

Mutation check (mandatory): after the tests pass, temporarily remove step 2 (the reset in `Close`) and show that test 3 fails; temporarily make `Close` return nil without calling `Logout` and show that test 1 fails. Restore the code. Report both failures.

Run: `--gate test ./adt -run TestDebugSessionClose`.

## Task 2: Integration test (compile only; never run here)

File `adt/debugger_close_integration_test.go` with `//go:build integration`. `TestDebugSessionClose_MultiSystem_Integration` using `eachSystem(t)`: for each system build `adt.NewDebugSession(sys.Client, sys.Config.User)`, `t.Cleanup` a `Close` too, call `StopListener` (starts the SAP session), `Close` must return nil, then `StopListener` again must succeed on the new session. No fixtures touched, no locks. Check it with `go vet -tags='integration transport' ./...` (gate `vet`). The test is not run (skip rule: no live SAP here); the PR notes that and keeps the `needs:integration-test` need for the merge session.

## Task 3: Docs (prose only; no test first: comments and documentation no compiler or test executes or parses; no test pins this wording, checked by search)

- `adt/client.go` freshSession comment: replace "DebugSession has no Logout or Close of its own, so its SAP session ends only when SAP times it out (issue #162)." with a sentence that `DebugSession.Close` ends it (otherwise SAP times it out).
- `adt/debugger.go`: struct doc ("Every field is set once...") names that `Close` changes only the jar and CSRF state of the client, not the fields; `NewDebugSession` doc points at `Close`.
- `CLAUDE.md` "Stateful sessions": the sentence that `DebugSession` has no `Logout` or `Close` of its own, changed to name `Close` and the early-return caveat.
- README: `DebugSession` is not listed anywhere there; no change.

## Final

Full gate (fmt, lint, datacheck, vet, test), then the whole-branch review.
