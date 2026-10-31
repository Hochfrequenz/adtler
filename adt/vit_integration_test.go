//go:build integration

package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// uriMappingErrorType is the exception ID SAP raises when no ADT handler is
// registered for a URI. Like a 404 it says the path does not exist on this
// release (ECC has no VIT endpoint at all), unlike the 406 this test guards.
const uriMappingErrorType = "uriMappingError"

// TestGetObjectInfo_VIT_Integration exercises GetObjectInfo against the URIs
// of Fiori and VIT objects to verify that adtler sends an Accept header the
// endpoint produces and parses the response. Before the fix for adtler#72 the
// VIT calls (/sap/bc/adt/vit/wb/object_type/...) returned HTTP 406.
//
// The test does not name any object: for each TADIR type it asks the system
// for one existing object through the data preview and skips the sub-test when
// there is none. Most of these types only exist on S/4, so on ECC most
// sub-tests skip, for one of two reasons: the system has no object of the
// type, or it has no handler for the URI (exception ID uriMappingError).
// Without S4CORE a 404 on the discovered object or a uriMappingError is a
// skip. On S/4 both fail the sub-test: every type here is served there and
// the object comes from TADIR, so either one means a wrong URI, a wrong
// segment or a wrongly encoded name (#217). Every other error, notably the
// 406 this test guards against, fails on every system.
//
// The 406 guard for adtler#72 fires only when both layers break together: the
// VIT Accept mapping (adt/repository.go, vitObjectPropertiesContentType) and
// readWithAcceptFallback's */* retry each mask a failure of the other. The
// same holds for UIAD and its fiori/uiad entry in objectTypeAcceptHeaders.
func TestGetObjectInfo_VIT_Integration(t *testing.T) {
	const vitPrefix = "/sap/bc/adt/vit/wb/object_type/"

	// The VIT object_type segment is the ADT type of the object, main type and
	// subtype run together in lower case: ADVC/LRP is advclrp, WDCC/YG is
	// wdccyg. The bare TADIR type is unmapped for WDCC (uriMappingError, #217).
	//
	// UIAD is not served through VIT: uiad is unmapped, and uiadtyp (for
	// UIAD/TYP) is mapped but fails in SAP's own serialization (400,
	// ExceptionInvalidData). ADT publishes app descriptor items under
	// /sap/bc/adt/fiori/uiad instead, which is what the ADT search returns as
	// their URI. Measured on SAP S/4HANA 2025, on-premise (SAP_BASIS 816).
	tests := []struct {
		tadir  string // TADIR object type
		prefix string // URI up to the object name
	}{
		{tadir: "UIAC", prefix: vitPrefix + "uiac/object_name/"},
		{tadir: "UIAD", prefix: "/sap/bc/adt/fiori/uiad/"},
		{tadir: "ADVC", prefix: vitPrefix + "advclrp/object_name/"},
		{tadir: "LRCC", prefix: vitPrefix + "lrcclrp/object_name/"},
		{tadir: "WDCC", prefix: vitPrefix + "wdccyg/object_name/"},
	}

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			res, err := sys.Client.RunQuery(ctx, "SELECT component FROM cvers WHERE component = 'S4CORE'", 1)
			if err != nil {
				t.Fatalf("CVERS query for S4CORE failed: %v", err)
			}
			isS4 := len(res.Rows) > 0
			for _, tt := range tests {
				tt := tt
				t.Run(tt.tadir, func(t *testing.T) {
					// Namespaced objects only: the test covers the %2F-encoded
					// object name in the URI, which a name without a namespace
					// would not exercise.
					sql := "SELECT obj_name FROM tadir WHERE pgmid = 'R3TR' AND object = '" +
						tt.tadir + "' AND delflag = ' ' AND obj_name LIKE '/%' ORDER BY obj_name"
					res, err := sys.Client.RunQuery(ctx, sql, 1)
					if err != nil {
						t.Fatalf("discovery query for %s failed: %v", tt.tadir, err)
					}
					if len(res.Rows) == 0 || len(res.Rows[0]) == 0 || strings.TrimSpace(res.Rows[0][0]) == "" {
						t.Skipf("no %s object on this system", tt.tadir)
					}
					// The TADIR name is used whole. A WDCC name is the
					// configuration ID padded to 32 characters followed by the
					// two-character configuration type, so it can carry inner
					// blanks, which PathEscape encodes along with the slashes.
					name := strings.TrimSpace(res.Rows[0][0])
					uri := tt.prefix + url.PathEscape(name)

					info, err := sys.Client.GetObjectInfo(ctx, uri)
					if err != nil {
						var adtErr *adt.ADTError
						if errors.As(err, &adtErr) && (adtErr.StatusCode == http.StatusNotFound ||
							adtErr.Type == uriMappingErrorType) {
							if isS4 {
								t.Fatalf("%s: object from TADIR not reachable on S/4: HTTP %d (%s)",
									tt.tadir, adtErr.StatusCode, adtErr.Type)
							}
							if adtErr.Type == uriMappingErrorType {
								t.Skipf("%s: ADT has no handler for this URI (%s)", tt.tadir, uriMappingErrorType)
							}
							t.Skipf("%s object not found (HTTP %d)", tt.tadir, adtErr.StatusCode)
						}
						// The error text echoes the URI, and with it the object name.
						if adtErr != nil {
							t.Fatalf("GetObjectInfo for %s: HTTP %d (%s)", tt.tadir, adtErr.StatusCode, adtErr.Type)
						}
						t.Fatalf("GetObjectInfo for %s failed: %T", tt.tadir, err)
					}
					if info.Name == "" {
						t.Error("Name is empty")
					}
					if info.Type == "" {
						t.Error("Type is empty")
					}
					// VIT answers 200 for a name that does not exist, echoing
					// the name and type back without a package. Only the
					// package shows that SAP found the object.
					if info.PackageName == "" {
						t.Error("PackageName is empty: SAP echoed the URI instead of finding the object")
					}
					t.Logf("type=%s nameSet=%t typeSet=%t packageSet=%t",
						tt.tadir, info.Name != "", info.Type != "", info.PackageName != "")
				})
			}
		})
	}
}
