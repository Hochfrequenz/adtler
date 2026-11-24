# Issue 241: create the fixture transport lazily

Goal: an integration run must not leave an empty modifiable transport request behind when all
fixtures already exist (`setupFixtures` creates it unconditionally; `TestMain` releases it only
when a fixture was created).

Approach:

- `lazyTransport` creates the request only when the first fixture has to be created in a
  non-local package (one attempt, the error is printed). It records whether an object was
  created in a non-local package (`holdsObjects`).
- `setupFixtures` returns the `*lazyTransport` instead of the number.
- One function `cleanupFixtures(ctx, client, created, tr, rec)` does the whole teardown (delete
  created fixtures in reverse, then the request) and is called on the normal path and on the
  setup-error path of `TestMain` (which exits with `os.Exit`, so no `defer`). The request is
  released when it holds objects (`holdsObjects`), deleted with `DeleteTransport` when it
  exists but holds none (fixture fell back to `$TMP`, or setup failed before anything went into
  it). After a 401 (`rec.rejected`) it touches the request not at all (CLAUDE.md, "A 401 is
  final under Basic auth").
- README.md "shared fixture setup ... creates and releases one" is reworded.

Files: `adt/fixtures_integration_test.go` (change), `adt/fixtures_cleanup_integration_test.go`
(new), `README.md` (one sentence). No production code change.

Known remaining case: a run killed by Ctrl-C or a panic inside `m.Run` still leaves the
request (the teardown does not run); not fixable from `TestMain`.

## Task 1: lazyTransport, setupFixtures, cleanupFixtures

Failing test first, `adt/fixtures_cleanup_integration_test.go` (tag `integration`), with a fake
`adt.Client` that embeds the interface (a call it does not override panics, so the test also
proves that nothing else is called) and counts calls:

- `TestSetupFixtures_AllFixturesExist_CreatesNoTransport`: `BrowsePackage` and `GetObjectInfo`
  succeed; `CreateTransport` is never called, `tr.number() == ""`. Against the current code this
  fails (the current code calls `CreateTransport` unconditionally; the test fails to compile first
  because of the new signature, and the behaviour is seen failing after a stub signature).
- `TestSetupFixtures_MissingFixture_HoldsObjectsOnlyInNonLocalPackage`: one fixture is missing;
  with `CreateObject` succeeding for `Z_ADT_MCP_TEST`, `holdsObjects` is true; with
  `CreateObject` failing for it and succeeding for `$TMP`, the request exists and
  `holdsObjects` is false.
- `TestCleanupFixtures`: table: no request, nothing happens; request without objects is
  deleted, not released; request with objects is released, not deleted; after a rejected
  credential neither happens.

Run offline: `SAP_INTEGRATION_SYSTEMS=none` and no `SAP_INTEGRATION_HOST`, so that `TestMain`
contacts no system (`go test -tags=integration -run 'TestSetupFixtures|TestCleanupFixtures' ./adt/`).
No other integration test is run.

Implementation: see the branch. Then `go vet -tags="integration transport" ./...`, the gate.
Commit.
