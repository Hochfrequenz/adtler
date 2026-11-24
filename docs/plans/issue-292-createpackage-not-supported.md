# Issue 292: CreatePackage classifies a missing package endpoint as ErrorNotSupported

Goal: when `POST /sap/bc/adt/packages` answers 404 (ECC, SAP_BASIS 750), `CreatePackage` returns an `*ADTError`
(`StatusCode` 404, new `Type` `ExceptionTypeCreatePackageUnsupported`) that `ClassifyError` maps to `ErrorNotSupported`.
The message keeps the current wording, so callers matching a substring keep working.

Chosen approach: the issue's primary proposal (keep the POST, convert the 404). The alternative (a discovery gate)
would send nothing, but a failed discovery read would then need its own handling; the 404 is the measured behaviour.

## Task 1 (single task)

Test first. It fails before the change because `ClassifyError` returns `ErrorUnknown` for the plain `fmt.Errorf`:

- [ ] In `adt/object_createpackage_accept_test.go` add `TestCreatePackage_MissingEndpointIsNotSupported`: an httptest
  server answers CSRF and logoff as the neighbouring tests do and 404 on `/sap/bc/adt/packages`. Assert that
  `errors.As` gives an `*adt.ADTError` with `StatusCode` 404 and `Type` `adt.ExceptionTypeCreatePackageUnsupported`,
  that `ClassifyError` is `ErrorNotSupported`, and that `err.Error()` still contains
  "package creation via ADT REST requires S/4HANA".
- [ ] Run `go test ./adt/ -run TestCreatePackage_MissingEndpoint`: expect a compile error (constant missing). Add the
  constant alone, run again, and see the `ClassifyError` / type assertion fail for the expected reason.

Implementation:

- [ ] `adt/types.go`: add `ExceptionTypeCreatePackageUnsupported = "ADT_PACKAGE_CREATE_UNSUPPORTED"` next to
  `ExceptionTypeCreateRequestUnsupported`. Documented as synthesised locally from the 404 (a request was sent).
- [ ] `adt/errorkind.go`: add it to the `ErrorNotSupported` case of `classifyByExceptionType`.
- [ ] `adt/object.go`: return `&ADTError{StatusCode: 404, Type: ..., Message: <current wording>}` instead of
  `fmt.Errorf`, and add a doc comment line to `CreatePackage`.
- [ ] Run the covering tests (`TestCreatePackage`, the errorkind tests), then the full gate.

No documentation file lists the exception types; `adt/README.md` mentions `CreatePackage` only in a table.
