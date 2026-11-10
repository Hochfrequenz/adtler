package adt_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

func TestTransportStateOf(t *testing.T) {
	for raw, want := range map[string]adt.TransportState{
		adt.TransportStatusModifiable:          adt.TransportStateModifiable,
		adt.TransportStatusModifiableProtected: adt.TransportStateModifiable,
		adt.TransportStatusReleaseStarted:      adt.TransportStateReleaseInProgress,
		adt.TransportStatusReleased:            adt.TransportStateReleased,
		adt.TransportStatusReleasedProtected:   adt.TransportStateReleased,
		"":                                     adt.TransportStateUnknown,
		"X":                                    adt.TransportStateUnknown,
		"r":                                    adt.TransportStateUnknown,
	} {
		if got := adt.TransportStateOf(raw); got != want {
			t.Errorf("TransportStateOf(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestTransportState_JSON(t *testing.T) {
	res := adt.ReleaseResult{Transport: "AAAK900001", State: adt.TransportStateReleaseInProgress, RawStatus: "O"}
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	const want = `{"transport":"AAAK900001","state":"release_in_progress","raw_status":"O"}`
	if string(data) != want {
		t.Errorf("Marshal = %s, want %s", data, want)
	}
	var back adt.ReleaseResult
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back != res {
		t.Errorf("round trip = %+v, want %+v", back, res)
	}
	if err := json.Unmarshal([]byte(`{"state":"done"}`), &back); err == nil {
		t.Error("Unmarshal of an unknown state name: want an error, got nil")
	}
	if got := adt.TransportState(99).String(); got != "unknown" {
		t.Errorf("String() of an out-of-range state = %q, want unknown", got)
	}
}

// TestTransportFilter_Text checks that a consumer can take the filter
// straight from a string parameter.
func TestTransportFilter_Text(t *testing.T) {
	for _, f := range []adt.TransportFilter{adt.TransportFilterAll, adt.TransportFilterModifiable, adt.TransportFilterReleased} {
		text, err := f.MarshalText()
		if err != nil {
			t.Fatalf("MarshalText(%v): %v", f, err)
		}
		var back adt.TransportFilter
		if err := back.UnmarshalText(text); err != nil || back != f {
			t.Errorf("round trip of %q = %v, %v; want %v", text, back, err, f)
		}
	}
	var f adt.TransportFilter
	for _, bad := range []string{"", "D", "Released", "open"} {
		if err := f.UnmarshalText([]byte(bad)); err == nil {
			t.Errorf("UnmarshalText(%q): want an error, got filter %v", bad, f)
		}
	}
	if _, err := adt.TransportFilter(42).MarshalText(); err == nil {
		t.Error("MarshalText of an invalid filter: want an error, got nil")
	}
}

// transportListServer mocks both sources of GetTransportRequests: the E070
// data preview query (answered with e070, or with status e070Status when it is
// not 0) and the transport organizer tree (answered with tree, or with
// treeStatus when it is not 0). It records what the client asked, in order.
type transportListServer struct {
	*httptest.Server
	mu          sync.Mutex
	calls       []string // "tree" or "e070", in order
	treeQueries []string // raw query strings of the tree reads
	sql         []string // E070 statements
	rowNumbers  []string // the rowNumber parameter of each E070 query
}

type transportListAnswers struct {
	tree, e070             string
	treeStatus, e070Status int
}

func newTransportListServer(t *testing.T, a transportListAnswers) *transportListServer {
	t.Helper()
	s := &transportListServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case transportRequestsEndpoint:
			s.mu.Lock()
			s.calls = append(s.calls, "tree")
			s.treeQueries = append(s.treeQueries, r.URL.RawQuery)
			s.mu.Unlock()
			if a.treeStatus != 0 {
				w.WriteHeader(a.treeStatus)
				return
			}
			_, _ = w.Write([]byte(a.tree))
		case datapreviewFreestyleEndpoint:
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.calls = append(s.calls, "e070")
			s.sql = append(s.sql, string(body))
			s.rowNumbers = append(s.rowNumbers, r.URL.Query().Get("rowNumber"))
			s.mu.Unlock()
			if a.e070Status != 0 {
				w.WriteHeader(a.e070Status)
				return
			}
			_, _ = w.Write([]byte(a.e070))
		default:
			// CSRF preflight and discovery succeed.
			w.Header().Set("X-CSRF-Token", "tok")
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *transportListServer) client(user string) adt.Client {
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: s.URL, User: user, Password: "P", Client: "100"})
}

func (s *transportListServer) onlySQL(t *testing.T) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sql) != 1 {
		t.Fatalf("E070 queries sent = %d, want 1", len(s.sql))
	}
	return s.sql[0]
}

