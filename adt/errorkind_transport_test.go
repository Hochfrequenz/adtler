package adt_test

import (
	"fmt"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// The two messages below are captured verbatim from live S/4 (HF S/4 Mandant
// 100) on mcp-server-abap#442 — a CTS "object is registered in another open
// request" conflict (LIMU/CINC sub-include and R3TR/DDLS), distinct from the
// runtime ENQUEUE. Both arrive as HTTP 409 / ExceptionResourceLockConflict and
// name the blocking request; retargeting the write at that request succeeds.
const (
	lockedInTransportDDLS = "Object R3TR DDLS /HFQ/DD_ADRESSE is already locked in request S4UK901974 of user BECKT"
	lockedInTransportCINC = "Object LIMU CINC /HFQ/BP_DD_ADRESSE============CCIMP is already locked in request S4UK901974 of user BECKT"
	// A German-locale rendering of the same message. The extractor keys on the
	// request-ID format, not the surrounding words, so localisation must not
	// break it.
	lockedInTransportDE = "Objekt R3TR DDLS /HFQ/DD_ADRESSE ist bereits in Auftrag S4UK901974 von Benutzer BECKT gesperrt"
	// trFixture is the request ID named in each captured message above.
	trFixture = "S4UK901974"
)

func TestClassifyError_ObjectLockedInTransport(t *testing.T) {
	cases := []struct {
		name    string
		err     *adt.ADTError
		wantTR  string
		wantKnd adt.ErrorKind
	}{
		{
			name:    "typed_409_DDLS",
			err:     &adt.ADTError{StatusCode: 409, Type: adt.ExceptionTypeResourceLockConflict, Message: lockedInTransportDDLS},
			wantTR:  trFixture,
			wantKnd: adt.ErrorObjectLockedInTransport,
		},
		{
			name:    "typed_409_CINC",
			err:     &adt.ADTError{StatusCode: 409, Type: adt.ExceptionTypeResourceLockConflict, Message: lockedInTransportCINC},
			wantTR:  trFixture,
			wantKnd: adt.ErrorObjectLockedInTransport,
		},
		{
			name:    "typeless_409_falls_back_to_status",
			err:     &adt.ADTError{StatusCode: 409, Message: lockedInTransportDDLS},
			wantTR:  trFixture,
			wantKnd: adt.ErrorObjectLockedInTransport,
		},
		{
			name:    "german_locale",
			err:     &adt.ADTError{StatusCode: 409, Type: adt.ExceptionTypeResourceLockConflict, Message: lockedInTransportDE},
			wantTR:  trFixture,
			wantKnd: adt.ErrorObjectLockedInTransport,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := adt.ClassifyError(tc.err); got != tc.wantKnd {
				t.Errorf("ClassifyError = %v, want %v", got, tc.wantKnd)
			}
			tr, ok := tc.err.LockingTransport()
			if !ok || tr != tc.wantTR {
				t.Errorf("LockingTransport() = (%q, %v), want (%q, true)", tr, ok, tc.wantTR)
			}
		})
	}
}

func TestClassifyError_LockConflictWithoutTransportStaysLockConflict(t *testing.T) {
	// A lock conflict whose message names no CTS request must remain the generic
	// ErrorLockConflict — the refinement only fires when a request ID is present.
	cases := []*adt.ADTError{
		{StatusCode: 409, Type: adt.ExceptionTypeResourceLockConflict, Message: "save conflict — a different enqueue is held"},
		{StatusCode: 409, Message: "conflict"},
	}
	for i, err := range cases {
		t.Run(fmt.Sprintf("case_%d", i), func(t *testing.T) {
			if got := adt.ClassifyError(err); got != adt.ErrorLockConflict {
				t.Errorf("ClassifyError = %v, want ErrorLockConflict", got)
			}
			if tr, ok := err.LockingTransport(); ok {
				t.Errorf("LockingTransport() = (%q, true), want (\"\", false)", tr)
			}
		})
	}
}

func TestADTError_LockingTransport(t *testing.T) {
	cases := []struct {
		name    string
		message string
		wantTR  string
		wantOK  bool
	}{
		{"ddls_english", lockedInTransportDDLS, trFixture, true},
		{"cinc_english", lockedInTransportCINC, trFixture, true},
		{"german", lockedInTransportDE, trFixture, true},
		{"no_request_id", "Object is already locked by another user", "", false},
		{"object_name_not_matched", "Object R3TR DDLS /HFQ/DD_ADRESSE locked", "", false},
		{"empty", "", "", false},
		// Ordering hazard: the object name precedes the request in the message.
		// If the name itself matches the <SID>K<6digit> shape, the FIRST match
		// would be the name — the request ID is always LAST, so last-match wins.
		{"object_name_matches_pattern", "Object R3TR PROG ABCK123456 is already locked in request S4UK901974 of user X", trFixture, true},
		// Task + request both present (both share the <SID>K###### format); the
		// request is named last.
		{"task_and_request", "locked in task S4UK901975 request S4UK901974 of user X", trFixture, true},
		{"lowercase_not_matched", "already locked in request s4uk901974", "", false},
		{"non_K_category_not_matched", "already locked in request ABCT123456", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := &adt.ADTError{StatusCode: 409, Message: tc.message}
			tr, ok := err.LockingTransport()
			if ok != tc.wantOK || tr != tc.wantTR {
				t.Errorf("LockingTransport(%q) = (%q, %v), want (%q, %v)", tc.message, tr, ok, tc.wantTR, tc.wantOK)
			}
		})
	}
}
