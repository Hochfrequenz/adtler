package main

import (
	"strings"
	"testing"
)

func TestScan(t *testing.T) {
	foreignNumber := "QQQ" + "K9" + "00001"
	foreignNS := "/" + "QQQ" + "/CL_THING"
	foreignEncoded := "%2f" + "qqq" + "%2fcl_thing"
	cases := []struct {
		name  string
		line  string
		kinds int
	}{
		{"placeholder transport", "request AAAK900001 of user USERA", 0},
		{"foreign transport", "request " + foreignNumber, 1},
		{"foreign K000000 number", "QQQ" + "K000000", 1},
		{"placeholder namespace", "class /ABC/CL_EXAMPLE and /XYZ/CL_OTHER", 0},
		{"foreign namespace", "class " + foreignNS, 1},
		{"placeholder encoded", "/sap/bc/adt/oo/classes/%2fabc%2fcl_example", 0},
		{"foreign encoded", "/sap/bc/adt/oo/classes/" + foreignEncoded, 1},
		{"lower-case path", "/sap/bc/adt/oo/classes/zcl_test", 0},
		{"two in one line", foreignNumber + " " + foreignNS, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(scan([]byte(c.line))); got != c.kinds {
				t.Errorf("scan(%q) found %d, want %d", c.line, got, c.kinds)
			}
		})
	}
}

// TestFormat_OmitsValue pins that a printed finding names file, line and
// kind but never the matched value: the CI log is public, so printing the
// value would republish it.
func TestFormat_OmitsValue(t *testing.T) {
	sid := "QQQ"
	findings := scan([]byte("x\nrequest " + sid + "K9" + "00001\n"))
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one", findings)
	}
	out := format("adt/x_test.go", findings[0])
	if !strings.HasPrefix(out, "adt/x_test.go:2: ") {
		t.Errorf("format = %q, want file:line prefix", out)
	}
	if strings.Contains(out, sid) {
		t.Errorf("format = %q contains the matched system ID", out)
	}
}