const datapreviewFreestyleEndpoint = "/sap/bc/adt/datapreview/freestyle"

// e070Row is one row of the E070 query: number, owner, status, date, time.
type e070Row [5]string

// e070Rows builds a data preview answer for the E070 query of
// GetTransportRequests.
func e070Rows(rows ...e070Row) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	for col, name := range []string{"TRKORR", "AS4USER", "TRSTATUS", "AS4DATE", "AS4TIME"} {
		b.WriteString(`<dataPreview:columns><dataPreview:metadata dataPreview:name="` + name + `" dataPreview:type="C"/><dataPreview:dataSet>`)
		for _, row := range rows {
			b.WriteString(`<dataPreview:data>` + row[col] + `</dataPreview:data>`)
		}
		b.WriteString(`</dataPreview:dataSet></dataPreview:columns>`)
	}
	b.WriteString(`</dataPreview:tableData>`)
	return b.String()
}

func requestNumbers(requests []adt.TransportRequest) []string {
	numbers := []string{}
	for _, tr := range requests {
		numbers = append(numbers, tr.Number)
	}
	return numbers
}

// mixedStatusTreeXML carries one request of USERA per TRSTATUS letter plus an
// unknown one, in both groups and both buckets, and one request of USERB. The
// transport organizer tree does not honour SAP's status parameter (measured
// on SAP_BASIS 750 and 816), so the filter has to be applied by the client.
const mixedStatusTreeXML = `<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:workbench>
    <tm:modifiable>
      <tm:request tm:number="AAAK900001" tm:owner="USERA" tm:desc="d" tm:status="D"/>
      <tm:request tm:number="AAAK900002" tm:owner="USERA" tm:desc="l" tm:status="L"/>
      <tm:request tm:number="AAAK900003" tm:owner="USERA" tm:desc="o" tm:status="O"/>
      <tm:request tm:number="AAAK900007" tm:owner="USERB" tm:desc="task of USERA only" tm:status="D"/>
    </tm:modifiable>
    <tm:released>
      <tm:request tm:number="AAAK900004" tm:owner="USERA" tm:desc="r" tm:status="R"/>
    </tm:released>
  </tm:workbench>
  <tm:customizing>
    <tm:modifiable>
      <tm:request tm:number="AAAK900006" tm:owner="USERA" tm:desc="x" tm:status="X"/>
    </tm:modifiable>
    <tm:released>
      <tm:request tm:number="AAAK900005" tm:owner="USERA" tm:desc="n" tm:status="N"/>
    </tm:released>
  </tm:customizing>
</tm:root>`

// mixedStatusE070 holds USERA's requests of mixedStatusTreeXML plus one the
// tree does not show (AAAK900008), newest first.
var mixedStatusE070 = e070Rows(
	e070Row{"AAAK900008", "USERA", "D", "20261105", "120000"},
	e070Row{"AAAK900006", "USERA", "X", "20261104", "120000"},
	e070Row{"AAAK900005", "USERA", "N", "20261103", "120000"},
	e070Row{"AAAK900004", "USERA", "R", "20261102", "120000"},
	e070Row{"AAAK900003", "USERA", "O", "20261101", "120000"},
	e070Row{"AAAK900002", "USERA", "L", "20261031", "120000"},
	e070Row{"AAAK900001", "USERA", "D", "20261030", "120000"},
)

