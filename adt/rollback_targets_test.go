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
	limuTestRequest  = "AAAK900100"
	limuTestTask     = "AAAK900101"
	limuTestOlder    = "AAAK900001"
	limuTestClassURI = "/sap/bc/adt/oo/classes/zcl_x"
	limuTestFeedMain = limuTestClassURI + "/includes/main/versions"
	limuTestFeedDefs = limuTestClassURI + "/includes/definitions/versions"
	limuTestPutMain  = "PUT " + limuTestClassURI + "/source/main"
	limuTestTRDIR    = "TRDIR WHERE NAME = '"
	limuTestTADIRPrg = "OBJECT = 'PROG' AND OBJ_NAME = '"
	limuTestTADIRFgr = "OBJECT = 'FUGR' AND OBJ_NAME = '"
	limuTestTFDIR    = "TFDIR WHERE FUNCNAME = '"

	testActionUnlock        = "UNLOCK"
	testActivationPath      = "/sap/bc/adt/activation"
	testInactiveObjectsPath = "/sap/bc/adt/activation/inactiveobjects"
)

// limuTestFeed is one version-history entry: the request or task that wrote
// it and the path its source is served under.
type limuTestFeed struct{ transport, content string }

// limuTestServer fakes the endpoints a rollback of sub-object entries uses.
// queries maps a fragment of the SQL text to the first column of the single
// row returned; a fragment mapped to "" returns no row. feeds maps a version
// feed path to its entries, newest first; any other feed answers 404.
type limuTestServer struct {
	entries     string
	queries     map[string]string
	feeds       map[string][]limuTestFeed
	queryStatus int

	mu    sync.Mutex
	calls []string
	puts  map[string]string // PUT path → body
}

func (s *limuTestServer) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *limuTestServer) callsWithPrefix(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (s *limuTestServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == createdTestCSRF:
			w.Header().Set("X-CSRF-Token", "token")
		case path == "/sap/bc/adt/cts/transportrequests/"+limuTestRequest:
			_, _ = w.Write([]byte(`<?xml version="1.0"?><tm:root xmlns:tm="http://www.sap.com/cts/adt/tm"><tm:request tm:number="` +
				limuTestRequest + `">` + s.entries + `<tm:task tm:number="` + limuTestTask + `"/></tm:request></tm:root>`))
		case path == "/sap/bc/adt/datapreview/freestyle":
			s.record("query")
			if s.queryStatus != 0 {
				w.WriteHeader(s.queryStatus)
				return
			}
			body, _ := io.ReadAll(r.Body)
			s.writeQuery(t, w, string(body))
		case strings.HasSuffix(path, "/versions"):
			entries, ok := s.feeds[path]
			if !ok {
				http.NotFound(w, r)
				return
			}
			var b strings.Builder
			b.WriteString(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">`)
			for _, e := range entries {
				b.WriteString(`<entry><content src="` + e.content + `"/>`)
				b.WriteString(`<link rel="http://www.sap.com/adt/relations/transport" name="` + e.transport + `"/></entry>`)
			}
			b.WriteString(`</feed>`)
			_, _ = w.Write([]byte(b.String()))
		case strings.HasPrefix(path, "/content/"):
			_, _ = w.Write([]byte("source of " + strings.TrimPrefix(path, "/content/")))
		case r.URL.Query().Get("_action") == createdTestLockAction:
			s.record("lock " + path)
			_, _ = w.Write([]byte("lock-handle"))
		case r.URL.Query().Get("_action") == testActionUnlock:
			s.record("unlock " + path)
		case r.Method == http.MethodGet && strings.HasSuffix(path, "/source/main"):
			w.Header().Set("ETag", `"current"`)
			_, _ = w.Write([]byte("current"))
		case r.Method == http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			s.mu.Lock()
			if s.puts == nil {
				s.puts = map[string]string{}
			}
			s.puts[path] = string(body)
			s.mu.Unlock()
			s.record("PUT " + path)
		case path == testActivationPath && r.Method == http.MethodPost:
			s.record("activate")
		case path == testInactiveObjectsPath:
			_, _ = w.Write([]byte(`<root/>`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}
}

func (s *limuTestServer) writeQuery(t *testing.T, w http.ResponseWriter, sql string) {
	for fragment, value := range s.queries {
		if !strings.Contains(sql, fragment) {
			continue
		}
		rows, data := "0", ""
		if value != "" {
			rows, data = "1", `<dataPreview:data>`+value+`</dataPreview:data>`
		}
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">` +
			`<dataPreview:totalRows>` + rows + `</dataPreview:totalRows><dataPreview:columns><dataPreview:metadata dataPreview:name="C" dataPreview:type="C"/>` +
			`<dataPreview:dataSet>` + data + `</dataPreview:dataSet></dataPreview:columns></dataPreview:tableData>`))
		return
	}
	t.Errorf("unexpected query: %s", sql)
	w.WriteHeader(http.StatusBadRequest)
}

func (s *limuTestServer) client(t *testing.T) *httpClient {
	t.Helper()
	srv := httptest.NewServer(s.handler(t))
	t.Cleanup(srv.Close)
	return NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}).(*httpClient)
}

