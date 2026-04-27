# Extract namespace + type from `<exc:exception>` envelope (Issue #43)

**Status:** Design
**Date:** 2026-04-27
**Branch:** `feat/43-adt-error-namespace-type`
**Related issues:** Hochfrequenz/adtler#43, Hochfrequenz/mcp-server-abap#310

## Problem

When SAP returns an ADT exception, the envelope carries a stable, language-independent identifier — `<namespace id="…"/>` plus `<type id="…"/>` — that today is parsed away. `parseADTError` (`adt/client.go:515`) only extracts the human-readable `<message>` and discards the rest.

This identifier is the SAP-ADT equivalent of an ABAP MSGID/MSGNO: stable across SAP releases and locales, ideal for downstream correlation, filtering, and conditional retry logic. Without it, internal helpers like `isInvalidLockHandle` and `isPreconditionFailed` (`adt/types.go:144,156`) fall back to checking only the HTTP status code, and consumer code (e.g. mcp-server-abap canonical event logging) has to substring-match localised message text.

The real envelope shape (verified in `adt/object_test.go:280`) is:

```xml
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceWrongData"/>
  <message lang="EN">Resource ZCL_TEST: wrong input data for processing</message>
</exc:exception>
```

The current parser is keyed on the older root element `<ExceptionText>`. Against the newer `<exc:exception>` shape, `xml.Unmarshal` fails and the body falls through to the plain-text layer.

## Goals

1. `ADTError` exposes `Namespace` and `Type` as first-class fields, populated from `<exc:exception>` when present.
2. `parseADTError` handles both `<exc:exception>` (new) and `<ExceptionText>` (legacy) without behavioural regression on the legacy form.
3. `Error()` includes the `Type` ID in parentheses when present; format unchanged when absent.
4. Internal retry predicates (`isInvalidLockHandle`, `isPreconditionFailed`) prefer the structured `Type` field when available, with a status-code fallback so legacy responses still work.
5. Exported string constants for the type IDs the library reacts to internally, so consumer code can compare structurally without fearing typos.

## Non-goals

- Classical ABAP MSGID/MSGNO from `<exc:properties>`. No real fixture observed; deserves its own issue with captured payload.
- A typed exception hierarchy (`ResourceLockedError`, `WrongDataError`, …). One `ADTError` carrying the ID as data is sufficient; callers can switch on `adtErr.Type`.
- Catalogue every conceivable SAP exception type. We export constants only for the IDs we use internally; consumers compare on plain strings for everything else.

## Architecture

### `ADTError` (`adt/types.go`)

```go
type ADTError struct {
    StatusCode int
    Namespace  string  // e.g. "com.sap.adt" — empty if unknown
    Type       string  // e.g. "ExceptionResourceLocked" — empty if unknown
    Message    string
}

func (e *ADTError) Error() string {
    if e.Type != "" {
        return fmt.Sprintf("SAP ADT error %d (%s): %s", e.StatusCode, e.Type, e.Message)
    }
    return fmt.Sprintf("SAP ADT error %d: %s", e.StatusCode, e.Message)
}
```

### Exception type constants (`adt/types.go`)

```go
const (
    ExceptionTypeResourceInvalidLockHandle = "ExceptionResourceInvalidLockHandle"
    ExceptionTypeResourceLocked            = "ExceptionResourceLocked"
    ExceptionTypePreconditionFailed        = "ExceptionPreconditionFailed"
    ExceptionTypeResourceWrongData         = "ExceptionResourceWrongData"
)
```

Scope: only the IDs adtler itself reacts to or asserts against in tests. New constants are added on demand.

### Parser layers (`adt/client.go:parseADTError`)

Today: 3 layers — `<ExceptionText>` XML, HTML page, plain text.
After: 4 layers, in order:

1. **NEW.** `<exc:exception>` — populates `Namespace`, `Type`, `Message`.
2. `<ExceptionText>` — populates `Message` only (existing behaviour).
3. SAP HTML "Application Server Error" page (existing).
4. Plain text fallback (existing).

`xml.Unmarshal` matches root element name, so layers 1 and 2 don't collide. Each layer attempts `xml.Unmarshal` against a struct keyed to that root; on failure (or empty `<message>`), the next layer runs. The HTML and plain-text layers stay byte-identical.

### Retry predicates (`adt/types.go`)

```go
func isInvalidLockHandle(err error) bool {
    var adtErr *ADTError
    if !errors.As(err, &adtErr) {
        return false
    }
    if adtErr.Type != "" {
        return adtErr.Type == ExceptionTypeResourceInvalidLockHandle
    }
    return adtErr.StatusCode == 423
}

func isPreconditionFailed(err error) bool {
    var adtErr *ADTError
    if !errors.As(err, &adtErr) {
        return false
    }
    if adtErr.Type != "" {
        return adtErr.Type == ExceptionTypePreconditionFailed
    }
    return adtErr.StatusCode == 412
}
```

