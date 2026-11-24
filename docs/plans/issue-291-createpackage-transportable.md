# Issue 291: CreatePackage for transportable packages

Goal: `CreatePackage` can create transportable packages: it sends `pak:recordChanges="true"` and lets the caller choose the package type and super package.

Design: no breaking change. `CreatePackage` gets trailing variadic `PackageOption`s (`WithPackageType`, `WithSuperPackage`), so existing callers compile unchanged. Change recording is derived from the name (not `$`-prefixed), not a parameter: SAP requires it for every non-local package.

## Task 1: body carries recordChanges, packageType, superPackage

1. Test first (`adt/object_createpackage_options_test.go`, fails: undefined options): transportable name has `pak:recordChanges="true"`; `$` name has none; default type is `development` with no super package; structure type and super package are sent (super package upper-cased); an unknown type is refused before any request.
2. Implement in `adt/object.go` (options, validation, recordChanges), `adt/adtxml/object.go` (`RecordChanges` attribute), `adt/client.go`, `adt/registry.go` (variadic signature) and the `custexport` test mock.
3. Run `go test ./adt/... -run TestCreatePackage`.

## Task 2: live test and docs

Prose and an integration test that is only compiled (`go vet -tags 'integration transport'`), never run here: `adt/object_createpackage_transportable_integration_test.go`, section "Transportable packages" in `adt/README.md`. The integration test is a test that was not seen failing: it needs a real SAP system (skip rule), the PR names it.

## Out of scope

SAP's error message for a new top-level non-structure package is not measured, so no client-side mapping is added (a guess would be wrong or misleading). aibap.mcp needs matching `create_package` parameters (separate repository).
