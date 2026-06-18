package adt_test

import (
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

func TestObjectURI(t *testing.T) {
	cases := []struct {
		name       string
		objectType string
		objName    string
		want       string
	}{
		{"program", "PROG", "ZFOO", "/sap/bc/adt/programs/programs/zfoo"},
		{"class", "CLAS", "ZCL_BAR", "/sap/bc/adt/oo/classes/zcl_bar"},
		{"interface", "INTF", "ZIF_BAZ", "/sap/bc/adt/oo/interfaces/zif_baz"},
		{"function group", "FUGR", "ZFG", "/sap/bc/adt/functions/groups/zfg"},
		{"data element", "DTEL", "ZDE", "/sap/bc/adt/ddic/dataelements/zde"},
		{"domain", "DOMA", "ZDO", "/sap/bc/adt/ddic/domains/zdo"},
		{"table", "TABL", "ZTAB", "/sap/bc/adt/ddic/tables/ztab"},
		{"ddl source", "DDLS", "ZCDS", "/sap/bc/adt/ddic/ddl/sources/zcds"},
		{"message class", "MSAG", "ZMSG", "/sap/bc/adt/messageclass/zmsg"},
		// Object type is matched case-insensitively, name is lower-cased.
		{"lowercase type", "prog", "ZFOO", "/sap/bc/adt/programs/programs/zfoo"},
		{"already lower name", "PROG", "zfoo", "/sap/bc/adt/programs/programs/zfoo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := adt.ObjectURI(tc.objectType, tc.objName)
			if err != nil {
				t.Fatalf("ObjectURI(%q, %q): unexpected error: %v", tc.objectType, tc.objName, err)
			}
			if got != tc.want {
				t.Errorf("ObjectURI(%q, %q) = %q, want %q", tc.objectType, tc.objName, got, tc.want)
			}
		})
	}
}

func TestObjectURI_UnsupportedType(t *testing.T) {
	got, err := adt.ObjectURI("BOGUS", "ZFOO")
	if err == nil {
		t.Fatalf("ObjectURI(\"BOGUS\", ...) = %q, want error", got)
	}
	if got != "" {
		t.Errorf("ObjectURI returned %q alongside an error, want empty string", got)
	}
	// The error should name the offending type and list supported types so
	// callers (and humans) can recover.
	if !strings.Contains(err.Error(), `"BOGUS"`) {
		t.Errorf("error %q should mention the unsupported type", err.Error())
	}
	for _, want := range []string{"PROG", "CLAS", "MSAG"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should list supported type %q", err.Error(), want)
		}
	}
}
