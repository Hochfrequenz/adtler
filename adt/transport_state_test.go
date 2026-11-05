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
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
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

// transportListServer mocks both sources of GetTransportRequests: the
// transport organizer tree (answered with tree, whatever the query) and the
// E070 data preview query (answered with e070, or with a 500 when e070 is
// ""). It records what the client asked.
type transportListServer struct {
	*httptest.Server
	mu          sync.Mutex
	treeQueries []string // raw query strings of the tree reads
	sql         []string // E070 statements
}

func newTransportListServer(t *testing.T, tree, e070 string) *transportListServer {
	t.Helper()
	s := &transportListServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case transportRequestsEndpoint:
			s.mu.Lock()
			s.treeQueries = append(s.treeQueries, r.URL.RawQuery)
			s.mu.Unlock()
			_, _ = w.Write([]byte(tree))
		case datapreviewFreestyleEndpoint:
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			s.sql = append(s.sql, string(body))
			s.mu.Unlock()
			if e070 == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(e070))
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

// e070Rows builds a data preview answer for SELECT TRKORR, AS4USER, TRSTATUS,
// one row per {number, owner, status} triple.
func e070Rows(rows ...[3]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	for col, name := range []string{"TRKORR", "AS4USER", "TRSTATUS"} {
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

// TestGetTransportRequests_StopsAfter401 guards #222: a 401 on the tree read
// must end the call without the E070 query sending the rejected Basic-auth
// credential again.
func TestGetTransportRequests_StopsAfter401(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != transportRequestsEndpoint && r.URL.Path != datapreviewFreestyleEndpoint {
			return // discovery and other preflights succeed
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	if _, err := client.GetTransportRequests(context.Background(), "", adt.TransportFilterAll); err == nil {
		t.Fatal("expected an error after a 401, got nil")
	}
	if len(paths) != 1 || paths[0] != transportRequestsEndpoint {
		t.Errorf("tree and data preview requests sent = %v, want exactly one tree read", paths)
	}
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

// TestGetTransportRequests_FiltersOnClient guards #274 and #242 point 2:
// every filter is applied to the tree's answer, by state rather than by a
// single letter, and the status parameter is not sent. It also checks the
// owner condition: the ECC tree lists requests in which the user only owns a
// task (AAAK900007), which are not the user's requests.
func TestGetTransportRequests_FiltersOnClient(t *testing.T) {
	cases := []struct {
		filter      adt.TransportFilter
		wantNumbers []string
	}{
		{adt.TransportFilterModifiable, []string{"AAAK900001", "AAAK900002"}},
		{adt.TransportFilterReleased, []string{"AAAK900004", "AAAK900005"}},
		{adt.TransportFilterAll,
			[]string{"AAAK900001", "AAAK900002", "AAAK900003", "AAAK900004", "AAAK900005", "AAAK900006"}},
	}
	for _, tc := range cases {
		t.Run(tc.filter.String(), func(t *testing.T) {
			srv := newTransportListServer(t, mixedStatusTreeXML, e070Rows())
			requests, err := srv.client("U").GetTransportRequests(context.Background(), "usera", tc.filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := requestNumbers(requests); !reflect.DeepEqual(got, tc.wantNumbers) {
				t.Errorf("numbers = %v, want %v", got, tc.wantNumbers)
			}
			for _, tr := range requests {
				if tr.State != adt.TransportStateOf(tr.RawStatus) {
					t.Errorf("%s: State %v does not match RawStatus %q", tr.Number, tr.State, tr.RawStatus)
				}
			}
			if want := []string{"user=USERA"}; !reflect.DeepEqual(srv.treeQueries, want) {
				t.Errorf("tree reads sent with queries %q, want %q (no status parameter)", srv.treeQueries, want)
			}
		})
	}
}

// TestGetTransportRequests_MergesTreeAndE070 guards #242 point 1: E070 is
// read on every call, not only when the tree yields nothing, and a request
// both sources know keeps the tree's description. This is the S/4 shape
// measured for #274: the tree lists a released request, E070 the modifiable
// ones the tree leaves out.
func TestGetTransportRequests_MergesTreeAndE070(t *testing.T) {
	tree := `<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"><tm:workbench><tm:released>
  <tm:request tm:number="AAAK900004" tm:owner="USERA" tm:desc="from the tree" tm:status="R"/>
</tm:released></tm:workbench></tm:root>`
	srv := newTransportListServer(t, tree, e070Rows(
		[3]string{"AAAK900001", "USERA", "D"},
		[3]string{"AAAK900004", "USERA", "R"},
		[3]string{"AAAK900009", "USERA", "N"},
	))

	requests, err := srv.client("U").GetTransportRequests(context.Background(), "USERA", adt.TransportFilterAll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := requestNumbers(requests), []string{"AAAK900001", "AAAK900004", "AAAK900009"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("numbers = %v, want %v (merged, deduplicated, sorted)", got, want)
	}
	if requests[1].Description != "from the tree" {
		t.Errorf("AAAK900004 Description = %q, want the tree's", requests[1].Description)
	}
	if requests[0].Description != "" || requests[0].Owner != "USERA" || requests[0].State != adt.TransportStateModifiable {
		t.Errorf("E070-only request = %+v, want owner USERA, state modifiable, no description", requests[0])
	}
}

// TestGetTransportRequests_E070ConditionAndRows checks the E070 query of a
// filter and that its rows are filtered again on the client.
func TestGetTransportRequests_E070ConditionAndRows(t *testing.T) {
	srv := newTransportListServer(t, `<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"/>`, e070Rows(
		[3]string{"BBBK900001", "USERA", "L"},
		[3]string{"BBBK900002", "USERA", "R"},
		[3]string{"BBBK900003", "USERA", "D"},
	))

	requests, err := srv.client("U").GetTransportRequests(context.Background(), "USERA", adt.TransportFilterModifiable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := srv.onlySQL(t)
	for _, want := range []string{"STRKORR = ''", "AS4USER = 'USERA'", "( TRSTATUS = 'D' OR TRSTATUS = 'L' )"} {
		if !strings.Contains(sql, want) {
			t.Errorf("E070 SQL lacks %q: %s", want, sql)
		}
	}
	if got, want := requestNumbers(requests), []string{"BBBK900001", "BBBK900003"}; !reflect.DeepEqual(got, want) {
		t.Errorf("numbers = %v, want %v (the R row must be filtered out)", got, want)
	}
}

// TestGetTransportRequests_EmptyUserIsLogonUser guards #242 point 3: an empty
// user means the logon user on both sources, not "my requests" on one and
// "everyone's" on the other.
func TestGetTransportRequests_EmptyUserIsLogonUser(t *testing.T) {
	srv := newTransportListServer(t, mixedStatusTreeXML, e070Rows())
	requests, err := srv.client("usera").GetTransportRequests(context.Background(), "", adt.TransportFilterModifiable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sql := srv.onlySQL(t); !strings.Contains(sql, "AS4USER = 'USERA'") {
		t.Errorf("E070 SQL must be scoped to the logon user: %s", sql)
	}
	if want := []string{"user=USERA"}; !reflect.DeepEqual(srv.treeQueries, want) {
		t.Errorf("tree queries = %q, want %q", srv.treeQueries, want)
	}
	if got, want := requestNumbers(requests), []string{"AAAK900001", "AAAK900002"}; !reflect.DeepEqual(got, want) {
		t.Errorf("numbers = %v, want %v", got, want)
	}
}

// TestGetTransportRequests_AllUsers checks TransportAllUsers: no user
// condition on either source and no owner filter, and no TRSTATUS condition
// for the all filter.
func TestGetTransportRequests_AllUsers(t *testing.T) {
	srv := newTransportListServer(t, mixedStatusTreeXML, e070Rows([3]string{"BBBK900001", "USERC", "D"}))
	requests, err := srv.client("U").GetTransportRequests(context.Background(), adt.TransportAllUsers, adt.TransportFilterAll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := srv.onlySQL(t)
	if _, where, _ := strings.Cut(sql, "WHERE"); strings.Contains(where, "AS4USER") || strings.Contains(where, "TRSTATUS") {
		t.Errorf("E070 SQL for every user and every state must have neither condition: %s", sql)
	}
	if want := []string{""}; !reflect.DeepEqual(srv.treeQueries, want) {
		t.Errorf("tree queries = %q, want one unscoped read", srv.treeQueries)
	}
	if len(requests) != 8 {
		t.Errorf("got %d requests, want 8 (7 from the tree incl. USERB's, 1 from E070)", len(requests))
	}
}

// TestGetTransportRequests_E070FailureIsIncomplete checks that a failed E070
// read still returns the tree's requests, marked as incomplete.
func TestGetTransportRequests_E070FailureIsIncomplete(t *testing.T) {
	srv := newTransportListServer(t, mixedStatusTreeXML, "")
	requests, err := srv.client("U").GetTransportRequests(context.Background(), "USERA", adt.TransportFilterModifiable)
	if !errors.Is(err, adt.ErrTransportListIncomplete) {
		t.Fatalf("err = %v, want ErrTransportListIncomplete", err)
	}
	if got, want := requestNumbers(requests), []string{"AAAK900001", "AAAK900002"}; !reflect.DeepEqual(got, want) {
		t.Errorf("partial numbers = %v, want the tree's %v", got, want)
	}
}

// TestGetTransportRequests_RefusedBeforeAnyRequest covers the arguments that
// are refused locally: an invalid filter, a user that cannot be embedded in
// the E070 query, and an empty user on a client without a logon user.
func TestGetTransportRequests_RefusedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name      string
		logonUser string
		user      string
		filter    adt.TransportFilter
	}{
		{"invalid filter", "U", "", adt.TransportFilter(42)},
		{"quote in user", "U", "US'ERA", adt.TransportFilterModifiable},
		{"no user, no logon user", "", "", adt.TransportFilterModifiable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTransportListServer(t, mixedStatusTreeXML, e070Rows())
			if _, err := srv.client(tc.logonUser).GetTransportRequests(context.Background(), tc.user, tc.filter); err == nil {
				t.Fatal("expected an error, got nil")
			}
			if len(srv.treeQueries) != 0 || len(srv.sql) != 0 {
				t.Errorf("sent %d tree read(s) and %d E070 quer(ies), want none", len(srv.treeQueries), len(srv.sql))
			}
		})
	}
}
