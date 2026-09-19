package adt

import "testing"

// TestObjectURIMatches exercises the URI-matching helper ActivateObjects
// uses to decide whether a GetInactiveObjects entry refers to a requested
// object. An earlier version used a case-sensitive, unidirectional prefix
// check that failed open (silently degrading to "not still inactive," never
// a false positive) on every case below except the sibling-name one.
func TestObjectURIMatches(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"

	cases := []struct {
		name      string
		requested string
		candidate string
		want      bool
	}{
		{
			name:      "exact match",
			requested: objectURI,
			candidate: objectURI,
			want:      true,
		},
		{
			name:      "candidate is an include nested under the requested object",
			requested: objectURI,
			candidate: objectURI + "/source/main",
			want:      true,
		},
		{
			name:      "requested is an include, candidate is the parent object (aibap.mcp#500's own repro passed includes/*)",
			requested: objectURI + "/includes/definitions",
			candidate: objectURI,
			want:      true,
		},
		{
			name:      "sibling object name must not match (CL_EXAMPLE vs CL_EXAMPLE2)",
			requested: objectURI,
			candidate: objectURI + "2",
			want:      false,
		},
		{
			name:      "unrelated object",
			requested: objectURI,
			candidate: "/sap/bc/adt/programs/programs/ZTEST",
			want:      false,
		},
		{
			name:      "case-different object name",
			requested: objectURI,
			candidate: "/sap/bc/adt/oo/classes/%2fabc%2fCL_EXAMPLE",
			want:      true,
		},
		{
			name:      "case-different percent-encoding hex digits",
			requested: objectURI,
			candidate: "/sap/bc/adt/oo/classes/%2Fabc%2Fcl_example",
			want:      true,
		},
		{
			name:      "trailing slash on the candidate",
			requested: objectURI,
			candidate: objectURI + "/",
			want:      true,
		},
		{
			name:      "trailing slash on the requested URI",
			requested: objectURI + "/",
			candidate: objectURI,
			want:      true,
		},
		{
			name:      "candidate carries a query string",
			requested: objectURI,
			candidate: objectURI + "?version=inactive",
			want:      true,
		},
		{
			name:      "candidate carries a fragment",
			requested: objectURI,
			candidate: objectURI + "/source/main#start=5,0",
			want:      true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := objectURIMatches(tc.requested, tc.candidate)
			if got != tc.want {
				t.Errorf("objectURIMatches(%q, %q) = %v, want %v", tc.requested, tc.candidate, got, tc.want)
			}
		})
	}
}
