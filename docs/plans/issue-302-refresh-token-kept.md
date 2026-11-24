# Issue 302: keep the refresh token when a refresh response has none

Goal: `auth.RefreshToken` returns the given refresh token when the token endpoint answers without a new one (RFC 6749 section 6), so `NewClientsFromConfig` never stores or reuses an empty refresh token.

## Task 1: fix in `RefreshToken` (single task, single file pair)

1. Failing test first: `TestRefreshTokenWithoutNewRefreshToken` in `auth/oauth2_test.go`. An `httptest` endpoint answers with only `access_token` and `expires_in`; assert the returned `TokenData.RefreshToken` equals the original. Run it and see it fail with `got ""`.
2. Fix in `auth/oauth2.go`: after `postToken`, set `RefreshToken` to the input when the response had none. `ExchangeCode` is untouched (an empty refresh token there is a real "none issued").
3. Run `go test ./auth ./adt`, then the full gate.

Callers: `adt/builder.go` refresh callback (saves and reuses the result; fixed by the change, no edit needed).
