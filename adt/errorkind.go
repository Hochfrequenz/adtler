package adt

import "errors"

// ErrorKind is a stable, language- and (where possible) system-independent
// classification of an ADT error. It is derived from ADTError.Type when the
// error carries a modern <exc:exception> envelope, and from the HTTP status
// code otherwise. It lets callers branch on the *meaning* of a failure without
// substring-matching localised messages or re-encoding SAP's cross-system
// status-code quirks (e.g. "already exists" is 400 on S/4 but 405 on R/3).
//
// ErrorKind classifies the protocol-level error only. Conditions SAP does not
// surface with a distinct Type or status — a name collision reported as a
// generic creation failure, or a "transport required" hidden inside a 400 —
// are reported as their broad kind (ErrorCreationFailed, ErrorBadRequest);
// callers wanting finer detail inspect the message themselves.
//
// One deliberate exception: a lock conflict is promoted to
// ErrorObjectLockedInTransport when the message carries a CTS request ID. SAP
// does not expose the blocking request in any structured field, only in the
// localised message text, so this is the one place classification inspects the
// message — and it keys on the language-independent request-ID format, not on
// translated words. See refineLockConflict and ADTError.LockingTransport.
type ErrorKind int

const (
	// ErrorUnknown is returned for non-ADTError errors and for ADT errors that
	// match no known Type or status code.
	ErrorUnknown           ErrorKind = iota
	ErrorAlreadyExists               // an object with that name already exists
	ErrorLocked                      // the object is locked by another user/transport
	ErrorLockConflict                // save/lock conflict — a different enqueue is held
	ErrorInvalidLockHandle           // the supplied lock handle is invalid or expired
	ErrorEtagMismatch                // an If-Match / ETag precondition failed
	ErrorNotAcceptable               // content negotiation failed (Accept)
	ErrorUnsupportedMedia            // the request Content-Type is not accepted
	ErrorUnprocessable               // the request is semantically invalid
	ErrorMethodNotAllowed            // the method is not allowed for the resource
	ErrorCreationFailed              // object creation failed (often a name collision)
	ErrorNotFound                    // the object or resource was not found
	ErrorForbidden                   // authorization error
	ErrorBadRequest                  // the request was malformed
	ErrorServerError                 // SAP server error (HTTP 500)
	// ErrorObjectLockedInTransport is a refinement of ErrorLockConflict: the
	// object is registered in another open CTS request (a "locked in request
	// <TR>" 409), a different lock domain from the runtime ENQUEUE. The blocking
	// request is recoverable via ADTError.LockingTransport(); retargeting the
	// write at it typically succeeds. See mcp-server-abap#442.
	ErrorObjectLockedInTransport
)

// String returns a stable, lowercase identifier for the kind (handy for logs
// and for keying hint tables). It is not localised.
func (k ErrorKind) String() string {
	switch k {
	case ErrorAlreadyExists:
		return "already_exists"
	case ErrorLocked:
		return "locked"
	case ErrorLockConflict:
		return "lock_conflict"
	case ErrorInvalidLockHandle:
		return "invalid_lock_handle"
	case ErrorEtagMismatch:
		return "etag_mismatch"
	case ErrorNotAcceptable:
		return "not_acceptable"
	case ErrorUnsupportedMedia:
		return "unsupported_media"
	case ErrorUnprocessable:
		return "unprocessable"
	case ErrorMethodNotAllowed:
		return "method_not_allowed"
	case ErrorCreationFailed:
		return "creation_failed"
	case ErrorNotFound:
		return "not_found"
	case ErrorForbidden:
		return "forbidden"
	case ErrorBadRequest:
		return "bad_request"
	case ErrorServerError:
		return "server_error"
	case ErrorObjectLockedInTransport:
		return "object_locked_in_transport"
	default:
		return "unknown"
	}
}

// ClassifyError maps an error to an ErrorKind. Non-ADTError errors (and nil)
// classify as ErrorUnknown. For an *ADTError, the SAP-stable Type is consulted
// first (it is language- and status-independent); when the Type is empty or
// unrecognised, the HTTP status code is used as a fallback.
//
// Note: a bare 423 with no Type classifies as ErrorLocked here. That is
// deliberately distinct from the internal invalid-lock-handle retry heuristic,
// which treats a typeless 423 as an invalid lock handle for SetSource's
// delivery-mechanism fallback.
func ClassifyError(err error) ErrorKind {
	var adtErr *ADTError
	if !errors.As(err, &adtErr) {
		return ErrorUnknown
	}
	if k := classifyByExceptionType(adtErr.Type); k != ErrorUnknown {
		return refineLockConflict(adtErr, k)
	}
	return refineLockConflict(adtErr, classifyByStatusCode(adtErr.StatusCode))
}

// refineLockConflict promotes a generic ErrorLockConflict to
// ErrorObjectLockedInTransport when the message names a CTS request — the
// object is registered in another open request (a different lock domain from
// the runtime ENQUEUE), which the caller can recover from by retargeting the
// write at that request. Any other kind is returned unchanged.
func refineLockConflict(adtErr *ADTError, k ErrorKind) ErrorKind {
	if k != ErrorLockConflict {
		return k
	}
	if _, ok := adtErr.LockingTransport(); ok {
		return ErrorObjectLockedInTransport
	}
	return k
}

// classifyByExceptionType maps a SAP <exc:exception> Type id to a kind, or
// ErrorUnknown if the Type is empty or not recognised.
func classifyByExceptionType(excType string) ErrorKind {
	switch excType {
	case ExceptionTypeResourceAlreadyExists:
		return ErrorAlreadyExists
	case ExceptionTypeResourceLocked:
		return ErrorLocked
	case ExceptionTypeResourceLockConflict:
		return ErrorLockConflict
	case ExceptionTypeResourceInvalidLockHandle:
		return ErrorInvalidLockHandle
	case ExceptionTypeResourceInvalidEtag, ExceptionTypePreconditionFailed:
		return ErrorEtagMismatch
	case ExceptionTypeResourceNotAcceptable:
		return ErrorNotAcceptable
	case ExceptionTypeUnsupportedMediaType:
		return ErrorUnsupportedMedia
	case ExceptionTypeUnprocessableEntity, ExceptionTypeResourceWrongData:
		return ErrorUnprocessable
	case ExceptionTypeNotAllowed:
		return ErrorMethodNotAllowed
	case ExceptionTypeResourceCreationFailure:
		return ErrorCreationFailed
	default:
		return ErrorUnknown
	}
}

// classifyByStatusCode maps an HTTP status code to a kind, or ErrorUnknown for
// codes with no specific classification.
func classifyByStatusCode(status int) ErrorKind {
	switch status {
	case 423:
		return ErrorLocked
	case 412:
		return ErrorEtagMismatch
	case 409:
		return ErrorLockConflict
	case 406:
		return ErrorNotAcceptable
	case 415:
		return ErrorUnsupportedMedia
	case 422:
		return ErrorUnprocessable
	case 405:
		return ErrorMethodNotAllowed
	case 404:
		return ErrorNotFound
	case 403:
		return ErrorForbidden
	case 400:
		return ErrorBadRequest
	case 500:
		return ErrorServerError
	default:
		return ErrorUnknown
	}
}