func (s *limuTestServer) rollback(t *testing.T, opts RollbackOptions) *RollbackResult {
	t.Helper()
	result, err := s.client(t).RollbackTransportWithOptions(context.Background(), limuTestRequest, opts)
	if err != nil {
		t.Fatalf("RollbackTransportWithOptions: %v", err)
	}
	return result
}

func limuEntry(pgmid, objType, name string) string {
	return `<tm:abap_object tm:pgmid="` + pgmid + `" tm:type="` + objType + `" tm:name="` + name + `"/>`
}

// TestRollbackTransport_MethodEntriesRestoreTheClass is the #134 case: a
// released request records the methods and sections it changed, not the
// class. They must restore the class's global source, once, from the history
// of its main include, and leave its local includes alone. Before the fix
// every one of these entries was Skipped as "not R3TR".
func TestRollbackTransport_MethodEntriesRestoreTheClass(t *testing.T) {
	s := &limuTestServer{
		entries: limuEntry(pgmIDCorr, "RELE", limuTestTask+" 20250526 112849 USERA") +
			limuEntry("LIMU", "METH", "ZCL_X                         METHOD_A") +
			limuEntry("LIMU", "METH", "ZCL_X                         METHOD_B") +
			limuEntry("LIMU", "CPUB", "ZCL_X"),
		feeds: map[string][]limuTestFeed{
			limuTestFeedMain: {{limuTestTask, "/content/main-new"}, {limuTestOlder, "/content/main-old"}},
			limuTestFeedDefs: {{limuTestOlder, "/content/defs-old"}},
		},
	}
	result := s.rollback(t, RollbackOptions{})

	want := []RollbackEntry{{Type: "CLAS", Name: "ZCL_X"}}
	if !reflect.DeepEqual(result.Restored, want) {
		t.Errorf("Restored = %+v, want %+v", result.Restored, want)
	}
	if len(result.Skipped) != 0 || len(result.Failed) != 0 {
		t.Errorf("skipped=%+v failed=%+v, want both empty", result.Skipped, result.Failed)
	}
	if puts := s.callsWithPrefix("PUT "); !reflect.DeepEqual(puts, []string{limuTestPutMain}) {
		t.Errorf("PUTs = %v, want only the class's main source", puts)
	}
	if got := s.puts[limuTestClassURI+"/source/main"]; got != "source of main-old" {
		t.Errorf("main source written = %q, want the pre-transport main version", got)
	}
	if n := len(s.callsWithPrefix("activate")); n != 1 {
		t.Errorf("%d activations, want 1", n)
	}
}

// TestRollbackTransport_ClassIncludesRestoredToThemselves: each class include
// is matched against its own history and written back to itself. The client
// used to merge the definitions and implementations histories and write the
// version it found there into the class's main source, replacing the global
// class with a local include.
func TestRollbackTransport_ClassIncludesRestoredToThemselves(t *testing.T) {
	s := &limuTestServer{
		entries: limuEntry("R3TR", "CLAS", "ZCL_X"),
		feeds: map[string][]limuTestFeed{
			limuTestFeedMain: {{limuTestTask, "/content/main-new"}, {limuTestOlder, "/content/main-old"}},
			limuTestFeedDefs: {{limuTestTask, "/content/defs-new"}, {limuTestOlder, "/content/defs-old"}},
			limuTestClassURI + "/includes/implementations/versions": {{limuTestOlder, "/content/impl-old"}},
		},
	}
	result := s.rollback(t, RollbackOptions{})

	if len(result.Restored) != 1 || len(result.Failed) != 0 {
		t.Fatalf("restored=%+v failed=%+v, want the class restored", result.Restored, result.Failed)
	}
	want := map[string]string{
		limuTestClassURI + "/source/main":          "source of main-old",
		limuTestClassURI + "/includes/definitions": "source of defs-old",
	}
	if !reflect.DeepEqual(s.puts, want) {
		t.Errorf("written = %v, want %v", s.puts, want)
	}
	if n := len(s.callsWithPrefix("lock ")); n != 1 {
		t.Errorf("%d locks, want 1 for the whole class", n)
	}
}

