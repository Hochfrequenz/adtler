package adt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	createdTestRequest = "AAAK900100"
	createdTestTask    = "AAAK900101"
	createdTestProgURI = "/sap/bc/adt/programs/programs/"
	createdTestCSRF    = "/sap/bc/adt/discovery"
	createdTestChanged = "ZCHANGED"
	createdTestNewA    = "ZNEW_A"
	createdTestNewB    = "ZNEW_B"

	createdTestLockAction = "LOCK"
)

func TestCreatedByTransport(t *testing.T) {
	transports := []string{createdTestRequest, createdTestTask}
	cases := []struct {
		name     string
		versions []VersionInfo
		want     bool
	}{
		{"empty history", nil, false},
		{"only the task", []VersionInfo{{Transport: createdTestTask}}, true},
		{"request and task", []VersionInfo{{Transport: createdTestTask}, {Transport: createdTestRequest}}, true},
		{"lowercase server value", []VersionInfo{{Transport: strings.ToLower(createdTestTask)}}, true},
		{"an older version of another request", []VersionInfo{{Transport: createdTestTask}, {Transport: "AAAK900001"}}, false},
		{"a newer version of another request", []VersionInfo{{Transport: "AAAK900200"}, {Transport: createdTestTask}}, false},
		{"a local version without a request", []VersionInfo{{Transport: createdTestTask}, {Transport: ""}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := createdByTransport(tc.versions, transports); got != tc.want {
				t.Errorf("createdByTransport = %v, want %v", got, tc.want)
			}
		})
	}
}

// createdTestServer fakes a transport with the given programs. histories maps
// a program name to the transports of its version-history entries, newest
// first; deleteStatus returns the status of the n-th (1-based) DELETE of a
// program, 0 meaning 200.
type createdTestServer struct {
	programs     []string
	extraEntries string // raw tm:abap_object elements appended to the transport
	histories    map[string][]string
	activateFail bool
	deleteStatus func(name string, attempt int) int
	// srcSystem maps a program to its TADIR source system; a program not
	// listed originates in the request's system AAA. queryStatus, when not
	// 0, is the status of every TADIR query.
	srcSystem   map[string]string
	queryStatus int

	mu       sync.Mutex
	calls    []string
	deletes  map[string]int
	deleteCo []string
}

func (s *createdTestServer) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *createdTestServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		name := strings.ToUpper(strings.TrimPrefix(path, createdTestProgURI))
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[:i]
		}
		switch {
		case path == createdTestCSRF:
			w.Header().Set("X-CSRF-Token", "token")
		case path == "/sap/bc/adt/cts/transportrequests/"+createdTestRequest:
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"><tm:request tm:number="` + createdTestRequest + `">`)
			for _, p := range s.programs {
				b.WriteString(`<tm:abap_object tm:pgmid="R3TR" tm:type="PROG" tm:name="` + p + `"/>`)
			}
			b.WriteString(s.extraEntries)
			b.WriteString(`<tm:task tm:number="` + createdTestTask + `"/></tm:request></tm:root>`)
			_, _ = w.Write([]byte(b.String()))
		case strings.HasSuffix(path, "/source/main/versions"):
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">`)
			for i, tr := range s.histories[name] {
				b.WriteString(`<entry><content src="/versions/` + name + `/` + string(rune('0'+i)) + `"/>`)
				b.WriteString(`<link rel="http://www.sap.com/adt/relations/transport" name="` + tr + `"/></entry>`)
			}
			b.WriteString(`</feed>`)
			_, _ = w.Write([]byte(b.String()))
		case strings.HasPrefix(path, "/versions/"):
			_, _ = w.Write([]byte("REPORT zold."))
		case r.URL.Query().Get("_action") == createdTestLockAction:
			s.record("lock " + name)
			_, _ = w.Write([]byte("lock-handle"))
		case r.URL.Query().Get("_action") == "UNLOCK":
			s.record("unlock " + name)
		case strings.HasSuffix(path, "/source/main") && r.Method == http.MethodGet:
			w.Header().Set("ETag", `"current"`)
			_, _ = w.Write([]byte("REPORT zcurrent."))
		case strings.HasSuffix(path, "/source/main") && r.Method == http.MethodPut:
			s.record("set-source " + name)
		case path == "/sap/bc/adt/activation" && r.Method == http.MethodPost:
			s.record("activate")
			if s.activateFail {
				_, _ = w.Write([]byte(`<chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><msg type="E"><shortText><txt>broken</txt></shortText></msg></chkl:messages>`))
			}
		case path == "/sap/bc/adt/datapreview/freestyle":
			s.record("query")
			if s.queryStatus != 0 {
				w.WriteHeader(s.queryStatus)
				return
			}
			body, _ := io.ReadAll(r.Body)
			sql := string(body)
			obj := sql[strings.Index(sql, "OBJ_NAME = '")+len("OBJ_NAME = '"):]
			obj = obj[:strings.IndexByte(obj, '\'')]
			src, ok := s.srcSystem[obj]
			if !ok {
				src = "AAA"
			}
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">` +
				`<dataPreview:totalRows>1</dataPreview:totalRows><dataPreview:columns><dataPreview:metadata dataPreview:name="SRCSYSTEM" dataPreview:type="C"/>` +
				`<dataPreview:dataSet><dataPreview:data>` + src + `</dataPreview:data></dataPreview:dataSet></dataPreview:columns></dataPreview:tableData>`))
		case path == "/sap/bc/adt/activation/inactiveobjects":
			_, _ = w.Write([]byte(`<root/>`))
		case strings.HasPrefix(path, createdTestProgURI) && r.Method == http.MethodGet:
			w.Header().Set("ETag", `"obj"`)
			_, _ = w.Write([]byte(`<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs"/>`))
		case strings.HasPrefix(path, createdTestProgURI) && r.Method == http.MethodDelete:
			s.mu.Lock()
			if s.deletes == nil {
				s.deletes = map[string]int{}
			}
			s.deletes[name]++
			attempt := s.deletes[name]
			s.deleteCo = append(s.deleteCo, r.URL.Query().Get("corrNr"))
			s.mu.Unlock()
			s.record("delete " + name)
			if s.deleteStatus != nil {
				if st := s.deleteStatus(name, attempt); st != 0 {
					w.WriteHeader(st)
					return
				}
			}
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}
}

