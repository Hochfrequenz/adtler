# Issue 155: CreatePackage negotiates its media type from discovery

Goal: `CreatePackage` resolves the media type of `Content-Type` and `Accept` through
`NegotiateContentType`, preferring v2, then v1, defaulting to v2 when discovery has no
usable entry.

## Facts from the code

- `NegotiateContentType` (adt/discovery.go) reads `c.discovery`, which `fetchCSRFToken`
  fills. The preflight inside `doMutate` runs too late for a header computed before the
  call, so `CreatePackage` calls `ensureCSRF` first, as `RunATCCheck` and the source
  functions do.
- Only the live check of the claim "the collection advertises v2 and v1" is out of
  reach (no SAP system for this issue). The fallback is safe either way: no v1 entry
  advertised means v2, today's behaviour.

## Task 1: negotiate the media type (single task)

Test first (adt/object_createpackage_accept_test.go, package adt_test), seen failing
for the v1-only case before the change:

- discovery advertises only v1 for `/sap/bc/adt/packages`: v1 on both headers (fails today).
- discovery advertises v2 and v1: v2 on both headers (guards unchanged behaviour).
- discovery advertises v1 before v2: still v2 (preference, not document order).
- discovery has no entry for the collection, or no discovery body at all: v2.
- the existing `TestCreatePackage_SendsAcceptHeader` keeps passing.

Change (adt/object.go): replace the `pkgContentType` constant by `pkgContentTypeV2` and
`pkgContentTypeV1`; in `CreatePackage` call `c.ensureCSRF(ctx)` (error wrapped as
`CreatePackage: %w`), then `c.NegotiateContentType("/sap/bc/adt/packages", [v2, v1], v2)`
and send the result as both headers.

Docs: README.md and CLAUDE.md mention nothing of the package media type; check with grep
and update only if they do.

Verification: `go test ./adt/ -run CreatePackage`, then the full gate.
