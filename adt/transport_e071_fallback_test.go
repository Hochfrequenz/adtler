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
	releasedRequestNumber = "DEVK901000"
	releasedTaskOne       = "DEVK901001"
	releasedTaskTwo       = "DEVK901002"
)

// dataPreviewXML renders a column-oriented data preview response body — the
// shape RunQuery parses — from a column-name list and row-major data.
// TotalRows is reported as len(rows) — this is the "server reports only the
// returned count" convention this package's fixtures use throughout, which is
// exactly why the truncation check in getTransportObjectsViaQuery cannot rely
// on TotalRows alone (see e071ObjectQueryMaxRows's doc comment). Use
// dataPreviewXMLWithTotal to report a different TotalRows.
func dataPreviewXML(columns []string, rows [][]string) string {
	return dataPreviewXMLWithTotal(columns, rows, len(rows))
}

// dataPreviewXMLWithTotal is dataPreviewXML with an explicit, independently
// controlled TotalRows — for pinning the truncation check's TotalRows-based
// condition separately from its returned-row-count condition.
func dataPreviewXMLWithTotal(columns []string, rows [][]string, totalRows int) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">`)
	fmt.Fprintf(&b, `<dataPreview:totalRows>%d</dataPreview:totalRows>`, totalRows)
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

// e070Columns is the column list the widened E070 request/task query selects.
// A returned row is either the request's own header row (STRKORR empty) or one
// of its tasks (STRKORR = the request number).
var e070Columns = []string{"TRKORR", "STRKORR"}

// releasedRequestResponder answers the two fallback queries for
// releasedRequestNumber: E070 yields the request's own header row plus its two
// tasks, E071 yields one object recorded at both request and task level (so
// the dedup upgrade rule is exercised) and one recorded only on the second
// task. extraE070Rows are appended to the E070 result, so a test can inject a
// hostile or malformed TRKORR without restating the whole responder.
func releasedRequestResponder(t *testing.T, extraE070Rows ...[]string) func(string) (int, string) {
	t.Helper()
	return func(sql string) (int, string) {
		switch {
		case strings.Contains(sql, "FROM E070"):
			rows := [][]string{
				{releasedRequestNumber, ""},
				{releasedTaskOne, releasedRequestNumber},
				{releasedTaskTwo, releasedRequestNumber},
			}
			rows = append(rows, extraE070Rows...)
			return http.StatusOK, dataPreviewXML(e070Columns, rows)
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
	// One E070 query answers both "does this request exist here" (its own
	// header row, TRKORR = the number) and "what are its tasks" (STRKORR =
	// the number), so no extra round trip is needed to tell an absent request
	// from an empty one.
	if !strings.Contains(queries[0], "SELECT TRKORR, STRKORR FROM E070 WHERE TRKORR = '"+releasedRequestNumber+
		"' OR STRKORR = '"+releasedRequestNumber+"'") {
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
	const releasedNumber = "DEVK900123"

	client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			// A released request has no tasks left — only its own header row.
			return http.StatusOK, dataPreviewXML(e070Columns, [][]string{{releasedNumber, ""}})
		}
		return http.StatusOK, dataPreviewXML(
			[]string{"TRKORR", "AS4POS", "PGMID", "OBJECT", "OBJ_NAME"},
			[][]string{
				{releasedNumber, "000001", "CORR", "RELE", "DEVK900124 20240101 120000 TESTUSER1"},
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
	const namespaced = "/ZDEMO/TESTOBJ001"

	client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			// The request's own header row, no tasks.
			return http.StatusOK, dataPreviewXML(e070Columns, [][]string{{namespaced, ""}})
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

	objs, err := client.GetTransportObjects(context.Background(), "DEVK900178")
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
		"DEVK9' OR '1'='1",
		`DEVK900178"`,
		"DEVK9 00178",
		"DEVK901000000000000000000", // longer than E070-TRKORR (CHAR20)
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
	if !strings.Contains(msg, "E070 request/task query") {
		t.Errorf("error should name the query attempt: %v", err)
	}
}

// TestGetTransportObjects_LowercaseNumber_UppercasedForTheQuery pins that the
// fallback agrees with the ADT path on case. The ADT path matches
// case-insensitively on purpose (matchesTransportNumber), but SAP stores
// TRKORR uppercase and Open SQL "=" on CHAR is case-sensitive, so a verbatim
// lowercase number would match no row and the fallback would answer "no
// objects" instead of the request's contents — a disagreement that is
// invisible to the caller, because an empty list is a successful result.
func TestGetTransportObjects_LowercaseNumber_UppercasedForTheQuery(t *testing.T) {
	client, probe := newQueryFallbackClient(t, eccWorklistXML, releasedRequestResponder(t))

	objs, err := client.GetTransportObjects(context.Background(), strings.ToLower(releasedRequestNumber))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("lowercase number resolved to %d objects, want the same 2 as the uppercase form: %+v", len(objs), objs)
	}
	// Task attribution must use the uppercased number too, or the request's
	// own row would look like a task row and be attributed to itself.
	if objs[0].Task != releasedTaskOne {
		t.Errorf("object 0 Task: got %q, want %q", objs[0].Task, releasedTaskOne)
	}

	for i, sql := range probe.all() {
		if strings.Contains(sql, strings.ToLower(releasedRequestNumber)) {
			t.Errorf("query %d interpolated the number in lowercase: %q", i, sql)
		}
		if !strings.Contains(sql, releasedRequestNumber) {
			t.Errorf("query %d should use the uppercased number: %q", i, sql)
		}
	}
}

// TestGetTransportObjects_NoE070Entry_ReportsAbsentNotEmpty pins Task 2's
// contract on the fallback path: a request that is on neither the worklist nor
// E070 must produce absentTransportError, not a successful empty object list.
// Without this, a typo'd or foreign transport number gives RollbackTransport
// an empty work list, which it reports as a clean success. The CORR exclusion
// makes the row count useless for telling the two cases apart — a real
// released request and a nonexistent one both return zero object rows — which
// is why the existence proof comes from E070 instead.
func TestGetTransportObjects_NoE070Entry_ReportsAbsentNotEmpty(t *testing.T) {
	client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			return http.StatusOK, dataPreviewXML(e070Columns, nil)
		}
		t.Errorf("E071 must not be queried for a request with no E070 entry: %s", sql)
		return http.StatusInternalServerError, ""
	})

	objs, err := client.GetTransportObjects(context.Background(), "DEVK999999")
	if err == nil {
		t.Fatalf("expected an absent error, got a successful result: %+v", objs)
	}
	if objs != nil {
		t.Errorf("expected no objects alongside the error, got %+v", objs)
	}
	if !strings.Contains(err.Error(), "DEVK999999") ||
		!strings.Contains(err.Error(), "transport-organizer worklist") ||
		!strings.Contains(err.Error(), "no E070 entry on this system either") {
		t.Errorf("error should say the request is absent from both sources: %v", err)
	}
	// Only the E070 existence query runs; there is nothing to ask E071 about.
	if got := probe.all(); len(got) != 1 {
		t.Errorf("expected exactly 1 query, got %v", got)
	}
}

