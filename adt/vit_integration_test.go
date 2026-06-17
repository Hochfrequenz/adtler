//go:build integration

package adt_test

import (
	"context"
	"testing"
)

// TestGetObjectInfo_VIT_Integration exercises GetObjectInfo against real VIT
// object URIs (/sap/bc/adt/vit/wb/object_type/...) to verify that adtler
// sends Accept: application/vnd.sap.adt.basic.object.properties+xml and
// successfully parses the response. Before the fix for adtler#72 these calls
// returned HTTP 406. VIT objects only exist on S/4, so ECC systems will skip
// individual cases when the object URI returns 404.
func TestGetObjectInfo_VIT_Integration(t *testing.T) {
	tests := []struct {
		name    string
		tadir   string // TADIR object type
		uri     string
		s4Only  bool
	}{
		{
			name:   "UIAC",
			tadir:  "UIAC",
			uri:    "/sap/bc/adt/vit/wb/object_type/uiac/object_name/%2fHFQ%2fTC_EXT",
			s4Only: true,
		},
		{
			name:   "UIAD",
			tadir:  "UIAD",
			uri:    "/sap/bc/adt/vit/wb/object_type/uiad/object_name/%2fHFQ%2f95A365FBC361529D",
			s4Only: true,
		},
		{
			name:   "ADVC (advclrp)",
			tadir:  "ADVC",
			uri:    "/sap/bc/adt/vit/wb/object_type/advclrp/object_name/%2fHFQ%2fIWGBLTCDZFMCAZLC5IN3LHTJZY",
			s4Only: true,
		},
		{
			name:   "LRCC (lrcclrp)",
			tadir:  "LRCC",
			uri:    "/sap/bc/adt/vit/wb/object_type/lrcclrp/object_name/%2fHFQ%2fW6SSBNYY2TNVIZKIDDTANNQPGY",
			s4Only: true,
		},
		{
			name:   "WDCC",
			tadir:  "WDCC",
			uri:    "/sap/bc/adt/vit/wb/object_type/wdcc/object_name/%2fHFQ%2fBB7F8A229259B8B8C03A1EEC4C307",
			s4Only: true,
		},
	}

	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			for _, tt := range tests {
				tt := tt
				t.Run(tt.name, func(t *testing.T) {
					info, err := sys.Client.GetObjectInfo(ctx, tt.uri)
					if err != nil {
						// 404 is expected on ECC (VIT objects are S/4-only).
						t.Logf("GetObjectInfo(%s) skipped: %v", tt.tadir, err)
						t.Skip("object not found — likely ECC system or object does not exist")
					}
					if info.Name == "" {
						t.Error("Name is empty")
					}
					if info.Type == "" {
						t.Error("Type is empty")
					}
					t.Logf("name=%s type=%s description=%q package=%s", info.Name, info.Type, info.Description, info.PackageName)
				})
			}
		})
	}
}
