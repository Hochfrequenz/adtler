package adt_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// The released ECC request the fallback tests below resolve, with the two
// tasks E070 reports for it. It is deliberately absent from eccWorklistXML,
// which holds only modifiable requests — exactly the ECC situation the E071
// fallback exists for.
const (
	releasedRequestNumber = "HFQK901000"
	releasedTaskOne       = "HFQK901001"
	releasedTaskTwo       = "HFQK901002"
)

// dataPreviewXML renders a column-oriented data preview response body — the
// shape RunQuery parses — from a column-name list and row-major data.
func dataPreviewXML(columns []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	fmt.Fprintf(&b, `<dataPreview:totalRows>%d</dataPreview:totalRows>`, len(rows))
	b.WriteString(`<dataPreview:queryExecutionTime>1.0</dataPreview:queryExecutionTime>`)
	for i, name := range columns {
		b.WriteString(`<dataPreview:columns>`)
		fmt.Fprintf(&b, `<dataPreview:metadata dataPreview:name=%q dataPreview:type="C"/>`, name)
		b.WriteString(`<dataPreview:dataSet>`)
		for _, row := range rows {
			fmt.Fprintf(&b, `<dataPreview:data>%s</dataPreview:data>`, row[i])
		}
		b.WriteString(`</dataPreview:dataSet></dataPreview:columns>`)
	}
	b.WriteString(`</dataPreview:tableData>`)
	return b.String()
}

// queryProbe records every SQL statement the client sends to the data preview
// endpoint, so a test can assert both what was asked and — just as important
// for the "ADT first" rule — that nothing was asked at all.
type queryProbe struct {
	mu      sync.Mutex
	queries []string
}

func (p *queryProbe) record(sql string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries = append(p.queries, sql)
}

func (p *queryProbe) all() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.queries...)
}

// newQueryFallbackClient returns a client whose transportrequests endpoint
// always answers with worklist (mirroring the ECC worklist bug) and whose
// data preview endpoint answers via respond, which receives the SQL sent and
// returns an HTTP status plus a body.
func newQueryFallbackClient(t *testing.T, worklist string, respond func(sql string) (int, string)) (adt.Client, *queryProbe) {
	t.Helper()
	probe := &queryProbe{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(r.URL.Path, "/sap/bc/adt/datapreview/freestyle"):
			body, _ := io.ReadAll(r.Body)
			sql := string(body)
			probe.record(sql)
			status, payload := respond(sql)
			w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(payload))
		case strings.HasPrefix(r.URL.Path, "/sap/bc/adt/cts/transportrequests/"):
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(worklist))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	return adt.NewClient(cfg), probe
}

// releasedRequestResponder answers the two fallback queries for
// releasedRequestNumber: E070 yields its two task numbers, E071 yields one
// object recorded at both request and task level (so the dedup upgrade rule
// is exercised) and one recorded only on the second task.
func releasedRequestResponder(t *testing.T) func(string) (int, string) {
	t.Helper()
	return func(sql string) (int, string) {
		switch {
		case strings.Contains(sql, "FROM E070"):
			return http.StatusOK, dataPreviewXML(
				[]string{"TRKORR"},
				[][]string{{releasedTaskOne}, {releasedTaskTwo}},
			)
		case strings.Contains(sql, "FROM E071"):
			return http.StatusOK, dataPreviewXML(
				[]string{"TRKORR", "AS4POS", "PGMID", "OBJECT", "OBJ_NAME"},
				[][]string{
					{releasedRequestNumber, "0001", "R3TR", "PROG", eccOrderRequestObjName},
					{releasedTaskOne, "0003", "R3TR", "PROG", eccOrderRequestObjName},
					{releasedTaskTwo, "0002", "R3TR", "CLAS", eccLockReproObjName},
				},
			)
		default:
			t.Errorf("unexpected SQL: %s", sql)
			return http.StatusInternalServerError, ""
		}
	}
}

