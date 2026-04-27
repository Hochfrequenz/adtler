package adt

import "testing"

// TestExtractRuntimeFields_CaseInsensitive regression-tests adtler#7:
// the S/4 ATOM feed uses category labels that don't match the exact casing
// the original parser expected. Case-insensitive matching catches all known
// variants (German "Laufzeitfehler"/"Programm", English "Runtime Error"/
// "Runtime error"/"Program", and the underscore variant "Runtime_Error").
func TestExtractRuntimeFields_CaseInsensitive(t *testing.T) {
	cases := []struct {
		name       string
		categories []struct {
			Term  string `xml:"term,attr"`
			Label string `xml:"label,attr"`
		}
		wantError   string
		wantProgram string
	}{
		{
			name: "German_R3",
			categories: []struct {
				Term  string `xml:"term,attr"`
				Label string `xml:"label,attr"`
			}{
				{Term: "UNCAUGHT_EXCEPTION", Label: "Laufzeitfehler"},
				{Term: "CL_FOO=====CP", Label: "ABAP: Programm"},
			},
			wantError:   "UNCAUGHT_EXCEPTION",
			wantProgram: "CL_FOO=====CP",
		},
		{
			name: "English_uppercase",
			categories: []struct {
				Term  string `xml:"term,attr"`
				Label string `xml:"label,attr"`
			}{
				{Term: "MESSAGE_TYPE_X", Label: "Runtime Error"},
				{Term: "SAPMV45A", Label: "Program"},
			},
			wantError:   "MESSAGE_TYPE_X",
			wantProgram: "SAPMV45A",
		},
		{
			name: "English_lowercase",
			categories: []struct {
				Term  string `xml:"term,attr"`
				Label string `xml:"label,attr"`
			}{
				{Term: "DYNPRO_MSG_IN_HELP", Label: "Runtime error"},
				{Term: "SAPLFOO", Label: "ABAP program"},
			},
			wantError:   "DYNPRO_MSG_IN_HELP",
			wantProgram: "SAPLFOO",
		},
		{
			name: "mixed_case_S4",
			categories: []struct {
				Term  string `xml:"term,attr"`
				Label string `xml:"label,attr"`
			}{
				{Term: "COMPUTE_INT_ZERODIVIDE", Label: "runtime error type"},
				{Term: "ZCL_TEST====CP", Label: "ABAP: Program"},
			},
			wantError:   "COMPUTE_INT_ZERODIVIDE",
			wantProgram: "ZCL_TEST====CP",
		},
		{
			name: "no_matching_labels",
			categories: []struct {
				Term  string `xml:"term,attr"`
				Label string `xml:"label,attr"`
			}{
				{Term: "UNCAUGHT_EXCEPTION", Label: "Category"},
				{Term: "CL_FOO", Label: "Exception"},
			},
			wantError:   "",
			wantProgram: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var h ShortDumpHeader
			extractRuntimeFields(&h, tc.categories)
			if h.RuntimeError != tc.wantError {
				t.Errorf("RuntimeError: got %q, want %q", h.RuntimeError, tc.wantError)
			}
			if h.Program != tc.wantProgram {
				t.Errorf("Program: got %q, want %q", h.Program, tc.wantProgram)
			}
		})
	}
}
