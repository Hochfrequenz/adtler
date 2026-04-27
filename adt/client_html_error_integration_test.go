//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

// TestParseADTError_HTMLBody_MultiSystem_Integration regression-tests
// adtler#13 against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Before the fix, parseADTError dumped the entire SAP "Application Server
// Error" HTML page (page chrome, CSS, embedded base64 PNG, several KB) into
// ADTError.Message whenever SAP returned a 5xx with text/html. The original
// trigger was activate_object on a non-existent program against R/3 — see
// mcp-server-abap#292.
//
// This test deliberately triggers an error path on each system by activating
// a program that doesn't exist. Per system:
//
//   - The call may succeed (e.g. S/4 returns a structured "Errors occurred
//     during generation" response with Success=false) or fail with an
//     error. Either is acceptable — what matters is the SHAPE of any error.
//   - If an error is returned, its message MUST NOT contain HTML chrome
//     markers (DOCTYPE, <style>, base64 PNG signatures) and MUST stay
//     under 2KB. A real SAP HTML error page is ~6KB; the parsed summary
//     is under 200 bytes. The 2KB cap leaves comfortable headroom for
//     legitimate XML error envelopes while catching any HTML leakage.
//   - If an error is returned, it should still be informative — at minimum
//     non-empty, and ideally containing a status code or recognisable SAP
//     error fragment.
func TestParseADTError_HTMLBody_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	const fakeURI = "/sap/bc/adt/programs/programs/z_adt_mcp_html_err_xxx"

	htmlMarkers := []string{
		"<!DOCTYPE",
		"<!doctype",
		"<style>",
		"<style ",
		"font-family",
		"iVBORw0",         // base64 PNG signature SAP embeds in the error page
		"errorTextHeader", // raw class name leaking through means parsing failed
	}

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			result, err := sys.Client.ActivateObjects(ctx, []string{fakeURI})

			// On systems that return a structured response (e.g. S/4), the
			// call succeeds with Success=false. That's a legitimate path and
			// has nothing to test here for the HTML-parser fix.
			if err == nil {
				t.Logf("[%s] structured response: success=%v messages=%d",
					sys.Name, result.Success, len(result.Messages))
				return
			}

			msg := err.Error()
			t.Logf("[%s] error (%d bytes): %v", sys.Name, len(msg), err)

			// Length cap: real SAP HTML error page is several KB; parsed
			// summary is well under 200 bytes; legitimate XML envelope errors
			// are typically a few hundred bytes. 2KB is a comfortable cap
			// that catches HTML leakage.
			if len(msg) > 2048 {
				t.Errorf("[%s] error message is %d bytes (>2048) — HTML body likely leaking through parseADTError",
					sys.Name, len(msg))
			}

			// HTML chrome markers: any of these in the propagated error
			// indicates the layer-2 HTML detection in parseADTError failed.
			for _, marker := range htmlMarkers {
				if strings.Contains(msg, marker) {
					t.Errorf("[%s] error message contains HTML chrome marker %q — parseADTError HTML detection regressed",
						sys.Name, marker)
				}
			}

			// Sanity: error must be non-empty and informative.
			if strings.TrimSpace(msg) == "" {
				t.Errorf("[%s] error message is empty", sys.Name)
			}
		})
	}
}