func (s *createdTestServer) run(t *testing.T, opts RollbackOptions) *RollbackResult {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	client := NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	result, err := client.RollbackTransportWithOptions(context.Background(), createdTestRequest, opts)
	if err != nil {
		t.Fatalf("RollbackTransportWithOptions: %v", err)
	}
	return result
}

func (s *createdTestServer) deleteCalls() []string {
	var out []string
	for _, c := range s.calls {
		if strings.HasPrefix(c, "delete ") {
			out = append(out, c)
		}
	}
	return out
}

func names(entries []RollbackEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

func TestRollbackTransport_CreatedObjectIsReportedNotFailed(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestChanged, createdTestNewA},
		histories: map[string][]string{
			createdTestChanged: {createdTestTask, "AAAK900001"},
			createdTestNewA:    {createdTestTask},
		},
	}
	result := s.run(t, RollbackOptions{})
	if got := names(result.Restored); !reflect.DeepEqual(got, []string{createdTestChanged}) {
		t.Errorf("Restored = %v, want [%s]", got, createdTestChanged)
	}
	if got := names(result.Created); !reflect.DeepEqual(got, []string{createdTestNewA}) {
		t.Errorf("Created = %v, want [%s]", got, createdTestNewA)
	}
	if len(result.Failed) != 0 || len(result.Deleted) != 0 {
		t.Errorf("Failed = %v, Deleted = %v, want both empty", result.Failed, result.Deleted)
	}
	if d := s.deleteCalls(); len(d) != 0 {
		t.Errorf("DELETE sent without DeleteCreatedObjects: %v", d)
	}
}

func TestRollbackTransportWithOptions_DeletesCreatedAfterRestoring(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestNewA, createdTestChanged},
		histories: map[string][]string{
			createdTestChanged: {createdTestTask, "AAAK900001"},
			createdTestNewA:    {createdTestTask, createdTestRequest},
		},
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if got := names(result.Deleted); !reflect.DeepEqual(got, []string{createdTestNewA}) {
		t.Errorf("Deleted = %v, want [%s]", got, createdTestNewA)
	}
	if got := names(result.Restored); !reflect.DeepEqual(got, []string{createdTestChanged}) {
		t.Errorf("Restored = %v, want [%s]", got, createdTestChanged)
	}
	if len(result.Failed) != 0 || len(result.Created) != 0 {
		t.Errorf("Failed = %v, Created = %v, want both empty", result.Failed, result.Created)
	}
	// The created program comes first in the transport, but is deleted only
	// after the changed one has been restored and activated.
	if last := s.calls[len(s.calls)-1]; last != "delete "+createdTestNewA {
		t.Errorf("calls = %v, want the DELETE last", s.calls)
	}
	if !reflect.DeepEqual(s.deleteCo, []string{createdTestRequest}) {
		t.Errorf("DELETE corrNr = %v, want the request %s", s.deleteCo, createdTestRequest)
	}
}

