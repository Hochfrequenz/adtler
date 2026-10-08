//go:build integration

package adt_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// uriMappingErrorType is the exception ID SAP raises when no ADT handler is
// registered for a URI. Like a 404 it says the path does not exist on this
// release (ECC has no VIT endpoint at all), unlike the 406 this test guards.
const uriMappingErrorType = "uriMappingError"

// TestGetObjectInfo_VIT_Integration exercises GetObjectInfo against real VIT
// object URIs (/sap/bc/adt/vit/wb/object_type/...) to verify that adtler
// sends Accept: application/vnd.sap.adt.basic.object.properties+xml and
// successfully parses the response. Before the fix for adtler#72 these calls
// returned HTTP 406.
//
// The test does not name any object: for each TADIR type it asks the system
// for one existing object through the data preview and skips the sub-test when
// there is none. VIT objects only exist on S/4, so ECC systems skip, for one
// of two reasons: the system has no object of the type, or it has no handler
// for the VIT URI (exception ID uriMappingError).
// Only a 404 on the discovered object or a uriMappingError is a skip; every
// other error, notably the 406 this test guards against, fails the sub-test.
//
// The 406 guard for adtler#72 fires only when both layers break together: the
// VIT Accept mapping (adt/repository.go, vitObjectPropertiesContentType) and
// readWithAcceptFallback's */* retry each mask a failure of the other.
func TestGetObjectInfo_VIT_Integration(t *testing.T) {
	// Known gap: on the S/4 system the test was last measured against, UIAD and
	// WDCC objects exist but ADT has no handler for the segments below
	// (uriMappingError), so both sub-tests skip as "unmapped", not as "no
	// object". UIAC, ADVC and LRCC pass there. On ECC every type skips (no
	// object, or unmapped). The segments for UIAD and WDCC may be wrong; to be
	// tracked in a follow-up issue.
	tests := []struct {
		tadir   string // TADIR object type
		segment string // VIT URI object_type segment
	}{
		{tadir: "UIAC", segment: "uiac"},
		{tadir: "UIAD", segment: "uiad"},
		{tadir: "ADVC", segment: "advclrp"},
		{tadir: "LRCC", segment: "lrcclrp"},
		{tadir: "WDCC", segment: "wdcc"},
	}

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			for _, tt := range tests {
				tt := tt
				t.Run(tt.tadir, func(t *testing.T) {
					sql := "SELECT obj_name FROM tadir WHERE pgmid = 'R3TR' AND object = '" +
						tt.tadir + "' AND delflag = ' ' AND obj_name LIKE '/%' ORDER BY obj_name"
					res, err := sys.Client.RunQuery(ctx, sql, 1)
					if err != nil {
						t.Fatalf("discovery query for %s failed: %v", tt.tadir, err)
					}
					if len(res.Rows) == 0 || len(res.Rows[0]) == 0 || strings.TrimSpace(res.Rows[0][0]) == "" {
						t.Skipf("no %s object on this system", tt.tadir)
					}
					name := strings.TrimSpace(res.Rows[0][0])
					uri := "/sap/bc/adt/vit/wb/object_type/" + tt.segment +
						"/object_name/" + strings.ReplaceAll(name, "/", "%2f")

					info, err := sys.Client.GetObjectInfo(ctx, uri)
					if err != nil {
						var adtErr *adt.ADTError
						if errors.As(err, &adtErr) && (adtErr.StatusCode == http.StatusNotFound ||
							adtErr.Type == uriMappingErrorType) {
							if adtErr.Type == uriMappingErrorType {
								t.Skipf("%s: ADT has no handler for this VIT segment (%s)", tt.tadir, uriMappingErrorType)
							}
							t.Skipf("%s object not found as VIT object (HTTP %d)", tt.tadir, adtErr.StatusCode)
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
					t.Logf("type=%s nameSet=%t typeSet=%t", tt.tadir, info.Name != "", info.Type != "")
				})
			}
		})
	}
}
