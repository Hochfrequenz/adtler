//go:build integration

package adt_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// searchPathSuffix identifies the quick search endpoint in a request path.
const searchPathSuffix = "/informationsystem/search"

// searchSampleLimit is how many objects each diagnostic search asks for.
const searchSampleLimit = 25

// searchSamplePattern is the name pattern of the diagnostic searches. A bare
// wildcard runs into the 30-second client timeout for classes on a large
// SAP ERP 6.0 system; customer-namespace objects are enough for the comparison.
const searchSamplePattern = "Z*"

// objectReferenceTag matches the opening tag of one search result entry,
// whatever namespace prefix the response uses.
var objectReferenceTag = regexp.MustCompile(`<(?:\w+:)?objectReference\b[^>]*>`)

// descriptionAttr matches the description attribute inside such a tag and
// captures its value. The prefix before the colon is deliberately not part of
// the pattern.
var descriptionAttr = regexp.MustCompile(`\bdescription="([^"]*)"`)

// searchBodyRecorder keeps the body of the most recent search response, read
// before the client sees it. It is how this test reaches the raw XML without
// a public API for it: the client is built on a caller-supplied RoundTripper,
// which sees every exchange.
type searchBodyRecorder struct {
	mu   sync.Mutex
	body []byte
}

func (r *searchBodyRecorder) set(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.body = b
}

func (r *searchBodyRecorder) last() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body
}

// rawDescriptionStats reports, for a raw quick search response, how many
// result entries it holds, how many of them carry a description attribute at
// all (even an empty one) and how many carry a non-empty one.
func rawDescriptionStats(raw []byte) (entries, withAttr, nonEmpty int) {
	for _, tag := range objectReferenceTag.FindAll(raw, -1) {
		entries++
		m := descriptionAttr.FindSubmatch(tag)
		if m == nil {
			continue
		}
		withAttr++
		if len(m[1]) > 0 {
			nonEmpty++
		}
	}
	return entries, withAttr, nonEmpty
}

// searchTypeSpelling pairs the short and the fully qualified spelling of one
// object type, the two forms adtler#152 compares.
type searchTypeSpelling struct {
	short, qualified string
}

// TestSearchObjects_DescriptionBySpelling_Diagnostic_Integration is a
// measurement, not a regression test. adtler#152 observed that SearchObjects
// returned descriptions for objectType "CLAS" and none for "CLAS/OC", and left
// open whether ADT omits the description attribute for the qualified form or
// whether the client's parsing drops it. This test settles that by comparing
// the raw response with what the client parsed out of it.
//
// What to look for in the -v output, per system and per type pair, one line
// for each spelling:
//
//   - "raw: attribute present" is false: ADT itself leaves the attribute out
//     for that spelling. Nothing to fix in the client; document it.
//   - "raw: attribute present" is true and "raw: non-empty" is 0: ADT sends the
//     attribute, but empty. Same conclusion.
//   - "raw: non-empty" is greater than 0 but "parsed: non-empty" is smaller: the
//     client drops a description ADT sent. That is a client bug, and the test
//     fails on it.
//
// The searches use a bare wildcard, so no object name is baked into the test,
// and the output holds booleans and counts only. A type pair is skipped when
// the system has no object of that type.
func TestSearchObjects_DescriptionBySpelling_Diagnostic_Integration(t *testing.T) {
	pairs := []searchTypeSpelling{
		{short: "CLAS", qualified: "CLAS/OC"},
		{short: "BDEF", qualified: "BDEF/BDO"},
		{short: "PROG", qualified: "PROG/P"},
	}
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			base := http.DefaultTransport.(*http.Transport).Clone()
			base.TLSClientConfig = &tls.Config{InsecureSkipVerify: sys.Config.TLSSkipVerify} //nolint:gosec
			t.Cleanup(base.CloseIdleConnections)

			rec := &searchBodyRecorder{}
			client := adt.NewClientWithTransport(sys.Config, roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				resp, err := base.RoundTrip(req)
				if err != nil || !strings.HasSuffix(req.URL.Path, searchPathSuffix) {
					return resp, err
				}
				body, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, readErr
				}
				rec.set(body)
				resp.Body = io.NopCloser(bytes.NewReader(body))
				return resp, nil
			}))

			// measure runs one search and logs what ADT sent next to what the
			// client parsed. It returns the number of entries ADT sent.
			measure := func(objectType string) int {
				results, err := client.SearchObjects(ctx, searchSamplePattern, objectType, searchSampleLimit)
				var adtErr *adt.ADTError
				if errors.As(err, &adtErr) && adtErr.StatusCode == http.StatusBadRequest && adtErr.Type == "ExceptionInvalidData" {
					// The release does not know this object type (SAP ERP 6.0
					// has no behavior definitions), so there is nothing to compare.
					t.Logf("objectType %s: not supported by this system", objectType)
					return 0
				}
				if err != nil {
					t.Fatalf("SearchObjects with objectType %s: %v", objectType, err)
				}
				parsedNonEmpty := 0
				for _, r := range results {
					if r.Description != "" {
						parsedNonEmpty++
					}
				}
				entries, rawWithAttr, rawNonEmpty := rawDescriptionStats(rec.last())
				if entries != len(results) {
					t.Errorf("objectType %s: raw response holds %d entries but the client returned %d", objectType, entries, len(results))
				}
				t.Logf("objectType %s: entries=%d raw: attribute present=%t raw: with attribute=%d raw: non-empty=%d parsed: non-empty=%d",
					objectType, entries, rawWithAttr > 0, rawWithAttr, rawNonEmpty, parsedNonEmpty)
				if parsedNonEmpty < rawNonEmpty {
					t.Errorf("objectType %s: ADT sent %d non-empty descriptions, the client returned %d", objectType, rawNonEmpty, parsedNonEmpty)
				}
				return entries
			}

			measuredAny := false
			for _, p := range pairs {
				if measure(p.short) == 0 {
					t.Logf("pair %s / %s: skipped, no object of the short type on this system", p.short, p.qualified)
					continue
				}
				measure(p.qualified)
				measuredAny = true
			}
			if !measuredAny {
				t.Skip("no object of any compared type on this system")
			}
		})
	}
}
