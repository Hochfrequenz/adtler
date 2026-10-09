//go:build integration

package adt_test

import "testing"

// TestRedact guards the redaction helper used by the integration tests. It is a
// pure-function test and does not contact SAP.
func TestRedact(t *testing.T) {
	const (
		host = "sap.example.com:44300"
		name = "/ABC/CL_EXAMPLE"
	)
	tests := []struct {
		name string
		text string
		host string
		obj  string
		want string
	}{
		{"literal upper", "object /ABC/CL_EXAMPLE missing", host, name, "object <name> missing"},
		{"literal lower", "object /abc/cl_example missing", host, name, "object <name> missing"},
		{"encoded lower", "x %2fabc%2fcl_example y", host, name, "x <name> y"},
		{"encoded upper", "x %2FABC%2FCL_EXAMPLE y", host, name, "x <name> y"},
		{
			"transport error",
			`Get "https://sap.example.com:44300/sap/bc/adt/oo/classes/%2fabc%2fcl_example/source/main": dial tcp: i/o timeout`,
			host, name,
			`Get "https://<host>/sap/bc/adt/oo/classes/<name>/source/main": dial tcp: i/o timeout`,
		},
		{"empty name", "nothing /ABC/CL_EXAMPLE here", host, "", "nothing /ABC/CL_EXAMPLE here"},
		{"regexp metacharacters are literal", "Z.X and ZAX", "", "Z.X", "<name> and ZAX"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := redact(tc.text, tc.host, tc.obj); got != tc.want {
				t.Errorf("redact() = %q, want %q", got, tc.want)
			}
		})
	}
}
