// Command datacheck scans the files named on the command line for
// identifiers of internal SAP systems that must not be published in this
// repository: transport and task numbers on a non-placeholder system ID,
// and names in a /X/ namespace outside an allowlist, raw or URL-encoded.
// It prints file, line and kind of each finding, never the matched value,
// so the public CI log does not republish what it found. It cannot detect
// host names, logon IDs, system aliases, or lower-case namespace forms;
// see "Before pushing" in CLAUDE.md.
//
// Usage: git ls-files -z | xargs -0 go run ./tools/datacheck
package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	// transportRe matches the number itself; transportNumbers applies the
	// boundary rules. \b is not usable here: it does not fire between two
	// word characters, so a number after "%2f" or after a client prefix in a
	// lock argument would slip through.
	transportRe = regexp.MustCompile(`([A-Z][A-Z0-9]{2})K[0-9]{6}`)
	namespaceRe = regexp.MustCompile(`/([A-Z][A-Z0-9]{1,9})/[A-Z0-9_]`)
	encodedRe   = regexp.MustCompile(`(?i)%2f([a-z][a-z0-9]{1,9})%2f`)
)

// placeholderSIDs are system IDs that identify no internal system: the
// synthetic IDs of test fixtures.
var placeholderSIDs = map[string]bool{
	"AAA": true, "BBB": true, "CCC": true,
	"NPL": true, // SAP's public demo system ID (adt/registry_test.go)
}

// allowedPrefixes are /X/ prefixes (compared upper-case) that identify
// nobody: the placeholder namespaces, and upper-case segments that only
// look like a namespace.
var allowedPrefixes = map[string]bool{
	"ABC": true, // placeholder namespace
	"XYZ": true, // placeholder namespace

	"NS": true, // generic namespace placeholder (adt/dependencies_test.go)

	// Upper-case path and type segments that only look like a namespace.
	"SOURCE": true, // ADT path segment "/SOURCE/MAIN" (adt/source_uri_*_test.go)
	"SAP":    true, // upper-cased "/SAP/BC/ADT" path (adt/textelements_test.go)
	"DEVC":   true, // object type in "R3TR/DEVC/<name>" (adt/transport_ecc_test.go)
	"CLAS":   true, // object type in "PROG/CLAS/INTF" (adt/rollback.go)
	"DOMA":   true, // object type in "DTEL/DOMA/TABL" (CLAUDE.md)
	"PUT":    true, // HTTP method in "POST/PUT/DELETE" (adt/client.go)
	"SKIP":   true, // test outcome in "PASS/SKIP/FAIL" (docs/superpowers/plans)
}

type finding struct {
	line int
	kind string
}

// transportNumbers returns the system ID of every transport or task number in
// line. A number counts unless a letter directly precedes it (then it is the
// tail of a longer word) or a digit directly follows it (a longer number).
// A digit, "%2f" or any other character in front does count.
func transportNumbers(line string) []string {
	var sids []string
	for _, m := range transportRe.FindAllStringSubmatchIndex(line, -1) {
		if m[0] > 0 && line[m[0]-1] >= 'A' && line[m[0]-1] <= 'Z' {
			continue
		}
		if m[1] < len(line) && line[m[1]] >= '0' && line[m[1]] <= '9' {
			continue
		}
		sids = append(sids, line[m[2]:m[3]])
	}
	return sids
}

func scan(content []byte) []finding {
	var out []finding
	for i, line := range strings.Split(string(content), "\n") {
		for _, sid := range transportNumbers(line) {
			if !placeholderSIDs[sid] {
				out = append(out, finding{i + 1, "transport or task number on a non-placeholder system ID"})
			}
		}
		for _, re := range []*regexp.Regexp{namespaceRe, encodedRe} {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				if !allowedPrefixes[strings.ToUpper(m[1])] {
					out = append(out, finding{i + 1, "name in a non-allowlisted /X/ namespace"})
				}
			}
		}
	}
	return out
}

// format renders a finding for the log: file, line and kind, never the value.
func format(name string, f finding) string {
	return fmt.Sprintf("%s:%d: %s", name, f.line, f.kind)
}

func main() {
	failed := false
	for _, name := range os.Args[1:] {
		content, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			failed = true
			continue
		}
		if bytes.IndexByte(content, 0) >= 0 {
			continue // binary file
		}
		for _, f := range scan(content) {
			fmt.Println(format(name, f))
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
