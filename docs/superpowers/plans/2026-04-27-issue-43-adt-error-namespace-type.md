# ADTError namespace+type extraction (Issue #43) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the SAP-stable `<namespace>` + `<type>` identifier from `<exc:exception>` envelopes into `ADTError`, expose canonical type-ID constants, and rewrite the internal retry predicates to use the structured `Type` field.

**Architecture:** `parseADTError` gains a new "layer 1" that recognises `<exc:exception>` and populates `ADTError.Namespace` / `Type` / `Message`. The legacy `<ExceptionText>` parser becomes layer 2. `Error()` includes the type ID in parentheses when present. `isInvalidLockHandle` / `isPreconditionFailed` prefer the new `Type` field, with status-code fallback for legacy responses.

**Tech Stack:** Go 1.25+, `encoding/xml`, `httptest` for unit tests, `eachSystem(t)` for multi-system integration tests.

**Branch:** `feat/43-adt-error-namespace-type` (already created and on this branch — design doc committed as `dcc37d0`).

**Spec:** `docs/superpowers/specs/2026-04-27-issue-43-adt-error-namespace-type-design.md`

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `adt/types.go` | Modify | Add `Namespace`/`Type` fields to `ADTError`; update `Error()`; add `ExceptionType*` constants; rewrite `isInvalidLockHandle`/`isPreconditionFailed`. |
| `adt/client.go` | Modify | Add `<exc:exception>` parsing as new layer 1 of `parseADTError`. |
| `adt/client_internal_test.go` | Modify | Add unit tests for new envelope, new edge cases, `Error()` formatting, and rewritten predicates. |
| `adt/error_namespace_type_integration_test.go` | Create | Multi-system integration test: trigger an `<exc:exception>` and assert `Type`/`Namespace` populated on both R/3 and S/4. |

The existing test in `adt/object_test.go:301-307` already checks that `err.Error()` contains `"wrong input data for processing"` — that substring survives the new format (`SAP ADT error 400 (ExceptionResourceWrongData): Resource ZCL_TEST: wrong input data for processing`), so no change there.

---

## Task 1: Extend `ADTError` and update `Error()`

**Files:**
- Modify: `adt/types.go` (lines 130-138)
- Modify: `adt/client_internal_test.go` (append new tests at end)

- [ ] **Step 1: Write failing tests for `Error()` formatting**

Append to `adt/client_internal_test.go`:

```go
// TestADTError_Error_WithType verifies that when Type is populated, Error()
// includes it in parentheses after the status code.
func TestADTError_Error_WithType(t *testing.T) {
	e := &ADTError{
		StatusCode: 423,
		Namespace:  "com.sap.adt",
		Type:       "ExceptionResourceLocked",
		Message:    "Object is locked by user X",
	}
	got := e.Error()
	want := "SAP ADT error 423 (ExceptionResourceLocked): Object is locked by user X"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// TestADTError_Error_WithoutType verifies that when Type is empty, Error()
// preserves the legacy format exactly.
func TestADTError_Error_WithoutType(t *testing.T) {
	e := &ADTError{StatusCode: 500, Message: "Internal server error"}
	got := e.Error()
	want := "SAP ADT error 500: Internal server error"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run tests, verify they FAIL**

```
go test -run TestADTError_Error -v ./adt/...
```

Expected: `TestADTError_Error_WithType` fails to compile because the `Namespace` and `Type` fields don't exist yet.

- [ ] **Step 3: Add the new fields and update `Error()`**

In `adt/types.go`, replace lines 130-138 (the `ADTError` struct and `Error()` method) with:

```go
// ADTError is returned when SAP ADT responds with an error status.
//
// Namespace and Type carry the SAP-stable identifier from <exc:exception>
// envelopes (the ADT equivalent of an ABAP MSGID/MSGNO). They are populated
// when the error body matches the modern <exc:exception> schema and remain
// "" for legacy <ExceptionText> bodies, HTML error pages, and plain-text
// fallbacks. Callers that branch on a specific exception (e.g. resource
// locked) should compare ADTError.Type against an ExceptionType* constant
// rather than substring-matching the localised Message.
type ADTError struct {
	StatusCode int
	Namespace  string // e.g. "com.sap.adt" — empty if unknown
	Type       string // e.g. "ExceptionResourceLocked" — empty if unknown
	Message    string
}