func TestRollbackTransportWithOptions_NeverDeletesWithForeignVersion(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestNewA},
		// A local version without a request, older than the transport's: no
		// version to restore, but the object existed before the transport.
		histories: map[string][]string{createdTestNewA: {createdTestTask, ""}},
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if d := s.deleteCalls(); len(d) != 0 {
		t.Errorf("DELETE sent for an object with a version outside the transport: %v", d)
	}
	// The local version is the one before the transport, so it is restored.
	if got := names(result.Restored); !reflect.DeepEqual(got, []string{createdTestNewA}) {
		t.Errorf("Restored = %v, want [%s]", got, createdTestNewA)
	}
	if len(result.Created) != 0 || len(result.Deleted) != 0 {
		t.Errorf("Created = %v, Deleted = %v, want both empty", result.Created, result.Deleted)
	}
}

func TestRollbackTransportWithOptions_EmptyHistoryIsFailedNotCreated(t *testing.T) {
	s := &createdTestServer{
		programs:  []string{createdTestNewA},
		histories: map[string][]string{},
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if d := s.deleteCalls(); len(d) != 0 {
		t.Errorf("DELETE sent for an object without version history: %v", d)
	}
	if len(result.Failed) != 1 || !strings.Contains(result.Failed[0].Reason, "no version history") {
		t.Errorf("Failed = %v, want %s with a no-version-history reason", result.Failed, createdTestNewA)
	}
}

func TestRollbackTransportWithOptions_FailedRestoreKeepsCreated(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestChanged, createdTestNewA},
		histories: map[string][]string{
			createdTestChanged: {createdTestTask, "AAAK900001"},
			createdTestNewA:    {createdTestTask},
		},
		activateFail: true,
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if d := s.deleteCalls(); len(d) != 0 {
		t.Errorf("DELETE sent although a restore failed: %v", d)
	}
	if got := names(result.Failed); !reflect.DeepEqual(got, []string{createdTestChanged}) {
		t.Errorf("Failed = %v, want [%s]", got, createdTestChanged)
	}
	if len(result.Created) != 1 || result.Created[0].Reason != rollbackReasonCreatedRestoreErr {
		t.Errorf("Created = %v, want %s with reason %q", result.Created, createdTestNewA, rollbackReasonCreatedRestoreErr)
	}
}

func TestRollbackTransportWithOptions_DeletesInRounds(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestNewA, createdTestNewB},
		histories: map[string][]string{
			createdTestNewA: {createdTestTask},
			createdTestNewB: {createdTestTask},
		},
		// A is in use by B until B is gone.
		deleteStatus: func(name string, attempt int) int {
			if name == createdTestNewA && attempt == 1 {
				return http.StatusBadRequest
			}
			return 0
		},
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if got := names(result.Deleted); !reflect.DeepEqual(got, []string{createdTestNewB, createdTestNewA}) {
		t.Errorf("Deleted = %v, want [%s %s]", got, createdTestNewB, createdTestNewA)
	}
	if len(result.Failed) != 0 {
		t.Errorf("Failed = %v, want none", result.Failed)
	}
}

func TestRollbackTransportWithOptions_StopsWhenARoundDeletesNothing(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestNewA, createdTestNewB},
		histories: map[string][]string{
			createdTestNewA: {createdTestTask},
			createdTestNewB: {createdTestTask},
		},
		deleteStatus: func(name string, _ int) int {
			if name == createdTestNewA {
				return http.StatusBadRequest
			}
			return 0
		},
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if got := names(result.Deleted); !reflect.DeepEqual(got, []string{createdTestNewB}) {
		t.Errorf("Deleted = %v, want [%s]", got, createdTestNewB)
	}
	if len(result.Failed) != 1 || !strings.Contains(result.Failed[0].Reason, "delete:") {
		t.Errorf("Failed = %v, want %s with its delete error", result.Failed, createdTestNewA)
	}
	if got := s.deletes[createdTestNewA]; got != 2 {
		t.Errorf("%s DELETE attempts = %d, want 2 (one per round)", createdTestNewA, got)
	}
}

func TestRollbackTransportWithOptions_401DuringDeleteStopsSending(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestNewA, createdTestNewB},
		histories: map[string][]string{
			createdTestNewA: {createdTestTask},
			createdTestNewB: {createdTestTask},
		},
		deleteStatus: func(string, int) int { return http.StatusUnauthorized },
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if d := s.deleteCalls(); !reflect.DeepEqual(d, []string{"delete " + createdTestNewA}) {
		t.Errorf("DELETE calls = %v, want exactly one before the 401 stopped the rollback", d)
	}
	if len(result.Failed) != 2 {
		t.Fatalf("Failed = %v, want both programs", result.Failed)
	}
	for _, f := range result.Failed {
		if !strings.Contains(f.Reason, "401") {
			t.Errorf("%s: reason %q does not name the 401", f.Name, f.Reason)
		}
	}
}

