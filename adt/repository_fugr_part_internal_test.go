package adt

import "testing"

// TestFunctionGroupURIOfPart pins which URIs GetObjectInfo treats as a part of
// a function group whose package comes from the group (adtler#169, #232): only
// a function module or a function group include directly below the group, never
// the group itself or any other path under it.
func TestFunctionGroupURIOfPart(t *testing.T) {
	const group = "/sap/bc/adt/functions/groups/zmy_fg"
	tests := []struct {
		name      string
		uri       string
		wantGroup string
		wantOK    bool
	}{
		{"function module", group + "/fmodules/zmy_fm", group, true},
		{"include", group + "/includes/lzmy_fgtop", group, true},
		{"include with an encoded name", group + "/includes/lzmy_fg%2401", group, true},
		{"include with a fragment", group + "/includes/lzmy_fgf01#start=10,0", group, true},
		{"namespaced include", "/sap/bc/adt/functions/groups/%2fabc%2ffg/includes/%2fabc%2flfgtop",
			"/sap/bc/adt/functions/groups/%2fabc%2ffg", true},
		{"the group itself", group, "", false},
		{"the group with a trailing slash", group + "/", "", false},
		{"the group's source", group + "/source/main", "", false},
		{"the group's object structure", group + "/objectstructure", "", false},
		{"a segment that only starts like includes", group + "/includesx/y", "", false},
		{"no group name", "/sap/bc/adt/functions/groups//includes/x", "", false},
		{"a class include", "/sap/bc/adt/oo/classes/zcl_x/includes/testclasses", "", false},
		{"a program", "/sap/bc/adt/programs/programs/zprog", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotGroup, gotOK := functionGroupURIOfPart(tt.uri)
			if gotGroup != tt.wantGroup || gotOK != tt.wantOK {
				t.Errorf("functionGroupURIOfPart(%q) = (%q, %t), want (%q, %t)", tt.uri, gotGroup, gotOK, tt.wantGroup, tt.wantOK)
			}
		})
	}
}