func (e *ADTError) Error() string {
	if e.Type != "" {
		return fmt.Sprintf("SAP ADT error %d (%s): %s", e.StatusCode, e.Type, e.Message)
	}
	return fmt.Sprintf("SAP ADT error %d: %s", e.StatusCode, e.Message)
}
```

- [ ] **Step 4: Run tests, verify they PASS**

```
go test -run TestADTError_Error -v ./adt/...
```

Expected: both `TestADTError_Error_WithType` and `TestADTError_Error_WithoutType` pass.

- [ ] **Step 5: Run the full unit suite to confirm no regression**

```
go test ./...
```

Expected: all green. The substring assertion in `adt/object_test.go:305` (`"wrong input data for processing"`) still matches because the new format keeps the message as a suffix.

- [ ] **Step 6: Commit**

```
git add adt/types.go adt/client_internal_test.go
git commit -m "$(cat <<'EOF'
feat(#43): add Namespace/Type fields to ADTError, update Error() format

Adds two new fields on ADTError that hold the SAP-stable identifier
extracted from <exc:exception> envelopes. Error() now prints the type
in parentheses when present; format unchanged when Type is empty.

Population of the fields by parseADTError comes in the next commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Parse `<exc:exception>` envelope in `parseADTError`

**Files:**
- Modify: `adt/client.go` (function `parseADTError`, lines 503-534)
- Modify: `adt/client_internal_test.go` (append tests)

- [ ] **Step 1: Write failing test for the modern envelope**

Append to `adt/client_internal_test.go`:

```go
// TestParseADTError_ExcExceptionEnvelope verifies the new layer 1: when the
// body is the modern <exc:exception> shape with namespace, type, and message
// children, all three are extracted into ADTError.
func TestParseADTError_ExcExceptionEnvelope(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceWrongData"/>
  <message lang="EN">Resource ZCL_TEST: wrong input data for processing</message>
</exc:exception>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.StatusCode != 400 {
		t.Errorf("StatusCode: got %d, want 400", adtErr.StatusCode)
	}
	if adtErr.Namespace != "com.sap.adt" {
		t.Errorf("Namespace: got %q, want %q", adtErr.Namespace, "com.sap.adt")
	}
	if adtErr.Type != "ExceptionResourceWrongData" {
		t.Errorf("Type: got %q, want %q", adtErr.Type, "ExceptionResourceWrongData")
	}
	if adtErr.Message != "Resource ZCL_TEST: wrong input data for processing" {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}
```

- [ ] **Step 2: Run test, verify it FAILS**

```
go test -run TestParseADTError_ExcExceptionEnvelope -v ./adt/...
```

Expected: FAIL — current parser doesn't recognise `<exc:exception>`, so `Namespace` and `Type` come back empty (and `Message` is whatever the plain-text fallback produces).

- [ ] **Step 3: Add layer 1 to `parseADTError`**

In `adt/client.go`, replace the body of `parseADTError` (lines 515-534) with:

```go
func parseADTError(statusCode int, body io.Reader) error {
	data, _ := io.ReadAll(body)

	// Layer 1: modern <exc:exception> envelope.
	// Captures Namespace and Type for callers that need to branch on a
	// stable, locale-independent identifier (the ADT equivalent of MSGID).
	var excEnv struct {
		XMLName   xml.Name `xml:"exception"`
		Namespace struct {
			ID string `xml:"id,attr"`
		} `xml:"namespace"`
		Type struct {
			ID string `xml:"id,attr"`
		} `xml:"type"`
		Message string `xml:"message"`
	}
	if err := xml.Unmarshal(data, &excEnv); err == nil && excEnv.Message != "" {
		return &ADTError{
			StatusCode: statusCode,
			Namespace:  excEnv.Namespace.ID,
			Type:       excEnv.Type.ID,
			Message:    excEnv.Message,
		}
	}

	// Layer 2: legacy <ExceptionText> envelope. Namespace/Type stay empty.
	var legacy struct {
		XMLName xml.Name `xml:"ExceptionText"`
		Message string   `xml:"message"`
	}
	if err := xml.Unmarshal(data, &legacy); err == nil && legacy.Message != "" {
		return &ADTError{StatusCode: statusCode, Message: legacy.Message}
	}

	// Layer 3: SAP HTML "Application Server Error" page.
	if msg := parseHTMLErrorBody(data); msg != "" {
		return &ADTError{StatusCode: statusCode, Message: msg}
	}

	// Layer 4: any other body, trimmed.
	return &ADTError{StatusCode: statusCode, Message: strings.TrimSpace(string(data))}
}
```

The doc-comment block above the function (lines 503-514) already lists three layers; update it to four:

```go
// parseADTError reads an error response body and returns an *ADTError.
//
// The body is parsed in four layers:
//  1. As the modern <exc:exception> envelope — populates Namespace, Type,
//     and Message. This is the SAP-ADT equivalent of an MSGID/MSGNO and is
//     stable across SAP releases and locales.
//  2. As the legacy ADT framework <ExceptionText><message>…</message>
//     envelope — populates Message only.
//  3. As a SAP "Application Server Error" HTML page — see adtler#13 /
//     mcp-server-abap#292 for the regression this layer prevents (dumping
//     several KB of HTML, CSS, and base64-encoded PNG into the message).
//  4. As-is (trimmed) for anything else, preserving prior behaviour for
//     non-XML, non-HTML bodies.
```

- [ ] **Step 4: Run the new test, verify it PASSES**

```
go test -run TestParseADTError_ExcExceptionEnvelope -v ./adt/...
```

Expected: PASS.

- [ ] **Step 5: Add edge-case tests**

Append to `adt/client_internal_test.go`:

```go
// TestParseADTError_ExcExceptionMissingNamespace verifies that when the
// modern envelope omits <namespace>, Type and Message are still extracted
// and Namespace stays empty.
func TestParseADTError_ExcExceptionMissingNamespace(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <type id="ExceptionResourceWrongData"/>
  <message lang="EN">Some message</message>
</exc:exception>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.Namespace != "" {
		t.Errorf("Namespace: got %q, want empty", adtErr.Namespace)
	}
	if adtErr.Type != "ExceptionResourceWrongData" {
		t.Errorf("Type: got %q, want %q", adtErr.Type, "ExceptionResourceWrongData")
	}
	if adtErr.Message != "Some message" {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}

// TestParseADTError_ExcExceptionMissingType verifies that when the modern
// envelope omits <type>, Namespace and Message are still extracted and Type
// stays empty.
func TestParseADTError_ExcExceptionMissingType(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <message lang="EN">Some message</message>
</exc:exception>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.Namespace != "com.sap.adt" {
		t.Errorf("Namespace: got %q, want %q", adtErr.Namespace, "com.sap.adt")
	}
	if adtErr.Type != "" {
		t.Errorf("Type: got %q, want empty", adtErr.Type)
	}
	if adtErr.Message != "Some message" {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}

// TestParseADTError_ExcExceptionMissingMessage verifies that an
// <exc:exception> body without a <message> child falls through to the
// plain-text layer (preserves the existing "empty message means try the
// next layer" semantics shared with the legacy parser).
func TestParseADTError_ExcExceptionMissingMessage(t *testing.T) {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceWrongData"/>
</exc:exception>`
	err := parseADTError(400, strings.NewReader(body))
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	// Without a <message>, layer 1 is skipped. Layers 2 and 3 don't match
	// either, so we fall through to layer 4 with the trimmed raw body.
	// Namespace/Type stay empty because layer 1 didn't claim the body.
	if adtErr.Namespace != "" {
		t.Errorf("Namespace: got %q, want empty (layer 1 should not have claimed)", adtErr.Namespace)
	}
	if adtErr.Type != "" {
		t.Errorf("Type: got %q, want empty (layer 1 should not have claimed)", adtErr.Type)
	}
	if !strings.Contains(adtErr.Message, "<exc:exception") {
		t.Errorf("Message: expected raw XML body in plain-text fallback, got %q", adtErr.Message)
	}
}

