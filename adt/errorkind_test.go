package adt_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

func TestClassifyError_ByExceptionType(t *testing.T) {
	cases := []struct {
		excType string
		want    adt.ErrorKind
	}{
		{adt.ExceptionTypeResourceAlreadyExists, adt.ErrorAlreadyExists},
		{adt.ExceptionTypeResourceLocked, adt.ErrorLocked},
		{adt.ExceptionTypeResourceLockConflict, adt.ErrorLockConflict},
		{adt.ExceptionTypeResourceInvalidLockHandle, adt.ErrorInvalidLockHandle},
		{adt.ExceptionTypeResourceInvalidEtag, adt.ErrorEtagMismatch},
		{adt.ExceptionTypePreconditionFailed, adt.ErrorEtagMismatch},
		{adt.ExceptionTypeResourceNotAcceptable, adt.ErrorNotAcceptable},
		{adt.ExceptionTypeUnsupportedMediaType, adt.ErrorUnsupportedMedia},
		{adt.ExceptionTypeUnprocessableEntity, adt.ErrorUnprocessable},
		{adt.ExceptionTypeResourceWrongData, adt.ErrorUnprocessable},
		{adt.ExceptionTypeNotAllowed, adt.ErrorMethodNotAllowed},
		{adt.ExceptionTypeResourceCreationFailure, adt.ErrorCreationFailed},
	}
	for _, tc := range cases {
		t.Run(tc.excType, func(t *testing.T) {
			err := &adt.ADTError{Type: tc.excType, StatusCode: 200, Message: "x"}
			if got := adt.ClassifyError(err); got != tc.want {
				t.Errorf("ClassifyError(Type=%s) = %v, want %v", tc.excType, got, tc.want)
			}
		})
	}
}

func TestClassifyError_ByStatusCode(t *testing.T) {
	cases := []struct {
		status int
		want   adt.ErrorKind
	}{
		{423, adt.ErrorLocked},
		{412, adt.ErrorEtagMismatch},
		{409, adt.ErrorLockConflict},
		{406, adt.ErrorNotAcceptable},
		{415, adt.ErrorUnsupportedMedia},
		{422, adt.ErrorUnprocessable},
		{405, adt.ErrorMethodNotAllowed},
		{404, adt.ErrorNotFound},
		{403, adt.ErrorForbidden},
		{400, adt.ErrorBadRequest},
		{500, adt.ErrorServerError},
		{418, adt.ErrorUnknown}, // unmapped status
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			// Empty Type forces the status-code fallback.
			err := &adt.ADTError{StatusCode: tc.status, Message: "x"}
			if got := adt.ClassifyError(err); got != tc.want {
				t.Errorf("ClassifyError(status=%d) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}

func TestClassifyError_TypeTakesPrecedenceOverStatus(t *testing.T) {
	// On R/3, "already exists" arrives as HTTP 405 but with the AlreadyExists
	// Type. Type must win over the status-code fallback (which would say
	// MethodNotAllowed).
	err := &adt.ADTError{StatusCode: 405, Type: adt.ExceptionTypeResourceAlreadyExists, Message: "exists"}
	if got := adt.ClassifyError(err); got != adt.ErrorAlreadyExists {
		t.Errorf("got %v, want ErrorAlreadyExists", got)
	}
}

func TestClassifyError_UnrecognisedTypeFallsBackToStatus(t *testing.T) {
	err := &adt.ADTError{StatusCode: 404, Type: "ExceptionSomethingBrandNew", Message: "x"}
	if got := adt.ClassifyError(err); got != adt.ErrorNotFound {
		t.Errorf("got %v, want ErrorNotFound (status fallback)", got)
	}
}

func TestClassifyError_BareLockStatusIsLockedNotInvalidHandle(t *testing.T) {
	// A typeless 423 classifies as ErrorLocked (the general meaning), distinct
	// from the internal invalid-lock-handle retry heuristic.
	err := &adt.ADTError{StatusCode: 423, Message: "locked"}
	if got := adt.ClassifyError(err); got != adt.ErrorLocked {
		t.Errorf("got %v, want ErrorLocked", got)
	}
}

func TestClassifyError_NonADTError(t *testing.T) {
	if got := adt.ClassifyError(errors.New("plain error")); got != adt.ErrorUnknown {
		t.Errorf("plain error: got %v, want ErrorUnknown", got)
	}
	if got := adt.ClassifyError(nil); got != adt.ErrorUnknown {
		t.Errorf("nil: got %v, want ErrorUnknown", got)
	}
}

func TestClassifyError_WrappedADTError(t *testing.T) {
	wrapped := fmt.Errorf("set source: %w", &adt.ADTError{StatusCode: 412, Message: "etag"})
	if got := adt.ClassifyError(wrapped); got != adt.ErrorEtagMismatch {
		t.Errorf("wrapped: got %v, want ErrorEtagMismatch", got)
	}
}

func TestErrorKind_String(t *testing.T) {
	cases := map[adt.ErrorKind]string{
		adt.ErrorUnknown:                 "unknown",
		adt.ErrorAlreadyExists:           "already_exists",
		adt.ErrorLocked:                  "locked",
		adt.ErrorLockConflict:            "lock_conflict",
		adt.ErrorInvalidLockHandle:       "invalid_lock_handle",
		adt.ErrorEtagMismatch:            "etag_mismatch",
		adt.ErrorNotAcceptable:           "not_acceptable",
		adt.ErrorUnsupportedMedia:        "unsupported_media",
		adt.ErrorUnprocessable:           "unprocessable",
		adt.ErrorMethodNotAllowed:        "method_not_allowed",
		adt.ErrorCreationFailed:          "creation_failed",
		adt.ErrorNotFound:                "not_found",
		adt.ErrorForbidden:               "forbidden",
		adt.ErrorBadRequest:              "bad_request",
		adt.ErrorServerError:             "server_error",
		adt.ErrorObjectLockedInTransport: "object_locked_in_transport",
	}
	// Guard against an unhandled kind silently returning "unknown": a bogus
	// value must map to "unknown", but every named kind above must not.
	if got := adt.ErrorKind(999).String(); got != "unknown" {
		t.Errorf("out-of-range kind = %q, want %q", got, "unknown")
	}
	for kind, want := range cases {
		if got := kind.String(); got != want {
			t.Errorf("ErrorKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}