// TestGetTransportObjects_NoE070EntryNoColumnMetadata_ReportsAbsentNotMalformed
// pins transportQueryNumbers' check order: a server answering an absent
// transport with an empty result set that also carries no column metadata at
// all must still report absentTransportError, not "no TRKORR column" — the
// zero-rows check runs before the column lookup, because the empty/absent
// case is the expected shape of this answer, not evidence of a malformed
// response.
func TestGetTransportObjects_NoE070EntryNoColumnMetadata_ReportsAbsentNotMalformed(t *testing.T) {
	client, probe := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			return http.StatusOK, dataPreviewXML(nil, nil)
		}
		t.Errorf("E071 must not be queried when the E070 existence check reports absent: %s", sql)
		return http.StatusInternalServerError, ""
	})

	_, err := client.GetTransportObjects(context.Background(), "DEVK999999")
	if err == nil {
		t.Fatal("expected an absent error, got a successful result")
	}
	if strings.Contains(err.Error(), "no TRKORR column") {
		t.Errorf("error should report absence, not missing column metadata: %v", err)
	}
	if !strings.Contains(err.Error(), "DEVK999999") ||
		!strings.Contains(err.Error(), "no E070 entry on this system either") {
		t.Errorf("error should say the request is absent from both sources: %v", err)
	}
	if got := probe.all(); len(got) != 1 {
		t.Errorf("expected exactly 1 query, got %v", got)
	}
}

