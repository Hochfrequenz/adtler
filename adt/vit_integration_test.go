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
// there is none. VIT objects only exist on S/4, so ECC systems usually skip.
// Only a 404 on the discovered object is a skip; every other error, notably
// the 406 this test guards against, fails the sub-test. A missing handler
// (exception ID uriMappingError) counts as absent, like a 404.
func TestGetObjectInfo_VIT_Integration(t *testing.T) {
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
							t.Skipf("%s is not served as a VIT object on this system (HTTP %d)", tt.tadir, adtErr.StatusCode)
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
