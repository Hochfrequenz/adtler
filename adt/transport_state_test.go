package adt_test

import (
	"context"
	"encoding/json"
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

// TestGetTransportRequests_StopsAfter401 guards #222: a 401 on the tree read
// must end the call without the E070 fallback sending the rejected Basic-auth
// credential again.
func TestGetTransportRequests_StopsAfter401(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != transportRequestsEndpoint && !strings.Contains(r.URL.Path, "datapreview") {
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

// mixedStatusTreeXML carries one request per TRSTATUS letter plus an unknown
// one, in both groups and both buckets. The transport organizer tree does not
// honour SAP's status parameter (measured on SAP_BASIS 750 and 816), so the
// filter has to be applied by the client.
const mixedStatusTreeXML = `<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:workbench>
    <tm:modifiable>
      <tm:request tm:number="AAAK900001" tm:owner="USERA" tm:desc="d" tm:status="D"/>
      <tm:request tm:number="AAAK900002" tm:owner="USERA" tm:desc="l" tm:status="L"/>
      <tm:request tm:number="AAAK900003" tm:owner="USERA" tm:desc="o" tm:status="O"/>
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

// TestGetTransportRequests_FiltersOnClient guards #274 and #242 point 2: the
// tree ignores SAP's status parameter, so every filter must be applied to the
// answer, by state rather than by a single letter, and the parameter is not
// sent at all.
func TestGetTransportRequests_FiltersOnClient(t *testing.T) {
	cases := []struct {
		filter      adt.TransportFilter
		wantNumbers []string
	}{
		{adt.TransportFilterModifiable, []string{"AAAK900001", "AAAK900002"}},
		{adt.TransportFilterReleased, []string{"AAAK900004", "AAAK900005"}},
		{adt.TransportFilterAll,
			[]string{"AAAK900001", "AAAK900002", "AAAK900003", "AAAK900004", "AAAK900006", "AAAK900005"}},
	}
	for _, tc := range cases {
		t.Run(tc.filter.String(), func(t *testing.T) {
			var mu sync.Mutex
			var gotQueries []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "datapreview") {
					t.Errorf("the tree held matching requests: no E070 fallback expected")
				}
				if r.URL.Path != transportRequestsEndpoint {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				mu.Lock()
				gotQueries = append(gotQueries, r.URL.RawQuery)
				mu.Unlock()
				_, _ = w.Write([]byte(mixedStatusTreeXML))
			}))
			defer srv.Close()
			client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

			requests, err := client.GetTransportRequests(context.Background(), "", tc.filter)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var numbers []string
			for _, tr := range requests {
				numbers = append(numbers, tr.Number)
				if tr.State != adt.TransportStateOf(tr.RawStatus) {
					t.Errorf("%s: State %v does not match RawStatus %q", tr.Number, tr.State, tr.RawStatus)
				}
			}
			if !reflect.DeepEqual(numbers, tc.wantNumbers) {
				t.Errorf("numbers = %v, want %v", numbers, tc.wantNumbers)
			}
			if want := []string{""}; !reflect.DeepEqual(gotQueries, want) {
				t.Errorf("tree reads sent with queries %q, want one read without a status parameter", gotQueries)
			}
		})
	}
}

// e070FallbackRowsXML is a data preview answer with one top-level request per
// TRSTATUS letter, so the client-side filter is exercised on the fallback
// path even when the SQL condition was not applied.
const e070FallbackRowsXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">` +
	`<dataPreview:totalRows>3</dataPreview:totalRows>` +
	`<dataPreview:columns><dataPreview:metadata dataPreview:name="TRKORR" dataPreview:type="C"/>` +
	`<dataPreview:dataSet><dataPreview:data>BBBK900001</dataPreview:data><dataPreview:data>BBBK900002</dataPreview:data><dataPreview:data>BBBK900003</dataPreview:data></dataPreview:dataSet></dataPreview:columns>` +
	`<dataPreview:columns><dataPreview:metadata dataPreview:name="AS4USER" dataPreview:type="C"/>` +
	`<dataPreview:dataSet><dataPreview:data>USERA</dataPreview:data><dataPreview:data>USERA</dataPreview:data><dataPreview:data>USERA</dataPreview:data></dataPreview:dataSet></dataPreview:columns>` +
	`<dataPreview:columns><dataPreview:metadata dataPreview:name="TRSTATUS" dataPreview:type="C"/>` +
	`<dataPreview:dataSet><dataPreview:data>L</dataPreview:data><dataPreview:data>R</dataPreview:data><dataPreview:data>D</dataPreview:data></dataPreview:dataSet></dataPreview:columns>` +
	`</dataPreview:tableData>`

// TestGetTransportRequests_FallbackWhenNothingPassesFilter reproduces the S/4
// answer measured for #274: asked for modifiable requests, the tree returns
// released ones only. Nothing passes the filter, so the E070 fallback runs,
// with a condition covering both modifiable letters, and its rows are
// filtered too.
func TestGetTransportRequests_FallbackWhenNothingPassesFilter(t *testing.T) {
	var gotSQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "tok")
		case transportRequestsEndpoint:
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"><tm:workbench><tm:released>
  <tm:request tm:number="AAAK900004" tm:owner="USERA" tm:desc="r" tm:status="R"/>
</tm:released></tm:workbench></tm:root>`))
		case "/sap/bc/adt/datapreview/freestyle":
			body, _ := io.ReadAll(r.Body)
			gotSQL = string(body)
			_, _ = w.Write([]byte(e070FallbackRowsXML))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	requests, err := client.GetTransportRequests(context.Background(), "USERA", adt.TransportFilterModifiable)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(gotSQL, "( TRSTATUS = 'D' OR TRSTATUS = 'L' )") {
		t.Errorf("fallback SQL must ask for both modifiable letters, got: %s", gotSQL)
	}
	var numbers []string
	for _, tr := range requests {
		numbers = append(numbers, tr.Number)
	}
	if want := []string{"BBBK900001", "BBBK900003"}; !reflect.DeepEqual(numbers, want) {
		t.Errorf("numbers = %v, want %v (the R row must be filtered out)", numbers, want)
	}
}

// TestGetTransportRequests_AllFilterHasNoStatusCondition checks that the E070
// fallback of TransportFilterAll puts no TRSTATUS condition into the query.
func TestGetTransportRequests_AllFilterHasNoStatusCondition(t *testing.T) {
	var gotSQL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "tok")
		case transportRequestsEndpoint:
			_, _ = w.Write([]byte(`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"/>`))
		case "/sap/bc/adt/datapreview/freestyle":
			body, _ := io.ReadAll(r.Body)
			gotSQL = string(body)
			_, _ = w.Write([]byte(e070FallbackRowsXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	requests, err := client.GetTransportRequests(context.Background(), "", adt.TransportFilterAll)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, where, _ := strings.Cut(gotSQL, "WHERE"); strings.Contains(where, "TRSTATUS") {
		t.Errorf("fallback SQL of the all filter must not filter by status, got: %s", gotSQL)
	}
	if len(requests) != 3 {
		t.Errorf("got %d requests, want all 3 rows", len(requests))
	}
}

func TestGetTransportRequests_InvalidFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an invalid filter must be refused before any request, got %s", r.URL.Path)
	}))
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	if _, err := client.GetTransportRequests(context.Background(), "", adt.TransportFilter(42)); err == nil {
		t.Fatal("expected an error for an invalid filter, got nil")
	}
}
