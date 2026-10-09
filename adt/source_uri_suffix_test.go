package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

const (
	suffixTestProgURI   = "/sap/bc/adt/programs/programs/ztest"
	suffixTestClassURI  = "/sap/bc/adt/oo/classes/zcl_test"
	suffixTestInclude   = "testclasses"
	suffixTestTransport = "T1"
)

// suffixTestObjectStructure is the smallest objectstructure body
// GetClassDefinition can parse: one definitionBlock link ending on line 2.
const suffixTestObjectStructure = `<?xml version="1.0" encoding="utf-8"?>
<abapsource:objectStructureElement xmlns:abapsource="http://www.sap.com/adt/abapsource" xmlns:atom="http://www.w3.org/2005/Atom">
  <atom:link href="./source/main#start=1,0;end=2,8" rel="http://www.sap.com/adt/relations/source/definitionBlock"/>
</abapsource:objectStructureElement>`

// sourceURIInputForms lists the URIs a caller may hold for one object: the
// bare object URI, and the source URI forms ADT itself hands out
// (syntax-check messages, navigation targets, debugger stack frames).
func sourceURIInputForms(bare string) []string {
	return []string{
		bare,
		bare + "/",
		bare + "/source/main",
		bare + "/source/main/",
		bare + "/SOURCE/MAIN",
		bare + "/source/main#start=42,5",
	}
}

// newSourcePathRecorder starts a test server that answers every source
// request with a minimal valid body and records "METHOD path?query" for each
// request except the discovery / CSRF preflight. take returns the recorded
// requests sorted, and resets the record.
func newSourcePathRecorder(t *testing.T) (srv *httptest.Server, take func() []string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		mu.Unlock()
		w.Header().Set("ETag", `"etag"`)
		switch {
		case strings.HasSuffix(r.URL.Path, "/objectstructure"):
			_, _ = w.Write([]byte(suffixTestObjectStructure))
		case strings.HasSuffix(r.URL.Path, "/versions"):
			_, _ = w.Write([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"/>`))
		case strings.HasSuffix(r.URL.Path, "/codecompletion/proposal"):
			// Empty body: GetCompletions reports no proposals.
		default:
			_, _ = w.Write([]byte("REPORT ztest.\nWRITE 'x'."))
		}
	}))
	t.Cleanup(srv.Close)
	take = func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := seen
		seen = nil
		slices.Sort(out)
		return out
	}
	return srv, take
}

// TestSourceMethods_AcceptSourceURI is the regression test for adtler#210:
// every method that builds a sub-path from an object URI must send the same
// request for the bare object URI and for every source URI form of it — in
// particular, never ".../source/main" twice, and never lose its query to a
// fragment in the input.
func TestSourceMethods_AcceptSourceURI(t *testing.T) {
	ctx := context.Background()
	completionURI := "/sap/bc/adt/abapsource/codecompletion/proposal?" +
		url.Values{"uri": {suffixTestProgURI + "/source/main#start=3,4"}}.Encode()
	corrNr := "?corrNr=" + suffixTestTransport

	tests := []struct {
		name string
		bare string
		call func(c adt.Client, uri string) error
		want []string
	}{
		{
			name: "GetSource",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetSource(ctx, uri); return err },
			want: []string{"GET " + suffixTestProgURI + "/source/main"},
		},
		{
			name: "GetClassDefinition",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetClassDefinition(ctx, uri); return err },
			want: []string{
				"GET " + suffixTestClassURI + "/objectstructure",
				"GET " + suffixTestClassURI + "/source/main",
			},
		},
		{
			name: "GetIncludeSource",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.GetIncludeSource(ctx, uri, suffixTestInclude)
				return err
			},
			want: []string{"GET " + suffixTestClassURI + "/includes/" + suffixTestInclude},
		},
		{
			name: "SetIncludeSource",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.SetIncludeSource(ctx, uri, suffixTestInclude, "CLASS lcl DEFINITION.", "", suffixTestTransport, "")
				return err
			},
			want: []string{"PUT " + suffixTestClassURI + "/includes/" + suffixTestInclude + corrNr},
		},
		{
			name: "CreateTestInclude",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error {
				return c.CreateTestInclude(ctx, uri, "LH", suffixTestTransport)
			},
			want: []string{"POST " + suffixTestClassURI + "/includes?corrNr=" + suffixTestTransport + "&lockHandle=LH"},
		},
		{
			name: "SetSource",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.SetSource(ctx, uri, "REPORT ztest.", "", suffixTestTransport, "")
				return err
			},
			want: []string{"PUT " + suffixTestProgURI + "/source/main" + corrNr},
		},
		{
			name: "GetVersionHistory program",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetVersionHistory(ctx, uri); return err },
			want: []string{"GET " + suffixTestProgURI + "/source/main/versions"},
		},
		{
			name: "GetVersionHistory class",
			bare: suffixTestClassURI,
			call: func(c adt.Client, uri string) error { _, err := c.GetVersionHistory(ctx, uri); return err },
			want: []string{
				"GET " + suffixTestClassURI + "/includes/definitions/versions",
				"GET " + suffixTestClassURI + "/includes/implementations/versions",
			},
		},
		{
			name: "DiffActiveInactive",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error { _, err := c.DiffActiveInactive(ctx, uri); return err },
			want: []string{
				"GET " + suffixTestProgURI + "/source/main?version=active",
				"GET " + suffixTestProgURI + "/source/main?version=inactive",
			},
		},
		{
			name: "GetCompletions",
			bare: suffixTestProgURI,
			call: func(c adt.Client, uri string) error {
				_, err := c.GetCompletions(ctx, uri, "REPORT ztest.\nWRITE ", 3, 4)
				return err
			},
			want: []string{"POST " + completionURI},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, take := newSourcePathRecorder(t)
			client := adt.NewClient(newTestConfig(srv.URL))
			want := slices.Clone(tt.want)
			slices.Sort(want)
			for _, in := range sourceURIInputForms(tt.bare) {
				if err := tt.call(client, in); err != nil {
					t.Errorf("input %q: unexpected error: %v", in, err)
				}
				if got := take(); !slices.Equal(got, want) {
					t.Errorf("input %q: requests\n got %q\nwant %q", in, got, want)
				}
			}
		})
	}
}