// TestGetTransportObjects_AbsentFromWorklist_ResolvesViaE071 is the core of
// Task 4: a transport the ADT worklist does not contain (every released
// request on ECC) still resolves, through E071, with Position and Task
// populated.
func TestGetTransportObjects_AbsentFromWorklist_ResolvesViaE071(t *testing.T) {
	client, probe := newQueryFallbackClient(t, eccWorklistXML, releasedRequestResponder(t))

	objs, err := client.GetTransportObjects(context.Background(), releasedRequestNumber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("got %d objects, want 2: %+v", len(objs), objs)
	}

	// First-seen wins on every field; the request-level row is seen first
	// (ORDER BY TRKORR, AS4POS), so Position stays 0001 while the later task
	// row upgrades the empty Task — the same rule the ADT path applies.
	want := []adt.TransportObject{
		{PgmID: "R3TR", Type: "PROG", Name: eccOrderRequestObjName, Position: "0001", Task: releasedTaskOne},
		{PgmID: "R3TR", Type: "CLAS", Name: eccLockReproObjName, Position: "0002", Task: releasedTaskTwo},
	}
	for i, w := range want {
		if objs[i] != w {
			t.Errorf("object %d: got %+v, want %+v", i, objs[i], w)
		}
	}
	// WBType has no E071 column and must stay empty rather than be guessed.
	for _, o := range objs {
		if o.WBType != "" {
			t.Errorf("object %s: WBType should be empty (no E071 column), got %q", o.Name, o.WBType)
		}
	}

	queries := probe.all()
	if len(queries) != 2 {
		t.Fatalf("expected exactly 2 queries (E070 tasks, E071 objects), got %d: %v", len(queries), queries)
	}
	if !strings.Contains(queries[0], "SELECT TRKORR FROM E070 WHERE STRKORR = '"+releasedRequestNumber+"'") {
		t.Errorf("E070 query: got %q", queries[0])
	}
	for _, number := range []string{releasedRequestNumber, releasedTaskOne, releasedTaskTwo} {
		if !strings.Contains(queries[1], "TRKORR = '"+number+"'") {
			t.Errorf("E071 query missing %s: %q", number, queries[1])
		}
	}
	// OR, not IN: the data preview endpoint is not verified to accept IN on
	// both system families.
	if !strings.Contains(queries[1], "' OR TRKORR = '") {
		t.Errorf("E071 query should combine numbers with OR: %q", queries[1])
	}
	if !strings.Contains(queries[1], "ORDER BY TRKORR, AS4POS") {
		t.Errorf("E071 query should order by TRKORR, AS4POS: %q", queries[1])
	}
	// The release-marker exclusion must live in the statement, where it is
	// visible, and must cover the whole TRKORR disjunction rather than only
	// its last alternative.
	if !strings.Contains(queries[1], "AND PGMID <> 'CORR'") {
		t.Errorf("E071 query should exclude release markers: %q", queries[1])
	}
	if !strings.Contains(queries[1], "WHERE ( TRKORR = '") || !strings.Contains(queries[1], "' ) AND PGMID") {
		t.Errorf("E071 query should parenthesise the TRKORR disjunction: %q", queries[1])
	}
	// The data preview endpoint rejects a descending sort, so neither query
	// may acquire one.
	for i, sql := range queries {
		if strings.Contains(sql, "DESC") {
			t.Errorf("query %d uses DESC, which the data preview endpoint rejects: %q", i, sql)
		}
	}
}

// TestGetTransportObjects_ReleasedRequest_DropsReleaseMarkerRow reproduces the
// shape a genuinely released request has in E071: its tasks are dissolved, so
// the E070 task query returns nothing and every object row sits on the request
// itself — including a PGMID CORR / OBJECT RELE release marker whose OBJ_NAME
// is a packed audit string rather than an object name. That row must never
// reach a caller. The query excludes it; this test additionally proves the
// result is clean even when a server hands it over regardless.
func TestGetTransportObjects_ReleasedRequest_DropsReleaseMarkerRow(t *testing.T) {
	const releasedNumber = "E20K928233"

	client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			// A released request has no tasks left.
			return http.StatusOK, dataPreviewXML([]string{"TRKORR"}, nil)
		}
		return http.StatusOK, dataPreviewXML(
			[]string{"TRKORR", "AS4POS", "PGMID", "OBJECT", "OBJ_NAME"},
			[][]string{
				{releasedNumber, "000001", "CORR", "RELE", "E20K928234 20160702 143007 U13409"},
				{releasedNumber, "000002", "LIMU", "METH", "ZCL_EDM_MIG_GINF              GET_GT_DATA"},
			},
		)
	})

	objs, err := client.GetTransportObjects(context.Background(), releasedNumber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, o := range objs {
		if o.PgmID == "CORR" || o.Type == "RELE" {
			t.Errorf("release marker leaked into the object list: %+v", o)
		}
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want only the LIMU METH row: %+v", len(objs), objs)
	}
	// E071's sub-object granularity is reported as-is, not folded up to the
	// R3TR entry the ADT path would show.
	want := adt.TransportObject{
		PgmID:    "LIMU",
		Type:     "METH",
		Name:     "ZCL_EDM_MIG_GINF              GET_GT_DATA",
		Position: "000002",
	}
	if objs[0] != want {
		t.Errorf("got %+v, want %+v", objs[0], want)
	}

	queries := probe.all()
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %v", queries)
	}
	// A task-less request addresses only itself, and the parentheses must
	// still be there so the exclusion binds correctly.
	if !strings.Contains(queries[1], "WHERE ( TRKORR = '"+releasedNumber+"' ) AND PGMID <> 'CORR'") {
		t.Errorf("E071 query for a task-less request: %q", queries[1])
	}
}

