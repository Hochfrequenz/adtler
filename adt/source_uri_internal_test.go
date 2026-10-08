package adt

import "testing"

// TestBareObjectURI pins which inputs bareObjectURI reduces to the bare
// object URI and which it must leave alone (adtler#210).
func TestBareObjectURI(t *testing.T) {
	const prog = "/sap/bc/adt/programs/programs/ztest"
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare", prog, prog},
		{"bare with trailing slash", prog + "/", prog},
		{"source suffix", prog + "/source/main", prog},
		{"source suffix with trailing slash", prog + "/source/main/", prog},
		{"upper-case source suffix", prog + "/SOURCE/MAIN", prog},
		{"source suffix with position fragment", prog + "/source/main#start=42,5", prog},
		{"bare with fragment", prog + "#start=1,0", prog},
		// No query form ever worked here (every entry point appends a
		// sub-path after the input), so a query is dropped.
		{"source suffix with query", prog + "/source/main?version=inactive", prog},
		{"function group include", "/sap/bc/adt/programs/includes/zinclude/source/main", "/sap/bc/adt/programs/includes/zinclude"},
		{"function module", "/sap/bc/adt/functions/groups/zfg/fmodules/zfm/source/main", "/sap/bc/adt/functions/groups/zfg/fmodules/zfm"},
		{"name ending in source is not a suffix", "/sap/bc/adt/programs/programs/zsource", "/sap/bc/adt/programs/programs/zsource"},
		{"longer last segment is not a suffix", prog + "/source/mainx", prog + "/source/mainx"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bareObjectURI(tt.in); got != tt.want {
				t.Errorf("bareObjectURI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