// TestGetTransportRequests_FiltersAndDescribes guards #274 and #242: E070 is
// the source of the list, in its order (newest first), filtered by state on
// the client too, and the tree supplies the descriptions. A request the tree
// does not show keeps an empty description.
func TestGetTransportRequests_FiltersAndDescribes(t *testing.T) {
	cases := []struct {
		filter      adt.TransportFilter
		wantNumbers []string
	}{
		{adt.TransportFilterModifiable, []string{"AAAK900008", "AAAK900002", "AAAK900001"}},
		{adt.TransportFilterReleased, []string{"AAAK900005", "AAAK900004"}},
		{adt.TransportFilterAll,
			[]string{"AAAK900008", "AAAK900006", "AAAK900005", "AAAK900004", "AAAK900003", "AAAK900002", "AAAK900001"}},
	}
	for _, tc := range cases {
		t.Run(tc.filter.String(), func(t *testing.T) {
			srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: mixedStatusE070})
			page, err := srv.client("U").GetTransportRequests(context.Background(),
				adt.TransportListOptions{User: "usera", Filter: tc.filter})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := requestNumbers(page.Requests); !reflect.DeepEqual(got, tc.wantNumbers) {
				t.Errorf("numbers = %v, want %v", got, tc.wantNumbers)
			}
			for _, tr := range page.Requests {
				if tr.State != adt.TransportStateOf(tr.RawStatus) {
					t.Errorf("%s: State %v does not match RawStatus %q", tr.Number, tr.State, tr.RawStatus)
				}
				wantDescribed := tr.Number != "AAAK900008"
				if (tr.Description != "") != wantDescribed {
					t.Errorf("%s: Description %q, want described = %v", tr.Number, tr.Description, wantDescribed)
				}
			}
			if page.Next != "" {
				t.Errorf("Next = %q, want \"\" for a single page", page.Next)
			}
			if want := []string{"user=USERA"}; !reflect.DeepEqual(srv.treeQueries, want) {
				t.Errorf("tree reads sent with queries %q, want %q (no status parameter)", srv.treeQueries, want)
			}
		})
	}
}

// TestGetTransportRequests_Query checks the E070 statement: the user, the
// state condition, and the descending sort spelled DESCENDING (the data
// preview rejects DESC on both releases).
func TestGetTransportRequests_Query(t *testing.T) {
	srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: e070Rows()})
	if _, err := srv.client("U").GetTransportRequests(context.Background(),
		adt.TransportListOptions{User: "USERA", Filter: adt.TransportFilterModifiable}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := strings.Join(strings.Fields(srv.onlySQL(t)), " ")
	for _, want := range []string{
		"STRKORR = ''", "AS4USER = 'USERA'", "( TRSTATUS = 'D' OR TRSTATUS = 'L' )",
		"ORDER BY AS4DATE DESCENDING, AS4TIME DESCENDING, TRKORR DESCENDING",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("E070 SQL lacks %q: %s", want, sql)
		}
	}
	if strings.Contains(sql, " DESC,") || strings.HasSuffix(sql, " DESC") {
		t.Errorf("E070 SQL uses DESC, which the data preview rejects: %s", sql)
	}
}