// TestParseADTError_LegacyEnvelopeNoNamespaceOrType verifies that the legacy
// <ExceptionText> path leaves Namespace and Type empty (regression guard).
func TestParseADTError_LegacyEnvelopeNoNamespaceOrType(t *testing.T) {
	body := strings.NewReader(`<?xml version="1.0" encoding="utf-8"?>
<exc:ExceptionText xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <message>Resource ZCL_TEST: wrong input data for processing</message>
</exc:ExceptionText>`)
	err := parseADTError(400, body)
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *ADTError, got %T: %v", err, err)
	}
	if adtErr.Namespace != "" {
		t.Errorf("Namespace: got %q, want empty (legacy form has no namespace)", adtErr.Namespace)
	}
	if adtErr.Type != "" {
		t.Errorf("Type: got %q, want empty (legacy form has no type)", adtErr.Type)
	}
	if adtErr.Message != "Resource ZCL_TEST: wrong input data for processing" {
		t.Errorf("Message: got %q", adtErr.Message)
	}
}
```

- [ ] **Step 6: Run all parser tests**

```
go test -run TestParseADTError -v ./adt/...
```

Expected: all four new tests plus the four pre-existing ones (`XMLEnvelope`, `HTMLBody`, `HTMLWithoutSAPLayout`, `PlainText`) pass.

- [ ] **Step 7: Run the full suite**

```
go test ./...
```

Expected: all green. In particular, `TestDeleteObject_PropagatesETagFetchError` (the test in `adt/object_test.go:280`) keeps passing — its mock body now goes through layer 1 instead of falling through to plain text, but the assertion only checks that the message contains `"wrong input data for processing"`, which it still does.

- [ ] **Step 8: Commit**

```
git add adt/client.go adt/client_internal_test.go
git commit -m "$(cat <<'EOF'
feat(#43): parse <exc:exception> envelope into ADTError

parseADTError gains a new layer 1 that recognises the modern
<exc:exception> envelope and populates Namespace, Type, and Message.
The legacy <ExceptionText> path becomes layer 2 and is otherwise
unchanged.

Layer 1 falls through when <message> is absent, preserving the
existing "empty message means try the next layer" semantics.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Exception type constants + retry-predicate rewrite

**Files:**
- Modify: `adt/types.go` (add constants block; rewrite `isInvalidLockHandle` lines 144-150 and `isPreconditionFailed` lines 156-162)
- Modify: `adt/client_internal_test.go` (append predicate tests)

- [ ] **Step 1: Add constants to `adt/types.go`**

Insert this constant block immediately above the `ADTError` type (current line 130):

```go
// Exception type IDs that adtler reacts to internally.
//
// These are the values SAP places in <exc:exception><type id="…"/> for the
// exceptions adtler asserts against (retry logic, error classification).
// They are stable across SAP releases and locales — far safer to compare
// against than the localised <message> text. Consumer code is welcome to
// compare against bare strings for IDs not listed here; new constants are
// added on demand.
const (
	ExceptionTypeResourceInvalidLockHandle = "ExceptionResourceInvalidLockHandle"
	ExceptionTypeResourceLocked            = "ExceptionResourceLocked"
	ExceptionTypePreconditionFailed        = "ExceptionPreconditionFailed"
	ExceptionTypeResourceWrongData         = "ExceptionResourceWrongData"
)
```

- [ ] **Step 2: Write failing tests for the new predicate behaviour**

Append to `adt/client_internal_test.go`:

```go
// TestIsInvalidLockHandle_TypeAware exercises the rewritten predicate:
// when Type is populated, the predicate matches on Type, not status code.
// When Type is empty (legacy responses), the predicate falls back to the
// status-code check.
func TestIsInvalidLockHandle_TypeAware(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "423 with InvalidLockHandle type → true",
			err:  &ADTError{StatusCode: 423, Type: ExceptionTypeResourceInvalidLockHandle, Message: "x"},
			want: true,
		},
		{
			name: "423 with ResourceLocked type → false (different exception)",
			err:  &ADTError{StatusCode: 423, Type: ExceptionTypeResourceLocked, Message: "x"},
			want: false,
		},
		{
			name: "423 with empty Type → true (legacy fallback)",
			err:  &ADTError{StatusCode: 423, Message: "x"},
			want: true,
		},
		{
			name: "500 with empty Type → false",
			err:  &ADTError{StatusCode: 500, Message: "x"},
			want: false,
		},
		{
			name: "non-ADTError → false",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "nil → false",
			err:  nil,
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInvalidLockHandle(tc.err); got != tc.want {
				t.Errorf("isInvalidLockHandle(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsPreconditionFailed_TypeAware mirrors TestIsInvalidLockHandle_TypeAware
// for the 412 / ExceptionPreconditionFailed predicate.
func TestIsPreconditionFailed_TypeAware(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "412 with PreconditionFailed type → true",
			err:  &ADTError{StatusCode: 412, Type: ExceptionTypePreconditionFailed, Message: "x"},
			want: true,
		},
		{
			name: "412 with WrongData type → false (different exception)",
			err:  &ADTError{StatusCode: 412, Type: ExceptionTypeResourceWrongData, Message: "x"},
			want: false,
		},
		{
			name: "412 with empty Type → true (legacy fallback)",
			err:  &ADTError{StatusCode: 412, Message: "x"},
			want: true,
		},
		{
			name: "500 with empty Type → false",
			err:  &ADTError{StatusCode: 500, Message: "x"},
			want: false,
		},
		{
			name: "non-ADTError → false",
			err:  errors.New("some other error"),
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPreconditionFailed(tc.err); got != tc.want {
				t.Errorf("isPreconditionFailed(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 3: Run tests, verify the type-mismatch cases FAIL**

```
go test -run "TestIsInvalidLockHandle_TypeAware|TestIsPreconditionFailed_TypeAware" -v ./adt/...
```

Expected: subtests with status codes that match but Type that mismatches (e.g. "423 with ResourceLocked type → false") FAIL, because the current predicates check status code only and would return true.

- [ ] **Step 4: Rewrite the predicates in `adt/types.go`**

Replace the bodies of `isInvalidLockHandle` and `isPreconditionFailed` (lines 144-162) with:

```go
// isInvalidLockHandle returns true if the error is a 423
// ExceptionResourceInvalidLockHandle from SAP. Used by SetSource to
// decide whether to retry with a different lock handle delivery
// mechanism (header vs query param). See adtler#4.
//
// When the error carries a populated Type, this is a structural check
// against ExceptionTypeResourceInvalidLockHandle. When Type is empty
// (legacy <ExceptionText> responses), the function falls back to the
// status-code check that predates the structured envelope support.
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

// isPreconditionFailed returns true if the error is a 412
// ExceptionPreconditionFailed from SAP. Used by SetSource to retry
// with a re-fetched ETag when the original ETag doesn't match what
// the server expects (e.g. TABL charset mismatch). See adtler#15.
//
// Type-aware in the same way as isInvalidLockHandle: prefers Type
// when present, falls back to status code for legacy responses.
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

- [ ] **Step 5: Run predicate tests, verify they PASS**

```
go test -run "TestIsInvalidLockHandle_TypeAware|TestIsPreconditionFailed_TypeAware" -v ./adt/...
```

Expected: all subtests pass.

- [ ] **Step 6: Run the full suite**

```
go test ./...
```

Expected: all green. The key sanity check is that source.go's lock-handle retry path (`source.go:379`) and ETag-charset retry path (`source.go:368`) still trigger correctly — they're exercised by `TestSetSource_*` unit tests in `adt/source_test.go`.

- [ ] **Step 7: Commit**

```
git add adt/types.go adt/client_internal_test.go
git commit -m "$(cat <<'EOF'
feat(#43): expose ExceptionType* constants, type-aware retry predicates

Exports four canonical type-ID constants for the exceptions adtler
reacts to internally, and rewrites isInvalidLockHandle /
isPreconditionFailed to prefer the structured Type field over status
code. Legacy responses (Type empty) keep the status-code-only
behaviour, so there is no regression on systems still emitting
<ExceptionText>.

Behaviour change worth flagging: a 423 ExceptionResourceLocked (object
held by another user) no longer triggers the lock-handle-delivery
retry. That retry only makes sense when SAP says the lock handle
itself is invalid; the previous "any 423" check was over-broad.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Multi-system integration test

**Files:**
- Create: `adt/error_namespace_type_integration_test.go`

- [ ] **Step 1: Create the integration test file**

Write `adt/error_namespace_type_integration_test.go` with:

```go
//go:build integration

package adt_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestADTError_NamespaceTypeExtraction_MultiSystem verifies issue #43:
// when a real SAP system returns an <exc:exception> envelope, parseADTError
// populates ADTError.Namespace and ADTError.Type from <namespace id="…"/>
// and <type id="…"/>.
//
// We don't hardcode the exact Type ID because it can differ slightly
// between R/3 and S/4 (and between operations). Instead the test asserts
// that BOTH fields are non-empty and logs the observed values per system,
// so we accumulate empirical knowledge of the ID space.
//
// The trigger is LockObject on a non-existent class URI, which reliably
// produces an exception envelope on both R/3 and S/4.
func TestADTError_NamespaceTypeExtraction_MultiSystem(t *testing.T) {
	ctx := context.Background()
	// Deliberately bogus URI — no SAP system will have this object.
	const fakeURI = "/sap/bc/adt/oo/classes/zcl_adt_mcp_exc_test_xxx"

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			_, err := sys.Client.LockObject(ctx, fakeURI)
			if err == nil {
				t.Fatalf("[%s] expected LockObject on non-existent URI to fail, got nil", sys.Name)
			}

			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("[%s] expected *adt.ADTError, got %T: %v", sys.Name, err, err)
			}

			t.Logf("[%s] StatusCode=%d Namespace=%q Type=%q Message=%q",
				sys.Name, adtErr.StatusCode, adtErr.Namespace, adtErr.Type, adtErr.Message)

			if adtErr.Namespace == "" {
				t.Errorf("[%s] expected non-empty Namespace, got %q (full error: %v)",
					sys.Name, adtErr.Namespace, err)
			}
			if adtErr.Type == "" {
				t.Errorf("[%s] expected non-empty Type, got %q (full error: %v)",
					sys.Name, adtErr.Type, err)
			}
			if adtErr.Message == "" {
				t.Errorf("[%s] expected non-empty Message, got empty", sys.Name)
			}
		})
	}
}
```

- [ ] **Step 2: Verify the integration build compiles**

```
go vet -tags integration ./adt/...
go build -tags integration ./adt/...
```

Expected: no errors.

- [ ] **Step 3: Verify the file is picked up by the integration test runner (compile-only)**

```
go test -tags=integration -run TestADTError_NamespaceTypeExtraction_MultiSystem -count=1 ./adt/...
```

Expected: either the test runs (if `SAP_INTEGRATION_SYSTEMS` is configured locally) or it skips with the standard `eachSystem` skip message — but it MUST compile and be discovered. If you see "no tests to run" something is wrong with the build tag or filename.

- [ ] **Step 4: Commit**

```
git add adt/error_namespace_type_integration_test.go
git commit -m "$(cat <<'EOF'
test(#43): add multi-system integration test for ADTError Namespace/Type

Triggers an <exc:exception> response from real SAP by attempting to
lock a non-existent class URI, then asserts that ADTError.Namespace
and ADTError.Type are both populated. The test does not hardcode the
exact Type ID — that can differ between R/3 and S/4 — but logs the
observed values so we accumulate empirical knowledge of the ID space.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Pre-PR verification

**Files:** none modified.

- [ ] **Step 1: Run unit tests**

```
go test ./...
```

Expected: all green.

- [ ] **Step 2: Run vet with the integration tag**

```
go vet -tags integration ./adt/...
```

Expected: no warnings.

- [ ] **Step 3: Build with the integration tag**

```
go build -tags integration ./adt/...
```

Expected: no errors.

- [ ] **Step 4: Run linter**

```
golangci-lint run --enable dupl,goconst,gocyclo ./...
```

Expected: no findings. Common gotchas already considered:
- `goconst`: the four exception type IDs are repeated across tests — they're already extracted into the new `ExceptionType*` constants, so reuse them in tests rather than re-typing the strings if the linter complains about additional duplication.
- `gocyclo`: `parseADTError` gains one more layer; if it crosses the threshold, the layered structure is intentional — extract a helper only if golangci-lint complains.

- [ ] **Step 5: Sanity-check the diff one more time**

```
git log --oneline main..HEAD
git diff main..HEAD --stat
```

Expected: 4 commits (design spec + three feature commits + integration test) — review file count and lines-changed totals match expectation (around 4 changed files plus the integration test file and the spec doc).

---

## Task 6: Open the pull request

**Files:** none.

- [ ] **Step 1: Push the branch**

```
git push -u origin feat/43-adt-error-namespace-type
```

- [ ] **Step 2: Open the PR**

```
gh pr create --title "feat(#43): extract namespace+type from <exc:exception> into ADTError" --body "$(cat <<'EOF'
## Summary

- Adds `Namespace` and `Type` fields to `ADTError`, populated from `<exc:exception>` envelopes (issue #43).
- Adds `ExceptionType*` constants for the four exception IDs adtler reacts to internally.
- Rewrites `isInvalidLockHandle` and `isPreconditionFailed` to prefer the structured `Type` field, with status-code fallback for legacy `<ExceptionText>` responses.
- `Error()` includes the type ID in parentheses when present; format unchanged otherwise.

## Behaviour change

A 423 `ExceptionResourceLocked` (object held by another user) no longer triggers the lock-handle-delivery retry — that retry is for `ExceptionResourceInvalidLockHandle` only. The previous "any 423" check was over-broad. Existing legacy responses (Type empty) keep the status-code-only behaviour.

## Consumer impact

Consumers that prefix-match `"SAP ADT error 423:"` on `Error()` output will see the new `(ExceptionResourceLocked)` qualifier when SAP returns an `<exc:exception>` envelope. The follow-up bump in `mcp-server-abap` (Hochfrequenz/mcp-server-abap#310) updates the canonical event logger to consume `adtErr.Type` directly.

## Test plan

- [x] Unit: 8 new tests in `adt/client_internal_test.go` covering envelope parsing, edge cases, `Error()` formatting, and predicate behaviour.
- [x] Unit: existing `TestParseADTError_*` and `TestDeleteObject_PropagatesETagFetchError` stay green.
- [ ] Integration: `TestADTError_NamespaceTypeExtraction_MultiSystem` against R/3 + S/4 — pending integration agent.

## Cross-references

- Issue: Hochfrequenz/adtler#43
- Consumer-side: Hochfrequenz/mcp-server-abap#310
- Design spec: `docs/superpowers/specs/2026-04-27-issue-43-adt-error-namespace-type-design.md`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 3: Add the integration-test label**

```
gh pr edit --add-label needs:integration-test
```

- [ ] **Step 4: Capture the PR URL**

`gh pr view --json url -q .url` — note this for the user; the integration-test agent will need it.

---

## Self-Review Notes

- **Spec coverage:** Each acceptance-criterion bullet maps to a task — Task 1 (fields + Error()), Task 2 (parser), Task 3 (constants + predicates), Task 4 (integration test). Backwards-compat note about `Error()` is captured in PR body.
- **Type consistency:** Field names `Namespace` / `Type`, constants `ExceptionType*`, predicate names `isInvalidLockHandle` / `isPreconditionFailed` — used identically in every task.
- **No placeholders:** every code step has the full snippet, every command has the expected output, no "similar to Task N" references.