// TestRollbackTransport_ClassIncludeCreatedByTransportWritesNothing: when the
// transport created one include (here the local test classes), writing back
// the others alone could leave a class that does not compile, so nothing is
// written and the class is reported as failed.
func TestRollbackTransport_ClassIncludeCreatedByTransportWritesNothing(t *testing.T) {
	s := &limuTestServer{
		entries: limuEntry("LIMU", "METH", "ZCL_X                         METHOD_A"),
		feeds: map[string][]limuTestFeed{
			limuTestFeedMain: {{limuTestTask, "/content/main-new"}, {limuTestOlder, "/content/main-old"}},
			limuTestClassURI + "/includes/testclasses/versions": {{limuTestTask, "/content/tests-new"}},
		},
	}
	result := s.rollback(t, RollbackOptions{})

	if len(result.Failed) != 1 || !strings.Contains(result.Failed[0].Reason, "include testclasses") {
		t.Fatalf("Failed = %+v, want the class failed naming the testclasses include", result.Failed)
	}
	if calls := append(s.callsWithPrefix("lock "), s.callsWithPrefix("PUT ")...); len(calls) != 0 {
		t.Errorf("sent %v, want no lock and no write", calls)
	}
}

// TestResolveRollbackTarget covers the mapping of each entry shape a
// transport holds to the unit of source restored for it.
func TestResolveRollbackTarget(t *testing.T) {
	s := &limuTestServer{queries: map[string]string{
		limuTestTRDIR + "ZPROG'":          "1",
		limuTestTRDIR + "SAPLZFG'":        "F",
		limuTestTRDIR + "ZINCL'":          "I",
		limuTestTADIRPrg + "ZINCL'":       "ZINCL",
		limuTestTRDIR + "LZFGF01'":        "I",
		limuTestTADIRPrg + "LZFGF01'":     "",
		limuTestTADIRFgr + "ZFG'":         "ZFG",
		limuTestTRDIR + "ZTYPES'":         "T",
		limuTestTRDIR + "ZGONE'":          "",
		limuTestTFDIR + "Z_FM'":           "SAPLZFG",
		limuTestTFDIR + "/ABC/FM'":        "/ABC/SAPLFG",
		limuTestTRDIR + "/ABC/SAPLFG'":    "F",
		limuTestTRDIR + "/ABC/LFGTOP'":    "I",
		limuTestTADIRPrg + "/ABC/LFGTOP'": "",
		limuTestTADIRFgr + "/ABC/FG'":     "/ABC/FG",
	}}
	c := s.client(t)

	type want struct {
		entry RollbackEntry
		uri   string
		tadir bool
		ok    bool
	}
	cases := []struct {
		name string
		obj  TransportObject
		want want
	}{
		{"R3TR program", TransportObject{PgmID: "R3TR", Type: "PROG", Name: "ZPROG"},
			want{RollbackEntry{Type: "PROG", Name: "ZPROG"}, "/sap/bc/adt/programs/programs/zprog", true, true}},
		{"R3TR namespaced class", TransportObject{PgmID: "R3TR", Type: "CLAS", Name: "/ABC/CL_X"},
			want{RollbackEntry{Type: "CLAS", Name: "/ABC/CL_X"}, "/sap/bc/adt/oo/classes/%2fabc%2fcl_x", true, true}},
		{"R3TR table", TransportObject{PgmID: "R3TR", Type: "TABL", Name: "ZTAB"}, want{ok: false}},
		{"method", TransportObject{PgmID: "LIMU", Type: "METH", Name: "ZCL_X                         METHOD_A"},
			want{RollbackEntry{Type: "CLAS", Name: "ZCL_X"}, limuTestClassURI, true, true}},
		{"private section", TransportObject{PgmID: "LIMU", Type: "CPRI", Name: "ZCL_X"},
			want{RollbackEntry{Type: "CLAS", Name: "ZCL_X"}, limuTestClassURI, true, true}},
		{"class include", TransportObject{PgmID: "LIMU", Type: "CINC", Name: "ZCL_X=========================CCIMP"},
			want{RollbackEntry{Type: "CLAS", Name: "ZCL_X"}, limuTestClassURI, true, true}},
		{"interface pool include", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "ZIF_X=========================IU"},
			want{RollbackEntry{Type: "INTF", Name: "ZIF_X"}, "/sap/bc/adt/oo/interfaces/zif_x", true, true}},
		{"main program", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "ZPROG"},
			want{RollbackEntry{Type: "PROG", Name: "ZPROG"}, "/sap/bc/adt/programs/programs/zprog", true, true}},
		{"function pool", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "SAPLZFG"},
			want{RollbackEntry{Type: "FUGR", Name: "ZFG"}, "/sap/bc/adt/functions/groups/zfg", true, true}},
		{"standalone include", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "ZINCL"},
			want{RollbackEntry{Type: "PROG", Name: "ZINCL"}, "/sap/bc/adt/programs/includes/zincl", true, true}},
		{"function group include", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "LZFGF01"},
			want{RollbackEntry{Type: "REPS", Name: "LZFGF01"}, "/sap/bc/adt/functions/groups/zfg/includes/lzfgf01", false, true}},
		{"namespaced function group include", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "/ABC/LFGTOP"},
			want{RollbackEntry{Type: "REPS", Name: "/ABC/LFGTOP"}, "/sap/bc/adt/functions/groups/%2fabc%2ffg/includes/%2fabc%2flfgtop", false, true}},
		{"type pool", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "ZTYPES"}, want{ok: false}},
		{"program not in TRDIR", TransportObject{PgmID: "LIMU", Type: "REPS", Name: "ZGONE"}, want{ok: false}},
		{"function module", TransportObject{PgmID: "LIMU", Type: "FUNC", Name: "Z_FM"},
			want{RollbackEntry{Type: "FUNC", Name: "Z_FM"}, "/sap/bc/adt/functions/groups/zfg/fmodules/z_fm", false, true}},
		{"namespaced function module", TransportObject{PgmID: "LIMU", Type: "FUNC", Name: "/ABC/FM"},
			want{RollbackEntry{Type: "FUNC", Name: "/ABC/FM"}, "/sap/bc/adt/functions/groups/%2fabc%2ffg/fmodules/%2fabc%2ffm", false, true}},
		{"screen", TransportObject{PgmID: "LIMU", Type: "DYNP", Name: "ZPROG                                   0100"}, want{ok: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tg, reason, ok, err := c.resolveRollbackTarget(context.Background(), tc.obj)
			if err != nil {
				t.Fatalf("resolveRollbackTarget: %v", err)
			}
			if ok != tc.want.ok {
				t.Fatalf("ok = %v (reason %q), want %v", ok, reason, tc.want.ok)
			}
			if !ok {
				if reason == "" {
					t.Errorf("skipped without a reason")
				}
				return
			}
			got := want{tg.entry, tg.uri, tg.tadir, ok}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("target = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestRollbackTransport_CreatedFunctionModuleIsKept: a function module the
// transport created is not deleted, and since it may use the other objects
// the transport created, they are kept too.
func TestRollbackTransport_CreatedFunctionModuleIsKept(t *testing.T) {
	s := &limuTestServer{
		entries: limuEntry("LIMU", "FUNC", "Z_FM") + limuEntry("R3TR", "PROG", "ZNEW"),
		queries: map[string]string{limuTestTFDIR + "Z_FM'": "SAPLZFG"},
		feeds: map[string][]limuTestFeed{
			"/sap/bc/adt/functions/groups/zfg/fmodules/z_fm/source/main/versions": {{limuTestTask, "/content/fm"}},
			"/sap/bc/adt/programs/programs/znew/source/main/versions":             {{limuTestTask, "/content/prog"}},
		},
	}
	result := s.rollback(t, RollbackOptions{DeleteCreatedObjects: true})

	reasons := map[string]string{}
	for _, e := range result.Created {
		reasons[e.Name] = e.Reason
	}
	if reasons["Z_FM"] != rollbackReasonCreatedSubObject || reasons["ZNEW"] != rollbackReasonCreatedSubObjects {
		t.Errorf("Created = %+v", result.Created)
	}
	if len(result.Deleted) != 0 {
		t.Errorf("Deleted = %+v, want nothing", result.Deleted)
	}
}

// TestRollbackTransport_401WhileResolvingSendsNothingMore (#222): after the
// first lookup answers 401, no further lookup, history read or write is sent.
func TestRollbackTransport_401WhileResolvingSendsNothingMore(t *testing.T) {
	s := &limuTestServer{
		entries:     limuEntry("LIMU", "REPS", "ZPROG") + limuEntry("LIMU", "FUNC", "Z_FM") + limuEntry("R3TR", "PROG", "ZOTHER"),
		queryStatus: http.StatusUnauthorized,
	}
	result := s.rollback(t, RollbackOptions{})

	if n := len(s.callsWithPrefix("query")); n != 1 {
		t.Errorf("%d queries sent, want 1", n)
	}
	if len(result.Failed) != 3 {
		t.Fatalf("Failed = %+v, want all three entries", result.Failed)
	}
	for _, f := range result.Failed[1:] {
		if !strings.HasPrefix(f.Reason, "not attempted: ") {
			t.Errorf("%s: reason %q, want not attempted", f.Name, f.Reason)
		}
	}
}
