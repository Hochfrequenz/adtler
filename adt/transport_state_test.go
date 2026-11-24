package adt_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// The kinds of read transportListServer records in calls.
const (
	callTree = "tree"
	callE070 = "e070"
	callE07T = "e07t"
)

// transportListServer mocks the three sources of GetTransportRequests: the
// E070 data preview query (answered with e070, or with status e070Status when
// it is not 0), the E07T data preview query (see transportListAnswers) and the
// transport organizer tree (answered with tree, or with treeStatus when it is
// not 0). It records what the client asked, in order.
type transportListServer struct {
	*httptest.Server
	mu          sync.Mutex
	calls       []string // callTree, callE070 or callE07T, in order
	treeQueries []string // raw query strings of the tree reads
	sql         []string // E070 statements
	rowNumbers  []string // the rowNumber parameter of each E070 query
	e07tSQL     []string // E07T statements
}

// transportListAnswers configures transportListServer. An E07T statement is
// answered with the rows of e07tTexts whose request number occurs in it as
// 'NUMBER' (nothing set: no rows), with e07tStatus when that is not 0, and with
// a 500 when it is the e07tFailOnCall-th E07T statement (1-based, 0 never).
type transportListAnswers struct {
	tree, e070             string
	treeStatus, e070Status int
	e07tTexts              []e07tRow
	e07tStatus             int
	e07tFailOnCall         int
}

func newTransportListServer(t *testing.T, a transportListAnswers) *transportListServer {
	t.Helper()
	s := &transportListServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case transportRequestsEndpoint:
			s.mu.Lock()
			s.calls = append(s.calls, callTree)
			s.treeQueries = append(s.treeQueries, r.URL.RawQuery)
			s.mu.Unlock()
			if a.treeStatus != 0 {
				w.WriteHeader(a.treeStatus)
				return
			}
			_, _ = w.Write([]byte(a.tree))
		case datapreviewFreestyleEndpoint:
			body, _ := io.ReadAll(r.Body)
			statement := string(body)
			if strings.Contains(statement, "FROM E07T") {
				s.mu.Lock()
				s.calls = append(s.calls, callE07T)
				s.e07tSQL = append(s.e07tSQL, statement)
				call := len(s.e07tSQL)
				s.mu.Unlock()
				if a.e07tStatus != 0 {
					w.WriteHeader(a.e07tStatus)
					return
				}
				if call == a.e07tFailOnCall {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				var rows []e07tRow
				for _, row := range a.e07tTexts {
					if strings.Contains(statement, "'"+row[0]+"'") {
						rows = append(rows, row)
					}
				}
				_, _ = w.Write([]byte(e07tRows(rows...)))
				return
			}
			s.mu.Lock()
			s.calls = append(s.calls, callE070)
			s.sql = append(s.sql, statement)
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
	return s.clientInLanguage(user, "")
}

// clientInLanguage logs on in the given language ("" leaves it unset).
func (s *transportListServer) clientInLanguage(user, language string) adt.Client {
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: s.URL, User: user, Password: "P", Client: "100", Language: language})
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
	cells := make([][]string, len(rows))
	for i, row := range rows {
		cells[i] = row[:]
	}
	return dataPreviewRows([]string{"TRKORR", "AS4USER", "TRSTATUS", "AS4DATE", "AS4TIME"}, cells)
}

// e07tRow is one row of the E07T query: number, language, text.
type e07tRow [3]string

// e07tRows builds a data preview answer for the E07T query of
// GetTransportRequests.
func e07tRows(rows ...e07tRow) string {
	cells := make([][]string, len(rows))
	for i, row := range rows {
		cells[i] = row[:]
	}
	return dataPreviewRows([]string{"TRKORR", "LANGU", "AS4TEXT"}, cells)
}

