package adt

import (
	"errors"
	"fmt"
)

// Common constants used across ADT operations.
const (
	contentTypeXML = "application/xml"
	nsADTCore      = "http://www.sap.com/adt/core"
)

// SourceResult holds ABAP source code and its ETag for optimistic locking.
type SourceResult struct {
	Source string
	ETag   string
}

// ObjectInfo describes an ABAP repository object.
type ObjectInfo struct {
	URI         string
	Type        string
	Name        string
	Description string
	PackageName string
}

// ActivationMessage is a per-object message from an activation response.
type ActivationMessage struct {
	ObjectURI string
	Type      string // "E" error, "W" warning, "I" info
	Text      string
}

// ActivationResult is returned by ActivateObject.
type ActivationResult struct {
	Success  bool
	Messages []ActivationMessage
}

// SyntaxMessage is a single message from a syntax check.
type SyntaxMessage struct {
	Type   string // "E", "W", "I"
	Text   string
	Line   int
	Column int
}

// TestCase represents a single ABAP unit test method result.
type TestCase struct {
	Name          string
	ExecutionTime float64
	Passed        bool
	Messages      []string
}

// TestResult is returned by RunUnitTests.
type TestResult struct {
	Passed    int
	Failed    int
	Errors    int
	TestCases []TestCase
}

// TransportRequest describes a CTS transport request.
type TransportRequest struct {
	Number      string
	Owner       string
	Description string
	Status      string // "D" = modifiable, "L" = released
}

// TransportCheckResult is returned by CheckTransport.
type TransportCheckResult struct {
	PgmID      string             // R3TR, LIMU, etc.
	Object     string             // PROG, CLAS, INTF, etc.
	ObjectName string             // object name
	DevClass   string             // package
	Result     string             // S=success, E=error
	Recording  bool               // true if object can be recorded
	Requests   []TransportRequest // available transport requests
	Messages   []string           // informational/error messages
}

// CompletionItem represents a single code completion proposal.
type CompletionItem struct {
	Text        string
	Description string
}

// ATCCustomizingResult holds ATC configuration from the SAP system.
type ATCCustomizingResult struct {
	SystemCheckVariant string
	Properties         map[string]string
}

// ATCFinding represents a single ATC check finding.
type ATCFinding struct {
	ObjectURI    string
	Priority     string // 1=error, 2=warning, 3=info
	CheckID      string
	CheckTitle   string
	MessageTitle string
	Location     string // e.g. line number reference
}

// ATCResult is returned by RunATCCheck.
type ATCResult struct {
	WorklistID string
	Findings   []ATCFinding
}

// QueryResult holds the result of a SQL query via ADT data preview.
type QueryResult struct {
	Columns     []QueryColumn
	Rows        [][]string // row-major: Rows[rowIdx][colIdx]
	TotalRows   int
	ExecutionMs float64
}

// QueryColumn describes a single column in a query result.
type QueryColumn struct {
	Name        string
	Type        string // ABAP type: C, N, D, T, P, I, etc.
	Description string
	IsKey       bool
}

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