// TestGetTransportRequests_Paging walks two pages: the first asks for one row
// more than the limit, returns the limit and a cursor from its last row; the
// second continues strictly after that row.
func TestGetTransportRequests_Paging(t *testing.T) {
	srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: e070Rows(
		e070Row{"AAAK900008", "USERA", "D", "20261105", "120000"},
		e070Row{"SAPK-000AAINXYZ", "USERA", "D", "20261031", "093000"},
		e070Row{"AAAK900001", "USERA", "D", "20261030", "120000"},
	)})
	client := srv.client("U")
	opts := adt.TransportListOptions{User: "USERA", Limit: 2}

	page, err := client.GetTransportRequests(context.Background(), opts)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	// The last row is an SAP-delivered request, whose number has a hyphen.
	if got, want := requestNumbers(page.Requests), []string{"AAAK900008", "SAPK-000AAINXYZ"}; !reflect.DeepEqual(got, want) {
		t.Errorf("first page = %v, want %v", got, want)
	}
	if want := "20261031.093000.SAPK-000AAINXYZ"; page.Next != want {
		t.Fatalf("Next = %q, want %q", page.Next, want)
	}
	if srv.rowNumbers[0] != "3" {
		t.Errorf("first query asked for %s rows, want limit+1 = 3", srv.rowNumbers[0])
	}

	opts.After = page.Next
	if _, err := client.GetTransportRequests(context.Background(), opts); err != nil {
		t.Fatalf("second page: %v", err)
	}
	const wantCond = "( AS4DATE < '20261031'" +
		" OR ( AS4DATE = '20261031' AND CAST( AS4TIME AS CHAR( 6 ) ) < '093000' )" +
		" OR ( AS4DATE = '20261031' AND CAST( AS4TIME AS CHAR( 6 ) ) = '093000' AND TRKORR < 'SAPK-000AAINXYZ' ) )"
	// RunQuery wraps lines longer than the data preview accepts.
	if !strings.Contains(strings.Join(strings.Fields(srv.sql[1]), " "), wantCond) {
		t.Errorf("second query lacks the cursor condition %q: %s", wantCond, srv.sql[1])
	}
	if strings.Contains(srv.sql[0], "AS4DATE <") {
		t.Errorf("first query must not carry a cursor condition: %s", srv.sql[0])
	}
}

// TestGetTransportRequests_EmptyUserIsLogonUser guards #242 point 3: an empty
// user means the logon user on both sources.
func TestGetTransportRequests_EmptyUserIsLogonUser(t *testing.T) {
	srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: e070Rows()})
	if _, err := srv.client("usera").GetTransportRequests(context.Background(), adt.TransportListOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql := srv.onlySQL(t); !strings.Contains(sql, "AS4USER = 'USERA'") {
		t.Errorf("E070 SQL must be scoped to the logon user: %s", sql)
	}
	if want := []string{"user=USERA"}; !reflect.DeepEqual(srv.treeQueries, want) {
		t.Errorf("tree queries = %q, want %q", srv.treeQueries, want)
	}
}

// TestGetTransportRequests_AllUsers checks TransportAllUsers: no user
// condition and an unscoped tree read, and no TRSTATUS condition for the all
// filter.
func TestGetTransportRequests_AllUsers(t *testing.T) {
	srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: e070Rows(
		e070Row{"BBBK900001", "USERC", "D", "20261105", "120000"},
	)})
	page, err := srv.client("U").GetTransportRequests(context.Background(), adt.TransportListOptions{User: adt.TransportAllUsers})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := srv.onlySQL(t)
	if where, _, _ := strings.Cut(sql, "ORDER BY"); strings.Contains(where, "AS4USER =") || strings.Contains(where, "TRSTATUS =") {
		t.Errorf("E070 SQL for every user and every state must have neither condition: %s", sql)
	}
	if want := []string{""}; !reflect.DeepEqual(srv.treeQueries, want) {
		t.Errorf("tree queries = %q, want one unscoped read", srv.treeQueries)
	}
	if got, want := requestNumbers(page.Requests), []string{"BBBK900001"}; !reflect.DeepEqual(got, want) {
		t.Errorf("numbers = %v, want %v (E070 is the source)", got, want)
	}
}