// dataPreviewRows builds a data preview answer with the given columns; every
// row holds one cell per column.
func dataPreviewRows(columns []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	for col, name := range columns {
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

// e07tTextOf is the text mixedStatusE07T holds for number.
func e07tTextOf(number string) string { return "E07T text of " + number }

// englishE07T gives every number one English text, e07tTextOf(number).
func englishE07T(numbers ...string) []e07tRow {
	rows := make([]e07tRow, len(numbers))
	for i, n := range numbers {
		rows[i] = e07tRow{n, "E", e07tTextOf(n)}
	}
	return rows
}

// mixedStatusE07T holds an English text for every request of mixedStatusE070,
// AAAK900008 (which the tree does not show) included.
var mixedStatusE07T = englishE07T("AAAK900001", "AAAK900002", "AAAK900003", "AAAK900004",
	"AAAK900005", "AAAK900006", "AAAK900007", "AAAK900008")

// TestGetTransportRequests_FiltersAndDescribes guards #274, #242 and #277:
// E070 is the source of the list, in its order (newest first), filtered by
// state on the client too, and E07T supplies the descriptions, also for a
// request the tree does not show. The tree is not read.
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
			srv := newTransportListServer(t, transportListAnswers{
				tree: mixedStatusTreeXML, e070: mixedStatusE070, e07tTexts: mixedStatusE07T})
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
				if want := e07tTextOf(tr.Number); tr.Description != want {
					t.Errorf("%s: Description %q, want the E07T text %q", tr.Number, tr.Description, want)
				}
			}
			if page.Next != "" {
				t.Errorf("Next = %q, want \"\" for a single page", page.Next)
			}
			if want := []string{callE070, callE07T}; !reflect.DeepEqual(srv.calls, want) {
				t.Errorf("calls = %v, want %v", srv.calls, want)
			}
			if len(srv.treeQueries) != 0 {
				t.Errorf("tree read with queries %q, want no tree read when E070 and E07T answer", srv.treeQueries)
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
	srv := newTransportListServer(t, transportListAnswers{
		e07tTexts: englishE07T("AAAK900008", "SAPK-000AAINXYZ", "AAAK900001"),
		e070: e070Rows(
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
// user means the logon user on both sources. The tree is read only after a
// failure, so its user scope is checked with a failing E07T read.
func TestGetTransportRequests_EmptyUserIsLogonUser(t *testing.T) {
	srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: e070Rows()})
	if _, err := srv.client("usera").GetTransportRequests(context.Background(), adt.TransportListOptions{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql := srv.onlySQL(t); !strings.Contains(sql, "AS4USER = 'USERA'") {
		t.Errorf("E070 SQL must be scoped to the logon user: %s", sql)
	}
	if want := []string{callE070}; !reflect.DeepEqual(srv.calls, want) {
		t.Errorf("an empty E070 page: calls = %v, want %v (no E07T, no tree)", srv.calls, want)
	}

	failing := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e070: mixedStatusE070,
		e07tStatus: http.StatusInternalServerError})
	if _, err := failing.client("usera").GetTransportRequests(context.Background(), adt.TransportListOptions{}); !errors.Is(err, adt.ErrTransportListPartial) {
		t.Fatalf("E07T failing: err = %v, want ErrTransportListPartial", err)
	}
	if want := []string{"user=USERA"}; !reflect.DeepEqual(failing.treeQueries, want) {
		t.Errorf("tree queries = %q, want %q", failing.treeQueries, want)
	}
}

// TestGetTransportRequests_AllUsers checks TransportAllUsers: no user
// condition and an unscoped tree read (after a failing E07T read, the only
// case in which the tree is read), and no TRSTATUS condition for the all
// filter.
func TestGetTransportRequests_AllUsers(t *testing.T) {
	srv := newTransportListServer(t, transportListAnswers{tree: mixedStatusTreeXML, e07tStatus: http.StatusInternalServerError,
		e070: e070Rows(e070Row{"BBBK900001", "USERC", "D", "20261105", "120000"})})
	page, err := srv.client("U").GetTransportRequests(context.Background(), adt.TransportListOptions{User: adt.TransportAllUsers})
	if !errors.Is(err, adt.ErrTransportListPartial) {
		t.Fatalf("E07T failing: err = %v, want ErrTransportListPartial", err)
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
// from any source ends the call without a page (#222) and before the next
// source is read; any other failure yields a partial page wrapping
// ErrTransportListPartial, described from the tree where E07T failed; E070 and
// the tree failing is an error.
func TestGetTransportRequests_SourceFailures(t *testing.T) {
	const (
		e07tServerError = http.StatusInternalServerError
		treeServerError = http.StatusInternalServerError
	)
	cases := []struct {
		name        string
		answers     transportListAnswers
		wantCalls   []string
		wantPartial bool
		wantNil     bool
		wantNumbers []string
		wantDescr   map[string]string // description by number; a request not listed has none
	}{
		{
			name:      "401 from E070",
			answers:   transportListAnswers{tree: mixedStatusTreeXML, e070Status: http.StatusUnauthorized},
			wantCalls: []string{callE070}, wantNil: true,
		},
		{
			name:      "401 from E07T",
			answers:   transportListAnswers{tree: mixedStatusTreeXML, e070: mixedStatusE070, e07tStatus: http.StatusUnauthorized},
			wantCalls: []string{callE070, callE07T}, wantNil: true,
		},
		{
			name:      "E07T 500 + tree 401",
			answers:   transportListAnswers{treeStatus: http.StatusUnauthorized, e070: mixedStatusE070, e07tStatus: e07tServerError},
			wantCalls: []string{callE070, callE07T, callTree}, wantNil: true,
		},
		{
			// E07T holds no text, so the descriptions are the tree's: AAAK900008
			// is not in the tree and stays empty.
			name:      "E07T 500 + tree ok: described from the tree",
			answers:   transportListAnswers{tree: mixedStatusTreeXML, e070: mixedStatusE070, e07tStatus: e07tServerError},
			wantCalls: []string{callE070, callE07T, callTree}, wantPartial: true,
			wantNumbers: []string{"AAAK900008", "AAAK900002", "AAAK900001"},
			wantDescr:   map[string]string{"AAAK900002": "l", "AAAK900001": "d"},
		},
		{
			name:      "E07T 500 + tree 500: no descriptions",
			answers:   transportListAnswers{treeStatus: treeServerError, e070: mixedStatusE070, e07tStatus: e07tServerError},
			wantCalls: []string{callE070, callE07T, callTree}, wantPartial: true,
			wantNumbers: []string{"AAAK900008", "AAAK900002", "AAAK900001"},
		},
		{
			// The tree-only page keeps USERA's modifiable requests, newest
			// number first, and drops USERB's request. E07T is not asked: there
			// is no E070 page to describe.
			name:      "E070 fails: the tree's requests",
			answers:   transportListAnswers{tree: mixedStatusTreeXML, e070Status: http.StatusInternalServerError, e07tTexts: mixedStatusE07T},
			wantCalls: []string{callE070, callTree}, wantPartial: true,
			wantNumbers: []string{"AAAK900002", "AAAK900001"},
			wantDescr:   map[string]string{"AAAK900002": "l", "AAAK900001": "d"},
		},
		{
			name: "E070 and the tree fail",
			answers: transportListAnswers{
				treeStatus: http.StatusInternalServerError, e070Status: http.StatusInternalServerError,
			},
			wantCalls: []string{callE070, callTree}, wantNil: true,
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
				if want := tc.wantDescr[tr.Number]; tr.Description != want {
					t.Errorf("%s: Description %q, want %q", tr.Number, tr.Description, want)
				}
			}
			if page.Next != "" {
				t.Errorf("Next = %q, want \"\" on a partial page", page.Next)
			}
		})
	}
}

// numberedRequests returns count requests AAAK9000NN (NN from 01), all of
// USERA, in one E070 answer.
func numberedRequests(count int) (string, []string) {
	rows := make([]e070Row, count)
	numbers := make([]string, count)
	for i := range rows {
		numbers[i] = fmt.Sprintf("AAAK9%05d", i+1)
		rows[i] = e070Row{numbers[i], "USERA", "D", "20261105", "120000"}
	}
	return e070Rows(rows...), numbers
}

// TestGetTransportRequests_DescriptionLanguage checks which of the texts E07T
// holds for a request becomes its Description: the logon language (SAP "D" or
// ISO "DE" form), else English, else any other, and never an empty text.
func TestGetTransportRequests_DescriptionLanguage(t *testing.T) {
	const number = "AAAK900001"
	cases := []struct {
		name     string
		language string
		texts    []e07tRow
		want     string
	}{
		{"DE picks German", "DE", []e07tRow{{number, "E", "english"}, {number, "D", "german"}}, "german"},
		{"de in lower case picks German", "de", []e07tRow{{number, "E", "english"}, {number, "D", "german"}}, "german"},
		{"the SAP form D picks German", "D", []e07tRow{{number, "D", "german"}, {number, "E", "english"}}, "german"},
		{"no logon language picks English", "", []e07tRow{{number, "D", "german"}, {number, "E", "english"}}, "english"},
		{"EN picks English", "EN", []e07tRow{{number, "D", "german"}, {number, "E", "english"}}, "english"},
		{"ZH picks Chinese, which SAP keys as 1", "ZH", []e07tRow{{number, "E", "english"}, {number, "1", "chinese"}}, "chinese"},
		{"a missing logon language falls back to English", "DE", []e07tRow{{number, "F", "french"}, {number, "E", "english"}}, "english"},
		{"only French: French", "DE", []e07tRow{{number, "F", "french"}}, "french"},
		{"an empty text never wins", "DE", []e07tRow{{number, "D", ""}, {number, "E", "english"}}, "english"},
		{"only empty texts: no description", "DE", []e07tRow{{number, "D", ""}, {number, "E", " "}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTransportListServer(t, transportListAnswers{
				e070:      e070Rows(e070Row{number, "USERA", "D", "20261105", "120000"}),
				e07tTexts: tc.texts,
			})
			page, err := srv.clientInLanguage("U", tc.language).GetTransportRequests(context.Background(),
				adt.TransportListOptions{User: "USERA"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(page.Requests) != 1 || page.Requests[0].Description != tc.want {
				t.Errorf("requests = %+v, want one with Description %q", page.Requests, tc.want)
			}
		})
	}
}

// TestGetTransportRequests_DescriptionQueries checks how the page's request
// numbers are put into E07T statements: in several statements with an IN list
// the data preview parses, each number once, none that is not safe to embed,
// and none for an empty page.
func TestGetTransportRequests_DescriptionQueries(t *testing.T) {
	// ddicInListMaxBytes of package adt, which package adt_test cannot see.
	const inListMaxBytes = 150

	t.Run("30 requests", func(t *testing.T) {
		e070, numbers := numberedRequests(30)
		quoted := "AAAK9'1"
		e070 = strings.Replace(e070, "<dataPreview:data>"+numbers[29]+"</dataPreview:data>",
			"<dataPreview:data>"+quoted+"</dataPreview:data>", 1)
		numbers[29] = quoted
		srv := newTransportListServer(t, transportListAnswers{e070: e070, e07tTexts: englishE07T(numbers...)})
		page, err := srv.client("U").GetTransportRequests(context.Background(), adt.TransportListOptions{User: "USERA"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(srv.e07tSQL) < 2 {
			t.Fatalf("E07T statements = %d, want the 29 safe numbers split over several", len(srv.e07tSQL))
		}
		for _, stmt := range srv.e07tSQL {
			open, closing := strings.Index(stmt, "("), strings.LastIndex(stmt, ")")
			if open < 0 || closing < open {
				t.Fatalf("E07T statement without an IN list: %s", stmt)
			}
			if n := len(strings.TrimSpace(stmt[open+1 : closing])); n > inListMaxBytes {
				t.Errorf("IN list of %d bytes, want at most %d: %s", n, inListMaxBytes, stmt)
			}
			if strings.Contains(stmt, "AAAK9'") {
				t.Errorf("a number with a quote is in an E07T statement: %s", stmt)
			}
		}
		for _, n := range numbers[:29] {
			found := 0
			for _, stmt := range srv.e07tSQL {
				found += strings.Count(stmt, "'"+n+"'")
			}
			if found != 1 {
				t.Errorf("%s is in %d E07T statements, want exactly 1", n, found)
			}
		}
		for _, tr := range page.Requests {
			want := e07tTextOf(tr.Number)
			if tr.Number == quoted {
				want = ""
			}
			if tr.Description != want {
				t.Errorf("%s: Description %q, want %q", tr.Number, tr.Description, want)
			}
		}
	})

	t.Run("an empty page", func(t *testing.T) {
		srv := newTransportListServer(t, transportListAnswers{e070: e070Rows()})
		if _, err := srv.client("U").GetTransportRequests(context.Background(), adt.TransportListOptions{User: "USERA"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(srv.e07tSQL) != 0 {
			t.Errorf("E07T statements = %v, want none for an empty page", srv.e07tSQL)
		}
	})
}

// TestGetTransportRequests_PartialDescriptions checks that the texts E07T
// delivered before a statement failed are kept, and that the tree describes
// only the requests without one.
func TestGetTransportRequests_PartialDescriptions(t *testing.T) {
	e070, numbers := numberedRequests(30)
	var treeRequests strings.Builder
	treeRequests.WriteString(`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"><tm:workbench><tm:modifiable>`)
	for _, n := range numbers {
		treeRequests.WriteString(`<tm:request tm:number="` + n + `" tm:owner="USERA" tm:desc="tree text" tm:status="D"/>`)
	}
	treeRequests.WriteString(`</tm:modifiable></tm:workbench></tm:root>`)

	for _, treeFails := range []bool{false, true} {
		name := "tree ok"
		answers := transportListAnswers{e070: e070, e07tTexts: englishE07T(numbers...), e07tFailOnCall: 2}
		if treeFails {
			name, answers.treeStatus = "tree fails", http.StatusInternalServerError
		} else {
			answers.tree = treeRequests.String()
		}
		t.Run(name, func(t *testing.T) {
			srv := newTransportListServer(t, answers)
			page, err := srv.client("U").GetTransportRequests(context.Background(), adt.TransportListOptions{User: "USERA"})
			if !errors.Is(err, adt.ErrTransportListPartial) {
				t.Fatalf("err = %v, want ErrTransportListPartial", err)
			}
			if want := []string{callE070, callE07T, callE07T, callTree}; !reflect.DeepEqual(srv.calls, want) {
				t.Errorf("calls = %v, want %v (the third E07T statement is not sent)", srv.calls, want)
			}
			// The first statement holds the first requests of the page.
			described, fromTree := 0, 0
			for _, tr := range page.Requests {
				switch tr.Description {
				case e07tTextOf(tr.Number):
					described++
				case "tree text":
					fromTree++
				case "":
				default:
					t.Errorf("%s: unexpected Description %q", tr.Number, tr.Description)
				}
			}
			if described == 0 {
				t.Error("no description from E07T survived the failed second statement")
			}
			wantFromTree := len(numbers) - described
			if treeFails {
				wantFromTree = 0
			}
			if fromTree != wantFromTree {
				t.Errorf("described from the tree = %d, want %d (E07T texts: %d, tree fails: %v)", fromTree, wantFromTree, described, treeFails)
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
