# Issue 303: NewClientsFromConfig returns the clients it could build

Goal: when one OAuth2 system has no stored token or no client ID, `NewClientsFromConfig`
returns the clients of the other systems together with an `errors.Join` of the per-system
failures, instead of `nil, err`. Callers that return on `err != nil` still fail fast.

Files: `adt/builder.go`, `adt/builder_test.go`, `README.md`.

## Task 1: partial result with joined errors (test first)

Existing test that covers the refactoring: `TestNewClientsFromConfigOAuth2MissingClientID`,
`TestNewClientsFromConfigOAuth2FallbackClientID`, `TestNewClientsFromConfigBasicAuth`.

Failing-test step (write first, run, see it fail because the current code returns a nil map):

1. In `adt/builder_test.go` extend `TestNewClientsFromConfigOAuth2MissingToken`: after the
   existing error check, assert `clients` is not nil and empty (single system, no client
   built). Change `_, err :=` to `clients, err :=`.
2. Add `TestNewClientsFromConfigPartialResult`: token path points at a nonexistent file;
   config has `basic` (User and Password set) and `oauth-nt` (no user, no token). Assert
   err is non-nil and mentions `oauth-nt`; `clients` has exactly the key `basic`.
3. Add `TestNewClientsFromConfigJoinsAllFailures`: token store has a token only for
   `oauth-ok`; config has `oauth-ok` (no client ID, `defaultOAuth2ClientID` ""),
   `oauth-notoken-a`, `oauth-notoken-b`, and `basic`. Assert err mentions both
   `oauth-notoken-a` and `oauth-notoken-b` and `oauth-ok` (client ID error), and `clients`
   has only `basic`. Also assert `errors.Join`-ness: `err.(interface{ Unwrap() []error })`
   yields 3 errors.
4. Run `go test ./adt/ -run TestNewClientsFromConfig`: the new tests fail (nil map /
   only the first error).

Implementation in `adt/builder.go`: replace the two `return nil, fmt.Errorf(...)` by
collecting into `var errs []error`, `continue`. Iterate over `slices.Sorted(maps.Keys(cfg.Systems))`
so the joined message is deterministic. End with `return clients, errors.Join(errs...)`
(`errors.Join` of no errors is nil). Update the doc comment: partial result plus joined
error, callers that want to continue can use the returned map; callers returning on
`err != nil` fail fast.

Plan review additions: the missing-token error keeps its text and gains the cause
(`...no stored token found: %w`, err of `TokenForSystem`). Imports `errors`, `maps`,
`slices` are added. Test 3 also asserts `clients` has no `oauth-ok` key. The new tests fail
before the change on the nil map / missing-key assertion (the error text already names the
systems). The doc comment states: on total failure the map is empty and non-nil; the
caller who continues with a partial map must make sure the default system is in it,
because `NewClientRegistry` fails when the default system is missing or the map is empty.

No integration test (`eachSystem`) and no `needs:integration-test` label apply: the function
builds clients and never contacts SAP. The PR links the aibap.mcp consumer issue only as
`Related: Hochfrequenz/aibap.mcp#636`. Extra checks by the orchestrator:
`go build -tags integration ./adt/...`, `go vet -tags integration ./adt/...`.

Run: `go test ./adt/ -run TestNewClientsFromConfig` (passes).

## Task 2: documentation (comments and prose only, no failing test: documentation task)

`README.md` line 54 paragraph: add one sentence saying that `NewClientsFromConfig` returns
the clients it could build together with the joined errors of the systems it could not
(missing OAuth2 token or client ID), so a caller may continue with the working systems.
No test pins the README wording for this sentence (checked with grep for
`NewClientsFromConfig` in tests: only builder_test.go).

## Verification

Full gate by the orchestrator: fmt, lint, datacheck, vet, test.
