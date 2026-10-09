package adt

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// verifyCleanupTimeout bounds the removal of VerifySource's temporary program,
// which runs even when the caller's context is already done.
const verifyCleanupTimeout = 30 * time.Second

// VerifySource syntax-checks standalone ABAP source without requiring an
// existing object. It creates a temporary program in the local $TMP package,
// writes the source, runs a syntax check against the inactive version, and
// deletes the temporary program again. valid is true when the check produced
// no error-severity ("E") messages.
//
// SAP's checkruns endpoint does not support checking inline source on ECC or
// S/4 (it ignores the inline body / rejects the action), so this throwaway-$TMP
// round-trip is the portable way to validate free-standing source. See
// mcp-server-abap#126.
//
// If the temporary program cannot be removed again, the returned error says
// so and names it, so the leftover can be deleted by hand. In that case valid
// and messages still carry the result of the syntax check.
func (c *httpClient) VerifySource(ctx context.Context, source string) (valid bool, messages []SyntaxMessage, err error) {
	name := fmt.Sprintf("Z_ADTLER_VERIFY_%06d", rand.Intn(1000000)) //nolint:gosec // throwaway temp object name, not security-sensitive
	objectURI, err := ObjectURI("PROG", name)
	if err != nil {
		return false, nil, fmt.Errorf("VerifySource: %w", err)
	}

	if err := c.CreateObject(ctx, "PROG", name, "$TMP", "adtler VerifySource temp", ""); err != nil {
		return false, nil, fmt.Errorf("VerifySource: create temp object: %w", err)
	}

	// Ensure the temporary program is removed regardless of outcome. The
	// delete takes no lock: the DELETE runs in another SAP session than a
	// LockObject, so on S/4HANA a lock taken here would block it and leave the
	// program behind (adtler#187). A failure is returned, not dropped, because
	// a silent failure leaks one program into $TMP per call.
	//
	// The cleanup does not use the caller's context: a call that timed out or
	// was cancelled is exactly when the program would otherwise stay behind.
	// A lock still held at that point is handed to DeleteObject, which releases
	// it before it deletes.
	var held string
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), verifyCleanupTimeout)
		defer cancel()
		delErr := c.DeleteObject(cleanupCtx, objectURI, held, "")
		if delErr == nil {
			return
		}
		leak := fmt.Errorf("VerifySource: temporary program %s could not be removed from $TMP: %w", name, delErr)
		if err != nil {
			err = fmt.Errorf("%w; additionally: %v", err, leak)
			return
		}
		// The syntax-check result is still valid, so keep returning it next
		// to the error.
		err = leak
	}()

	lockHandle, err := c.LockObject(ctx, objectURI)
	if err != nil {
		return false, nil, fmt.Errorf("VerifySource: lock: %w", err)
	}
	held = lockHandle
	// release lets go of the lock. Only a release that went through clears
	// held, so the cleanup still releases a lock that could not be released
	// here, for instance because the caller's context is done.
	release := func() {
		if err := c.UnlockObject(ctx, objectURI, lockHandle); err == nil {
			held = ""
		}
	}
	src, err := c.GetSource(ctx, objectURI)
	if err != nil {
		release()
		return false, nil, fmt.Errorf("VerifySource: get source for etag: %w", err)
	}
	if _, err := c.SetSource(ctx, objectURI, source, lockHandle, "", src.ETag); err != nil {
		release()
		return false, nil, fmt.Errorf("VerifySource: set source: %w", err)
	}
	release()

	messages, err = c.SyntaxCheck(ctx, objectURI)
	if err != nil {
		return false, nil, fmt.Errorf("VerifySource: syntax check: %w", err)
	}

	valid = true
	for _, m := range messages {
		if m.Type == "E" {
			valid = false
			break
		}
	}
	return valid, messages, nil
}
