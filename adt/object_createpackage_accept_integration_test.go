//go:build integration

package adt_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// endpointUnavailable reports whether CreatePackage refused because the
// /sap/bc/adt/packages endpoint does not exist on this release. CreatePackage
// answers a 404 with its own guidance string rather than an ADTError, so this
// is a substring match on that fixed prefix and not on a server message.
func endpointUnavailable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "endpoint is not available on this SAP system")
}

// TestCreatePackage_AcceptHeaderReachesPayloadValidation_MultiSystem_Integration
// is the live regression test for adtler#149: CreatePackage sent only a
// Content-Type, so S/4 rejected the POST at the HTTP layer with 400
// ExceptionResourceBadRequest ("Accept header missing") and package creation
// could never succeed there. Measured on SAP S/4HANA on-premise (SAP_BASIS 816,
// S4CORE 109) on 2026-09-21; ECC tolerates the omission, which is why a
// single-system run could not see the bug and this test parametrizes over both.
//
// # Why this test creates nothing
//
// A package cannot be deleted over ADT — the ETag read for the delete belongs
// to a different representation than the one SAP compares, so the request comes
// back 412 (adtler#150). A test that created a package per run would therefore
// strand one on every system, every run, permanently. Instead it sends a
// request SAP is certain to reject on *data* grounds, with adtcore:responsible
// deliberately empty: that same system answers 400 ExceptionInvalidData ("Check
// of condition failed") for it.
//
// That rejection is the whole point. Payload validation happens only after the
// request has been accepted at the HTTP layer, so *reaching* it proves the
// Accept header was satisfied. Before the fix the call never got that far and
// came back ExceptionResourceBadRequest instead.
//
// The assertion is on the exception ID rather than the message text, because
// SAP translates its messages but not the IDs.
func TestCreatePackage_AcceptHeaderReachesPayloadValidation_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()

	// Local ($-prefixed) so no transport, software component or transport
	// layer is involved — none of those are the subject here, and requiring
	// them would make the probe release- and landscape-specific.
	const probeName = "$Z_ADTLER_149_PROBE"

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			err := sys.Client.CreatePackage(ctx, probeName,
				"adtler#149 Accept header probe", "", "", "", "")

			if endpointUnavailable(err) {
				t.Skipf("[%s] /sap/bc/adt/packages is not available on this release — "+
					"the Accept header cannot be exercised here", sys.Name)
			}

			if err == nil {
				// Not expected: an empty adtcore:responsible is rejected on
				// every system measured so far. If some release accepts it,
				// the package now exists and adtler#150 means it cannot be
				// removed again, so name it for whoever picks this up.
				t.Fatalf("[%s] CreatePackage succeeded with an empty responsible — "+
					"the probe relied on that being rejected, so %s now exists on this "+
					"system and cannot be deleted over ADT (adtler#150). Rework the probe "+
					"to use a rejection this release does enforce", sys.Name, probeName)
			}

			var adtErr *adt.ADTError
			if !errors.As(err, &adtErr) {
				t.Fatalf("[%s] CreatePackage returned a non-ADT error, so the request "+
					"never reached SAP's payload validation: %v", sys.Name, err)
			}

			if adtErr.Type == "ExceptionResourceBadRequest" {
				t.Fatalf("[%s] CreatePackage was rejected at the HTTP layer with %s: %q — "+
					"this is exactly the adtler#149 failure, the request carried no "+
					"acceptable Accept header and never reached payload validation",
					sys.Name, adtErr.Type, adtErr.Message)
			}

			t.Logf("[%s] rejected on data grounds with %s (HTTP %d), so the Accept "+
				"header was accepted and the request reached payload validation",
				sys.Name, adtErr.Type, adtErr.StatusCode)
		})
	}
}
