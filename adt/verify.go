package adt

import (
	"context"
	"fmt"
	"math/rand"
)

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
func (c *httpClient) VerifySource(ctx context.Context, source string) (valid bool, messages []SyntaxMessage, err error) {
	name := fmt.Sprintf("Z_ADTLER_VERIFY_%06d", rand.Intn(1000000)) //nolint:gosec // throwaway temp object name, not security-sensitive
	objectURI, err := ObjectURI("PROG", name)
	if err != nil {
		return false, nil, fmt.Errorf("VerifySource: %w", err)
	}

	if err := c.CreateObject(ctx, "PROG", name, "$TMP", "adtler VerifySource temp", ""); err != nil {
		return false, nil, fmt.Errorf("VerifySource: create temp object: %w", err)
	}

	// Ensure the temporary program is removed regardless of outcome.
	defer func() {
		if lh, lockErr := c.LockObject(ctx, objectURI); lockErr == nil {
			_ = c.DeleteObject(ctx, objectURI, lh, "")
		}
	}()

	lockHandle, err := c.LockObject(ctx, objectURI)
	if err != nil {
		return false, nil, fmt.Errorf("VerifySource: lock: %w", err)
	}
	src, err := c.GetSource(ctx, objectURI)
	if err != nil {
		_ = c.UnlockObject(ctx, objectURI, lockHandle)
		return false, nil, fmt.Errorf("VerifySource: get source for etag: %w", err)
	}
	if _, err := c.SetSource(ctx, objectURI, source, lockHandle, "", src.ETag); err != nil {
		_ = c.UnlockObject(ctx, objectURI, lockHandle)
		return false, nil, fmt.Errorf("VerifySource: set source: %w", err)
	}
	_ = c.UnlockObject(ctx, objectURI, lockHandle)

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