// TestGetTransportObjects_NamespacedNumber_ReachesTheQuery pins that a
// namespaced request number — which legitimately contains "/" — passes
// validation instead of being rejected as unsafe.
func TestGetTransportObjects_NamespacedNumber_ReachesTheQuery(t *testing.T) {
	const namespaced = "/ACCGO/ACMS41709FP00"

	client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			return http.StatusOK, dataPreviewXML([]string{"TRKORR"}, nil)
		}
		return http.StatusOK, dataPreviewXML(
			[]string{"TRKORR", "AS4POS", "PGMID", "OBJECT", "OBJ_NAME"},
			[][]string{{namespaced, "0001", "R3TR", "CLAS", eccLockReproObjName}},
		)
	})

	objs, err := client.GetTransportObjects(context.Background(), namespaced)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 || objs[0].Name != eccLockReproObjName {
		t.Fatalf("got %+v, want the single namespaced-request object", objs)
	}
	// A row on the request itself carries no task attribution.
	if objs[0].Task != "" {
		t.Errorf("request-level row should have no Task, got %q", objs[0].Task)
	}
	if len(probe.all()) != 2 {
		t.Errorf("expected 2 queries, got %v", probe.all())
	}
}

// TestGetTransportObjects_PresentInWorklist_NeverQueries pins the order rule:
// when the ADT response contains the addressed request, the query route is
// not a second opinion and must not be entered at all.
func TestGetTransportObjects_PresentInWorklist_NeverQueries(t *testing.T) {
	client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		t.Errorf("query issued although the ADT path succeeded: %s", sql)
		return http.StatusInternalServerError, ""
	})

	objs, err := client.GetTransportObjects(context.Background(), "HFQK900178")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 || objs[0].Name != eccOrderRequestObjName {
		t.Fatalf("got %+v, want the worklist request's single object", objs)
	}
	if got := probe.all(); len(got) != 0 {
		t.Errorf("expected no queries, got %v", got)
	}
}

// TestGetTransportObjects_InvalidNumber_RejectedBeforeAnyQuery verifies a
// transport number that could escape the SQL literal is rejected by the
// validator, with nothing sent to the data preview endpoint.
func TestGetTransportObjects_InvalidNumber_RejectedBeforeAnyQuery(t *testing.T) {
	for _, number := range []string{
		"HFQK9' OR '1'='1",
		`HFQK900178"`,
		"HFQK9 00178",
		"HFQK901000000000000000000", // longer than E070-TRKORR (CHAR20)
	} {
		t.Run(number, func(t *testing.T) {
			client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
				t.Errorf("query issued for an invalid transport number: %s", sql)
				return http.StatusInternalServerError, ""
			})

			_, err := client.GetTransportObjects(context.Background(), number)
			if err == nil {
				t.Fatal("expected an error for an unsafe transport number")
			}
			if !strings.Contains(err.Error(), "not allowed in a transport query") {
				t.Errorf("error should name the validation failure: %v", err)
			}
			if got := probe.all(); len(got) != 0 {
				t.Errorf("expected no queries, got %v", got)
			}
		})
	}
}

// TestGetTransportObjects_FallbackFails_ErrorNamesBothAttempts verifies the
// composed error lets a caller tell "this request is not on this system"
// apart from "the fallback could not run here".
func TestGetTransportObjects_FallbackFails_ErrorNamesBothAttempts(t *testing.T) {
	client, _ := newQueryFallbackClient(t, eccWorklistXML, func(string) (int, string) {
		return http.StatusForbidden, `<err>no data preview authorisation</err>`
	})

	_, err := client.GetTransportObjects(context.Background(), releasedRequestNumber)
	if err == nil {
		t.Fatal("expected an error when both attempts fail")
	}
	msg := err.Error()
	// The ADT attempt.
	if !strings.Contains(msg, releasedRequestNumber) || !strings.Contains(msg, "transport-organizer worklist") {
		t.Errorf("error should name the ADT attempt: %v", err)
	}
	// The query attempt.
	if !strings.Contains(msg, "E070 task query") {
		t.Errorf("error should name the query attempt: %v", err)
	}
}
