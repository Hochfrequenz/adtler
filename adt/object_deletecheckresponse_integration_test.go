//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestDeleteObject_NonExistentMessageQuality_MultiSystem_Integration
// regression-tests adtler#19 against every system in the
// SAP_INTEGRATION_SYSTEMS whitelist.
//
// Before the fix, DeleteObject's ETag-fetch GET path called doRead() and
// then read the ETag header without first calling checkResponse(). When
// SAP returned a 4xx (e.g. 404 for a non-existent object), the call flowed
// through as a "successful" response, found no ETag header, and surfaced
// the cryptic "DeleteObject: no ETag returned for <uri>" instead of the
// real SAP error message.
//
// This test asks each system to delete an object that doesn't exist and
// asserts the resulting error:
//
//   - MUST be non-nil (the call obviously can't succeed).
//   - MUST NOT contain "no ETag returned" — that's the cryptic-message
//     anti-pattern the fix removed.
//   - SHOULD contain something resembling a SAP exception (e.g. the URI,
//     a status code, "not exist", "ResourceNotFound", "wrong input data").
//     We assert one of these substrings to confirm the propagated error
//     came from parseADTError, not the legacy stub.
func TestDeleteObject_NonExistentMessageQuality_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	// A pseudo-random program name that is extremely unlikely to exist on
	// any test system. The trailing digits are intentionally fixed so
	// repeated runs hit the same URL.
	const fakeURI = "/sap/bc/adt/programs/programs/z_adt_mcp_delete_19_404_xxx"

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			err := sys.Client.DeleteObject(ctx, fakeURI, "", "")
			if err == nil {
				t.Fatalf("[%s] expected error deleting non-existent object %s", sys.Name, fakeURI)
			}
			msg := err.Error()
			t.Logf("[%s] error: %v", sys.Name, err)

			if strings.Contains(msg, "no ETag returned") {
				t.Errorf("[%s] cryptic 'no ETag returned' message leaked through; "+
					"checkResponse() must run before reading ETag header. error: %v", sys.Name, err)
			}

			// At least one of these must appear in the propagated error to
			// prove parseADTError actually parsed the SAP body. We accept a
			// loose set because the exact wording differs by SAP release
			// (German on R/3, English on S/4) and by exception type
			// (ResourceNotFound, ResourceNoAccess, etc).
			expectedFragments := []string{
				"ResourceNotFound",
				"does not exist",
				"existiert nicht",
				"404",
				"wrong input data", // (CLAS path falls into this)
			}
			matched := false
			for _, frag := range expectedFragments {
				if strings.Contains(msg, frag) {
					matched = true
					break
				}
			}
			if !matched {
				t.Errorf("[%s] error doesn't look like a propagated SAP exception "+
					"(expected one of %v in the message). error: %v",
					sys.Name, expectedFragments, err)
			}
		})
	}
}