func TestRollbackTransportWithOptions_ImportedObjectIsNotDeleted(t *testing.T) {
	s := &createdTestServer{
		programs:  []string{createdTestNewA, createdTestNewB},
		histories: map[string][]string{createdTestNewA: {createdTestTask}, createdTestNewB: {createdTestTask}},
		// A came in by import: no version on import, so its history shows
		// only this request, but it originates in another system.
		srcSystem: map[string]string{createdTestNewA: "BBB"},
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if d := s.deleteCalls(); !reflect.DeepEqual(d, []string{"delete " + createdTestNewB}) {
		t.Errorf("DELETE calls = %v, want only %s", d, createdTestNewB)
	}
	if got := names(result.Created); !reflect.DeepEqual(got, []string{createdTestNewA}) {
		t.Errorf("Created = %v, want [%s]", got, createdTestNewA)
	}
	if got := names(result.Deleted); !reflect.DeepEqual(got, []string{createdTestNewB}) {
		t.Errorf("Deleted = %v, want [%s]", got, createdTestNewB)
	}
}

func TestRollbackTransportWithOptions_UnreadableOriginIsNotDeleted(t *testing.T) {
	s := &createdTestServer{
		programs:    []string{createdTestNewA},
		histories:   map[string][]string{createdTestNewA: {createdTestTask}},
		queryStatus: http.StatusForbidden,
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if d := s.deleteCalls(); len(d) != 0 {
		t.Errorf("DELETE sent although the origin could not be read: %v", d)
	}
	if got := names(result.Created); !reflect.DeepEqual(got, []string{createdTestNewA}) {
		t.Errorf("Created = %v, want [%s]", got, createdTestNewA)
	}
}

func TestRollbackTransportWithOptions_SubObjectEntriesBlockDeletion(t *testing.T) {
	s := &createdTestServer{
		programs:  []string{createdTestNewA},
		histories: map[string][]string{createdTestNewA: {createdTestTask}},
		// A method of an existing class, changed in the same request, is not
		// rolled back (#134) and may use the created program.
		extraEntries: `<tm:abap_object tm:pgmid="LIMU" tm:type="METH" tm:name="ZCL_EXISTING                  DO_IT"/>`,
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if d := s.deleteCalls(); len(d) != 0 {
		t.Errorf("DELETE sent although a sub-object entry was not rolled back: %v", d)
	}
	if len(result.Created) != 1 || result.Created[0].Reason != rollbackReasonCreatedSubObjects {
		t.Errorf("Created = %v, want %s with reason %q", result.Created, createdTestNewA, rollbackReasonCreatedSubObjects)
	}
}

func TestRollbackTransportWithOptions_AlreadyGoneCountsAsDeleted(t *testing.T) {
	s := &createdTestServer{
		programs:     []string{createdTestNewA},
		histories:    map[string][]string{createdTestNewA: {createdTestTask}},
		deleteStatus: func(string, int) int { return http.StatusNotFound },
	}
	result := s.run(t, RollbackOptions{DeleteCreatedObjects: true})
	if got := names(result.Deleted); !reflect.DeepEqual(got, []string{createdTestNewA}) {
		t.Errorf("Deleted = %v, want [%s]; failed=%v", got, createdTestNewA, result.Failed)
	}
}

func TestRollbackTransportWithOptions_401DuringRestoreDeletesNothing(t *testing.T) {
	s := &createdTestServer{
		programs: []string{createdTestNewA, createdTestChanged},
		histories: map[string][]string{
			createdTestChanged: {createdTestTask, "AAAK900001"},
			createdTestNewA:    {createdTestTask},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("_action") == createdTestLockAction {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		s.handler(t)(w, r)
	}))
	t.Cleanup(srv.Close)
	client := NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	result, err := client.RollbackTransportWithOptions(context.Background(), createdTestRequest, RollbackOptions{DeleteCreatedObjects: true})
	if err != nil {
		t.Fatalf("RollbackTransportWithOptions: %v", err)
	}
	if d := s.deleteCalls(); len(d) != 0 {
		t.Errorf("DELETE sent after a 401: %v", d)
	}
	for _, c := range s.calls {
		if c == "query" {
			t.Errorf("TADIR query sent after a 401")
		}
	}
	if len(result.Failed) != 2 {
		t.Fatalf("Failed = %v, want both programs", result.Failed)
	}
	for _, f := range result.Failed {
		if !strings.Contains(f.Reason, "401") {
			t.Errorf("%s: reason %q does not name the 401", f.Name, f.Reason)
		}
	}
}
