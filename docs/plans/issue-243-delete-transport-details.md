# Issue 243: DeleteTransport returns what it deleted

Goal: `DeleteTransport(ctx, number) (*DeletedTransport, error)` reads the request before the `DELETE` and returns owner, description, type, state, last change, creation (optional) and tasks. A failed read never blocks the deletion, except a rejected credential.

## Design decisions

- Signature change (the issue's suggested shape; the repository already ships such changes as `feat!:`). Touches `TransportClient` (adt/client.go), `ClientRegistry.DeleteTransport` (adt/registry.go), `mockClient` in adt/custexport/export_test.go, the unit test `TestDeleteAndReleaseTransport` and the four integration tests that call it.
- Primary source is the ADT transport organizer read that `GetTransportInfo` already does (one GET). Its `tm:request` carries owner, desc, type (`tm:type`, K/W/T), status, `tm:lastchanged_timestamp` and `<tm:task>` children with owner, desc and status (fixtures in adt/transport_ecc_test.go, taken from both releases). No E070 query is needed.
- Creation (`E070CREATE`: `CRE_USER`, `CRE_DATE`, `CRE_TIME`) comes from one data preview query. Not every request has a row, so `Created` is the zero time then, and `LastChanged` is the documented fallback.
- Failures before the DELETE: collected into `DeletedTransport.DetailsErr` (`errors.Join`), the deletion proceeds. Exceptions: `credentialRejected(err)` (a 401 would only be sent again, #222) and a cancelled context return the error without deleting.
- The number is validated with `transportNumberRe` before it enters the SQL literal and upper-cased; an unsafe number skips the query and is reported in `DetailsErr`.
- `LastChanged` and `Created` are wall-clock values of the SAP system without a zone, carried as `time.Time` in UTC; documented.
- Not done: task descriptions beyond what ADT gives, system name (the caller knows it), E070 query (ADT already gives the same fields).

## Task 1: parser refactor and new fields (covered by existing GetTransportInfo tests; no new failing test needed first)

Refactor, behaviour unchanged. Existing tests covering it: every test that calls `GetTransportInfo` / `parseTransportInfo` (`go test ./adt -run 'TransportInfo'`).

In adt/transport.go:

1. Add to `xmlRequest`: `Type string xml:"type,attr"`, `Timestamp string xml:"lastchanged_timestamp,attr"`. Add to `xmlTask`: `Owner xml:"owner,attr"`, `Description xml:"desc,attr"`, `Status xml:"status,attr"`.
2. Split `parseTransportInfo` into `findTransportRequest(data []byte, transportNumber string) (xmlRequest, error)` (the Format 1 / Format 2 logic returning the first matching `xmlRequest`, errors exactly as now) and `parseTransportInfo`, which calls it and converts to `TransportRequest`.

Run: `bash <helper> run --gate test ./adt -run 'Transport'` must stay green.

## Task 2: DeleteTransport returns DeletedTransport (test first)

Write the tests in a new file adt/transport_delete_test.go (package `adt_test`), see them fail (they do not compile before the signature change; stub nothing, the compile failure is the expected failure, then make them compile and watch the assertions fail before implementing the reads), then implement.

Tests, all against an httptest server (csrfEndpoint, `GET /sap/bc/adt/cts/transportrequests/<n>`, `POST /sap/bc/adt/datapreview/freestyle`, `DELETE /sap/bc/adt/cts/transportrequests/<n>`), recording the request order. Use `dataPreviewXML` from transport_e071_fallback_test.go and placeholders `AAAK900123`, `USERA`.

1. Full details: GET returns a request XML (owner USERA, desc, type K, status D, `lastchanged_timestamp="20261105123456"`, two tasks with owner and status) and E070CREATE returns `USERA`, `20261104`, `101112`. Assert every field of the result, the SQL contains `FROM E070CREATE` and `TRKORR = 'AAAK900123'`, the DELETE comes last, `DetailsErr == nil`.
2. No E070CREATE row: `Created` zero, `CreatedBy` empty, `LastChanged` set, `DetailsErr == nil`.
3. ADT read answers 500: DELETE still sent, no error returned, `DetailsErr != nil`, `Number` set, `Created` still read.
4. Data preview answers 403: DELETE still sent, ADT fields present, `DetailsErr != nil`.
5. Read answers 401: no DELETE (and no data preview) sent, the error is returned, result nil (`errors.As` to `*adt.ADTError` with `StatusCode` 401).
6. DELETE answers 500: result nil, error returned.
7. Unsafe number (contains a quote): no SQL sent, `DetailsErr != nil`.
8. A lower-case number is upper-cased in the SQL.

Implementation in adt/transport.go, replacing `DeleteTransport`:

```go
// DeletedTransport is what DeleteTransport read about a request before it deleted it.
type DeletedTransport struct {
	Number      string
	Owner       string
	Description string
	// Type is the request category (E070-TRFUNCTION): K, W or T.
	Type      TransportType
	State     TransportState
	RawStatus string
	// LastChanged is the last change of the request (ADT lastchanged_timestamp); zero if unknown.
	LastChanged time.Time
	// Created and CreatedBy come from E070CREATE. SAP does not write a row for
	// every request (one created through CreateTransport had none), so Created
	// is the zero time then; use LastChanged as the documented fallback.
	Created   time.Time
	CreatedBy string
	// Tasks are the tasks deleted with the request (Number, Owner, Description, State, RawStatus).
	Tasks []TransportRequest
	// DetailsErr is non-nil when a read before the deletion failed; the fields it covers are empty.
	DetailsErr error
}
```

Both time fields hold the SAP system's wall-clock time, which carries no zone; they are placed in UTC only to fit a `time.Time`.

```go
func (c *httpClient) DeleteTransport(ctx context.Context, transportNumber string) (*DeletedTransport, error) {
	deleted := &DeletedTransport{Number: transportNumber}
	var errs []error
	if data, err := c.readTransportXML(ctx, transportNumber, "application/vnd.sap.adt.transportorganizer.v1+xml"); err != nil {
		errs = append(errs, fmt.Errorf("reading the request: %w", err))
	} else if req, err := findTransportRequest(data, transportNumber); err != nil {
		errs = append(errs, fmt.Errorf("reading the request: %w", err))
	} else {
		fillDeletedTransport(deleted, req)
	}
	if err := c.readTransportCreation(ctx, deleted); err != nil {
		errs = append(errs, fmt.Errorf("reading E070CREATE: %w", err))
	}
	deleted.DetailsErr = errors.Join(errs...)
	// A rejected credential is final (#222) and a cancelled context ends the call: the DELETE would fail the same way.
	if credentialRejected(deleted.DetailsErr) { return nil, fmt.Errorf("DeleteTransport: %w", deleted.DetailsErr) }
	if err := ctx.Err(); err != nil { return nil, fmt.Errorf("DeleteTransport: %w", err) }
	... existing DELETE; on error or checkResponse error return nil, err; else return deleted, nil
}
```

`fillDeletedTransport` copies Owner, Description, Type (`TransportType(strings.ToUpper(req.Type))`), `TransportStateOf(req.Status)`, RawStatus, LastChanged (`parseSAPTimestamp("20060102150405")`) and the tasks. `readTransportCreation` validates the number with `transportNumberRe` (error wrapping `errTransportNumberUnsafe`), upper-cases it, runs `SELECT TRKORR, CRE_USER, CRE_DATE, CRE_TIME FROM E070CREATE WHERE TRKORR = '<n>'` through `RunQuery(ctx, q, 1)`, addresses columns by name (`queryColumnIndexes`, `requireQueryColumns`; skip `requireQueryColumns` when there are zero rows, as `transportQueryNumbers` does), and fills `Created` (`CRE_DATE` + `CRE_TIME`, text form `YYYYMMDD` `HHMMSS`, as the cursor code already relies on) and `CreatedBy`. An unparsable or `00000000` date leaves `Created` zero. Doc comment of `DeleteTransport` states the read-before-delete, that DeleteTransport also accepts a task number (then the ADT read may not find it and `DetailsErr` says so), and the zone caveat.

Update the signature in adt/client.go, adt/registry.go, adt/custexport/export_test.go, `TestDeleteAndReleaseTransport` (transport_test.go: wrap `DeleteTransport` as `_, err := ...`), and the four integration tests (adt/transport_copies_integration_test.go:171, adt/transport_create_integration_test.go:110, adt/transport_fullcycle_integration_test.go:118, adt/transport_task_integration_test.go:38 and 44; use `_, err :=` or `_, _ =`). Note in TestDeleteAndReleaseTransport the mock answers 200 with an empty body for the reads: that must give a result and no error.

Run through the helper: `--gate test ./adt -run 'DeleteTransport|DeleteAndRelease|TransportInfo'`, `--gate test ./adt/custexport`, and `--gate vet` (compiles the integration files).

## Plan review amendments (apply to Task 2)

- 401 order: check `credentialRejected` right after the ADT read and return `nil, err` before the E070CREATE query; check again after the query and before the DELETE. Extra tests: ADT read 401 sends no data preview and no DELETE; ADT read succeeds, data preview answers 401, no DELETE, error returned.
- `parseSAPTimestamp(date, time string) time.Time` does not exist: write it (8 digit date, 6 digit time or 14 digit combined; zero time for empty, `00000000`, wrong length, non-digits, invalid calendar values) with a table test in a new adt/sap_timestamp_test.go (package `adt`, internal test is fine for this helper only, or test through DeleteTransport if the repository has no internal tests).
- The two pre-reads run under `readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)` so a stuck data preview cannot hold back the deletion for the 5-minute default deadline of `RunQuery`; the cancelled-context check before the DELETE looks at the parent `ctx`. No test for the timeout itself (it would take 30 seconds); it is one line.
- `DeletedTransport` has json tags like `ReleaseResult`; `DetailsErr error` is `json:"-"` and `DetailsError string json:"details_error,omitempty"` is set from it, so a JSON consumer does not lose it.
- Extra test: the ADT read answers 200 with a body for a different request number (`absentTransportError`, the normal case of a released request on ECC): DELETE still sent, `DetailsErr != nil`, Created still read.
- Doc comment: `LastChanged` stays zero where the ADT answer carries no `lastchanged_timestamp` (the ECC worklist shape). Do not claim that a request created through CreateTransport had no E070CREATE row; say only that not every request has one.
- Add a multi-system integration test `TestDeleteTransport_Details_Integration` with `eachSystem(t)` in a new file (build tag `integration`, `transport` not needed for create/delete of a request, follow adt/transport_create_integration_test.go): create a workbench request, delete it, assert Owner, Description, Type K, State modifiable and one task; accept a zero `Created` (log that case, not the values). It cannot be run here (no SAP access); it must compile under `go vet -tags='integration transport' ./...`.
- The DELETE path stays unescaped as before (out of scope); the unsafe-number test only proves that no SQL is sent.

## Task 3: documentation (prose only, no test)

Exception of the test rule: prose documentation that no compiler or test runner executes. README.md: add one short paragraph (where the 401 handling is listed, README.md around line 91-101: add `DeleteTransport` (its pre-deletion reads) to the list of calls that stop on a rejected credential) and CLAUDE.md "Key Patterns" needs no change. No internal data.

## Verification

Full gate (fmt, lint, datacheck, vet, test) by the worker after the tasks.