// TestGetTransportRequests_SourceFailures covers the error contract: a 401
// from either source ends the call without a page (#222) and, from E070,
// before the tree is read; any other failure of one source yields a partial
// page wrapping ErrTransportListPartial; both failing is an error.
func TestGetTransportRequests_SourceFailures(t *testing.T) {
	cases := []struct {
		name        string
		answers     transportListAnswers
		wantCalls   []string
		wantPartial bool
		wantNil     bool
		wantNumbers []string
		wantDescr   bool
	}{
		{
			name:      "401 from E070",
			answers:   transportListAnswers{tree: mixedStatusTreeXML, e070Status: http.StatusUnauthorized},
			wantCalls: []string{"e070"}, wantNil: true,
		},
		{
			name:      "401 from the tree",
			answers:   transportListAnswers{treeStatus: http.StatusUnauthorized, e070: mixedStatusE070},
			wantCalls: []string{"e070", "tree"}, wantNil: true,
		},
		{
			name:      "tree fails: no descriptions",
			answers:   transportListAnswers{treeStatus: http.StatusInternalServerError, e070: mixedStatusE070},
			wantCalls: []string{"e070", "tree"}, wantPartial: true,
			wantNumbers: []string{"AAAK900008", "AAAK900002", "AAAK900001"},
		},
		{
			// The tree-only page keeps USERA's modifiable requests, newest
			// number first, and drops USERB's request.
			name:      "E070 fails: the tree's requests",
			answers:   transportListAnswers{tree: mixedStatusTreeXML, e070Status: http.StatusInternalServerError},
			wantCalls: []string{"e070", "tree"}, wantPartial: true,
			wantNumbers: []string{"AAAK900002", "AAAK900001"}, wantDescr: true,
		},
		{
			name: "both fail",
			answers: transportListAnswers{
				treeStatus: http.StatusInternalServerError, e070Status: http.StatusInternalServerError,
			},
			wantCalls: []string{"e070", "tree"}, wantNil: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTransportListServer(t, tc.answers)
			page, err := srv.client("U").GetTransportRequests(context.Background(),
				adt.TransportListOptions{User: "USERA", Filter: adt.TransportFilterModifiable})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if got := errors.Is(err, adt.ErrTransportListPartial); got != tc.wantPartial {
				t.Errorf("errors.Is(err, ErrTransportListPartial) = %v, want %v (err: %v)", got, tc.wantPartial, err)
			}
			if !reflect.DeepEqual(srv.calls, tc.wantCalls) {
				t.Errorf("calls = %v, want %v", srv.calls, tc.wantCalls)
			}
			if tc.wantNil {
				if page != nil {
					t.Errorf("page = %+v, want nil", page)
				}
				return
			}
			if got := requestNumbers(page.Requests); !reflect.DeepEqual(got, tc.wantNumbers) {
				t.Errorf("numbers = %v, want %v", got, tc.wantNumbers)
			}
			for _, tr := range page.Requests {
				if (tr.Description != "") != tc.wantDescr {
					t.Errorf("%s: Description %q, want described = %v", tr.Number, tr.Description, tc.wantDescr)
				}
			}
			if page.Next != "" {
				t.Errorf("Next = %q, want \"\" on a partial page", page.Next)
			}
		})
	}
}

// TestGetTransportRequests_UnexpectedSortKey refuses to hand out a cursor the
// next call would reject.
func TestGetTransportRequests_UnexpectedSortKey(t *testing.T) {
	srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: e070Rows(
		e070Row{"AAAK900002", "USERA", "D", "2026-10-31", "09:30:00"},
		e070Row{"AAAK900001", "USERA", "D", "2026-10-30", "12:00:00"},
	)})
	if _, err := srv.client("U").GetTransportRequests(context.Background(),
		adt.TransportListOptions{User: "USERA", Limit: 1}); err == nil {
		t.Fatal("expected an error for a sort key in an unexpected format, got nil")
	}
}

// TestGetTransportRequests_RefusedBeforeAnyRequest covers the options that
// are refused locally.
func TestGetTransportRequests_RefusedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name      string
		logonUser string
		opts      adt.TransportListOptions
	}{
		{"invalid filter", "U", adt.TransportListOptions{Filter: adt.TransportFilter(42)}},
		{"quote in user", "U", adt.TransportListOptions{User: "US'ERA"}},
		{"no user, no logon user", "", adt.TransportListOptions{}},
		{"negative limit", "U", adt.TransportListOptions{Limit: -1}},
		{"limit too large", "U", adt.TransportListOptions{Limit: adt.MaxTransportPageSize + 1}},
		{"cursor with a quote", "U", adt.TransportListOptions{After: "20261031.093000.AAA'K"}},
		{"cursor of another shape", "U", adt.TransportListOptions{After: "page-2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: e070Rows()})
			if _, err := srv.client(tc.logonUser).GetTransportRequests(context.Background(), tc.opts); err == nil {
				t.Fatal("expected an error, got nil")
			}
			if len(srv.calls) != 0 {
				t.Errorf("sent %v, want no request", srv.calls)
			}
		})
	}
}