Behaviour change: when `Type` is present and is *not* the matching ID, the predicate now returns false even though the status code matches. This narrows a previously over-broad check — e.g. a 423 `ExceptionResourceLocked` (object held by another user) no longer triggers the lock-handle-delivery retry, which is correctness-positive: that retry only makes sense when SAP says the lock handle itself is invalid. Legacy `<ExceptionText>` responses (no `Type`) keep the old status-code-only behaviour.

### Cross-reference: deviation from issue scope

The issue explicitly lists rewriting these predicates as "out of scope, follow-up". The user (project owner) opted to bundle them with this PR (option C in brainstorming) since the predicates are the only internal consumers of the new `Type` field — splitting them across two PRs would mean a no-op intermediate state. The behaviour-change note above is the cost of that choice.

## Data flow

```
SAP response body
       │
       ▼
parseADTError(statusCode, body)
       │
       ├── try <exc:exception>     → ADTError{Status, Namespace, Type, Message}
       ├── try <ExceptionText>     → ADTError{Status, "", "", Message}
       ├── try SAP HTML page       → ADTError{Status, "", "", summarised}
       └── plain text fallback     → ADTError{Status, "", "", trimmed}

Caller path (e.g. SetSource on 423):
       │
       ▼
isInvalidLockHandle(err)
       │
       ├── Type set?    → Type == ExceptionResourceInvalidLockHandle
       └── Type empty?  → StatusCode == 423   (legacy fallback)
```

## Testing

Unit tests in `adt/client_internal_test.go` (already hosts `parseADTError` tests):

- `<exc:exception>` with all three children → `Namespace`, `Type`, `Message` all populated.
- `<exc:exception>` missing `<namespace>` → `Type` + `Message` populated, `Namespace` empty.
- `<exc:exception>` missing `<type>` → `Namespace` + `Message` populated, `Type` empty.
- `<exc:exception>` missing `<message>` → falls through to plain text (preserves existing "empty message means try next layer" semantics).
- Existing legacy `<ExceptionText>` test: stays green; assert `Namespace` and `Type` are both empty after the change.

Unit tests for `Error()` formatting (new file or `client_internal_test.go`):

- With `Type`: `SAP ADT error 423 (ExceptionResourceLocked): Object is locked by user X`.
- Without `Type`: `SAP ADT error 500: Internal server error`.

Unit tests for predicate behaviour (new in `client_internal_test.go`):

- `isInvalidLockHandle`: 423 + `ExceptionResourceInvalidLockHandle` → true; 423 + `ExceptionResourceLocked` → false; 423 + empty Type → true (legacy fallback); 500 → false.
- `isPreconditionFailed`: 412 + `ExceptionPreconditionFailed` → true; 412 + empty Type → true; other status → false.

Integration test (multi-system, `eachSystem(t)`):

- Trigger an `<exc:exception>` response by performing an operation we know fails — e.g. `LockObject` on a non-existent object URI, or `GetSource` on a malformed URI. The exact `Type` ID may differ slightly between R/3 and S/4 and between operations; the test must not hardcode it.
- Assert: `errors.As(err, &adtErr)` succeeds; `adtErr.Type` is non-empty; `adtErr.Namespace` is non-empty (typically `"com.sap.adt"`); `adtErr.Message` is non-empty.
- Log the observed `Type` per system so we accumulate empirical knowledge of the ID space.
- The test must run on both R/3 and S/4 to confirm the envelope shape is consistent across systems.

## Backwards compatibility

- `ADTError.Message` carries exactly the same string it carries today.
- New fields default to `""`; existing code that reads only `adtErr.Message` is unaffected.
- `Error()` format changes when a type ID is extracted. Consumers that prefix-match `"SAP ADT error 423:"` need a small update — to be bumped in `mcp-server-abap` as a follow-up PR alongside the version bump (per issue note).
- Internal predicate behaviour narrows when `Type` is present (see "Retry predicates" above). Existing call sites (`source.go:368, 379`) are exercised by the integration suite, so a regression there will surface immediately.

## Acceptance criteria

- [ ] `ADTError` has new fields `Namespace` and `Type`, both `string`.
- [ ] Constants `ExceptionTypeResourceInvalidLockHandle`, `ExceptionTypeResourceLocked`, `ExceptionTypePreconditionFailed`, `ExceptionTypeResourceWrongData` exist in `adt/types.go`.
- [ ] `parseADTError` handles `<exc:exception>` (populates Namespace/Type/Message) and `<ExceptionText>` (populates Message only).
- [ ] `Error()` includes the type ID in parentheses when present; format unchanged when absent.
- [ ] `isInvalidLockHandle` / `isPreconditionFailed` prefer `Type` when present, fall back to status code when empty.
- [ ] Existing fixtures and tests (`adt/client_internal_test.go`, `adt/object_test.go:280`, `adt/client_test.go:135`) remain green.
- [ ] New unit tests cover all envelope variants and predicate behaviour.
- [ ] Multi-system integration test exercises a real `<exc:exception>` response on R/3 and S/4.
- [ ] No new dependencies.