// TestGetTransportObjects_HostileTaskNumberFromE070_NeverReachesTheQuery
// covers the second validation site: task numbers arrive from the server and
// are interpolated into the E071 statement exactly like the caller's own
// number, so they are re-validated. A malformed row must be dropped without
// taking the legitimate tasks down with it.
func TestGetTransportObjects_HostileTaskNumberFromE070_NeverReachesTheQuery(t *testing.T) {
	const hostileTask = `T' OR '1'='1`

	client, probe := newQueryFallbackClient(t, eccWorklistXML, releasedRequestResponder(t,
		[]string{hostileTask, releasedRequestNumber},
	))

	if _, err := client.GetTransportObjects(context.Background(), releasedRequestNumber); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	queries := probe.all()
	if len(queries) != 2 {
		t.Fatalf("expected 2 queries, got %v", queries)
	}
	if strings.Contains(queries[1], hostileTask) || strings.Contains(queries[1], "'1'='1") {
		t.Errorf("hostile task number reached the E071 statement: %q", queries[1])
	}
	// The legitimate tasks must survive — the guard drops the bad row, not
	// the whole result.
	for _, number := range []string{releasedRequestNumber, releasedTaskOne, releasedTaskTwo} {
		if !strings.Contains(queries[1], "TRKORR = '"+number+"'") {
			t.Errorf("E071 query lost legitimate number %s: %q", number, queries[1])
		}
	}
}

// e071QueryCap mirrors the unexported e071ObjectQueryMaxRows constant
// (adt/transport.go) so this test can build a response that reaches it
// without depending on package-internal access.
const e071QueryCap = 5000

// TestGetTransportObjects_E071RowCountAtCap_ErrorsInsteadOfTruncating pins
// that the E071 object query refuses to answer once the number of rows
// actually returned reaches the server-side cap, rather than silently
// handing back a list that looks complete but may not be. The row count is
// the primary signal (see e071ObjectQueryMaxRows's doc comment for why); this
// fixture also reports TotalRows == len(rows), the same convention every
// other fixture in this file uses, so this test would still catch the
// truncation even if the TotalRows-based condition were removed.
func TestGetTransportObjects_E071RowCountAtCap_ErrorsInsteadOfTruncating(t *testing.T) {
	rows := make([][]string, e071QueryCap)
	for i := range rows {
		rows[i] = []string{releasedRequestNumber, fmt.Sprintf("%06d", i+1), "R3TR", "PROG", fmt.Sprintf("ZPROG%04d", i)}
	}

	client, _ := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			// A task-less request: every row addresses the request itself.
			return http.StatusOK, dataPreviewXML(e070Columns, [][]string{{releasedRequestNumber, ""}})
		}
		return http.StatusOK, dataPreviewXML(
			[]string{"TRKORR", "AS4POS", "PGMID", "OBJECT", "OBJ_NAME"},
			rows,
		)
	})

	objs, err := client.GetTransportObjects(context.Background(), releasedRequestNumber)
	if err == nil {
		t.Fatalf("expected an error when the E071 query returns exactly the %d-row cap, got %d objects", e071QueryCap, len(objs))
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("%d", e071QueryCap)) {
		t.Errorf("error should name the cap: %v", err)
	}
}

// TestGetTransportObjects_E071TotalRowsAboveCap_ErrorsEvenWithFewerReturnedRows
// pins the other half of the truncation check: even when only a handful of
// rows come back, a server-reported TotalRows at or above the cap must still
// be treated as "this list may be truncated", independently of the returned
// row count.
func TestGetTransportObjects_E071TotalRowsAboveCap_ErrorsEvenWithFewerReturnedRows(t *testing.T) {
	client, _ := newQueryFallbackClient(t, eccWorklistXML, func(sql string) (int, string) {
		if strings.Contains(sql, "FROM E070") {
			return http.StatusOK, dataPreviewXML(e070Columns, [][]string{{releasedRequestNumber, ""}})
		}
		return http.StatusOK, dataPreviewXMLWithTotal(
			[]string{"TRKORR", "AS4POS", "PGMID", "OBJECT", "OBJ_NAME"},
			[][]string{{releasedRequestNumber, "0001", "R3TR", "PROG", eccOrderRequestObjName}},
			e071QueryCap, // reported total, far above the single row actually returned
		)
	})

	objs, err := client.GetTransportObjects(context.Background(), releasedRequestNumber)
	if err == nil {
		t.Fatalf("expected an error when TotalRows reports the cap even though only 1 row was returned, got %d objects", len(objs))
	}
}
