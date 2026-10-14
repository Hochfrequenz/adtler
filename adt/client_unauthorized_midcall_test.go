package adt_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// The tests in this file cover a credential that stops working in the middle
// of a call that sends several requests: the first requests succeed, then SAP
// answers 401, for instance because the user got locked meanwhile. From that
// request on nothing may be sent any more (#222), neither the next step nor a
// best-effort cleanup.

// tripwireServer serves base until trip matches a request. That request and
// every later one are answered with 401 and counted in after, so after == 1
// means the call stopped at the rejected request.
type tripwireServer struct {
	*httptest.Server
	tripped atomic.Bool
	after   atomic.Int32
}

func newTripwireServer(t *testing.T, base http.Handler, trip func(r *http.Request) bool) *tripwireServer {
	t.Helper()
	tw := &tripwireServer{}
	tw.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tw.tripped.Load() || trip(r) {
			tw.tripped.Store(true)
			tw.after.Add(1)
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(unauthorizedBody))
			return
		}
		base.ServeHTTP(w, r)
	}))
	t.Cleanup(tw.Close)
	return tw
}

// assertStoppedAtTrip fails unless the tripwire fired and exactly one request
// reached the server from then on.
func assertStoppedAtTrip(t *testing.T, tw *tripwireServer) {
	t.Helper()
	if !tw.tripped.Load() {
		t.Fatal("the call never reached the request the test rejects")
	}
	if got := tw.after.Load(); got != 1 {
		t.Errorf("server received %d request(s) from the rejected one on, want 1", got)
	}
}

// requestBody reads r's body and puts it back, so a trip condition can look
// at it before the handler does.
func requestBody(r *http.Request) string {
	data, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(data))
	return string(data)
}

func isMethod(r *http.Request, method, pathSuffix string) bool {
	return r.Method == method && strings.HasSuffix(r.URL.Path, pathSuffix)
}

func isAction(r *http.Request, action string) bool {
	return r.URL.Query().Get("_action") == action
}

const (
	midcallTransport = "AAAK900100"
	midcallVersions  = "/source/main/versions"
	midcallOldPrefix = "/versions/old/"
)

// rollbackHandler serves a transport with two programs, ZA and ZB, each with
// a version before the transport, and accepts every step of their rollback.
func rollbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case r.URL.Path == transportRequestsEndpoint+"/"+midcallTransport:
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:request tm:number="` + midcallTransport + `">
    <tm:abap_object tm:pgmid="R3TR" tm:type="PROG" tm:name="ZA"/>
    <tm:abap_object tm:pgmid="R3TR" tm:type="PROG" tm:name="ZB"/>
  </tm:request>
</tm:root>`))
		case strings.HasSuffix(r.URL.Path, midcallVersions):
			name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, programsEndpoint+"/"), midcallVersions)
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><content src="/versions/new/` + name + `"/><link rel="http://www.sap.com/adt/relations/transport" name="` + midcallTransport + `"/></entry>
  <entry><content src="` + midcallOldPrefix + name + `"/><link rel="http://www.sap.com/adt/relations/transport" name="OLD"/></entry>
</feed>`))
		case strings.HasPrefix(r.URL.Path, midcallOldPrefix):
			_, _ = w.Write([]byte("REPORT zold."))
		case isAction(r, "LOCK"):
			_, _ = w.Write([]byte("lock-handle"))
		case isAction(r, actionUnlock):
		case isMethod(r, http.MethodGet, "/source/main"):
			w.Header().Set("ETag", `"current"`)
			_, _ = w.Write([]byte("REPORT zcurrent."))
		case isMethod(r, http.MethodPut, "/source/main"):
		case r.URL.Path == activationPath:
		case r.URL.Path == inactiveObjectsPath:
			_, _ = w.Write([]byte(`<root/>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestRollbackTransport_401MidCall_StopsSending(t *testing.T) {
	cases := []struct {
		name string
		trip func(*http.Request) bool
	}{
		{"reading the task numbers", nthRequest(func(r *http.Request) bool {
			return r.URL.Path == transportRequestsEndpoint+"/"+midcallTransport
		}, 2)},
		{"reading the version history", func(r *http.Request) bool {
			return strings.HasSuffix(r.URL.Path, midcallVersions)
		}},
		{"reading the current source after the lock", func(r *http.Request) bool {
			return isMethod(r, http.MethodGet, "/source/main")
		}},
		{"writing the old source", func(r *http.Request) bool {
			return isMethod(r, http.MethodPut, "/source/main")
		}},
		{"unlocking before the activation", func(r *http.Request) bool {
			return isAction(r, actionUnlock)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tw := newTripwireServer(t, rollbackHandler(), tc.trip)
			client := adt.NewClient(newTestConfig(tw.URL))

			result, err := client.RollbackTransport(context.Background(), midcallTransport)

			assertStoppedAtTrip(t, tw)
			if err != nil {
				assertUnauthorized(t, err)
				return
			}
			if len(result.Restored) != 0 || len(result.Failed) != 2 {
				t.Fatalf("restored=%v failed=%v, want both programs failed", result.Restored, result.Failed)
			}
			for _, f := range result.Failed {
				if !strings.Contains(f.Reason, "401") {
					t.Errorf("%s: reason %q does not name the 401", f.Name, f.Reason)
				}
			}
		})
	}
}

// nthRequest matches the n-th request that match matches.
func nthRequest(match func(*http.Request) bool, n int32) func(*http.Request) bool {
	var seen atomic.Int32
	return func(r *http.Request) bool {
		return match(r) && seen.Add(1) == n
	}
}

func TestVerifySource_401MidCall_StopsSending(t *testing.T) {
	cases := []struct {
		name string
		trip func(*http.Request) bool
	}{
		{"locking", func(r *http.Request) bool { return isAction(r, "LOCK") }},
		{"reading the source for its ETag", func(r *http.Request) bool {
			return isMethod(r, http.MethodGet, "/source/main")
		}},
		{"writing the source", func(r *http.Request) bool {
			return isMethod(r, http.MethodPut, "/source/main")
		}},
		{"releasing the lock", func(r *http.Request) bool { return isAction(r, actionUnlock) }},
		{"the syntax check", func(r *http.Request) bool { return r.URL.Path == checkrunsPath }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := verifySourceServer(false)
			t.Cleanup(base.Close)
			tw := newTripwireServer(t, base.Config.Handler, tc.trip)
			client := adt.NewClient(newTestConfig(tw.URL))

			_, _, err := client.VerifySource(context.Background(), "REPORT zx.")

			assertStoppedAtTrip(t, tw)
			assertUnauthorized(t, err)
			// The program could not be removed, and the caller must learn
			// which one stays behind.
			var leak *adt.VerifyCleanupError
			if !errors.As(err, &leak) || leak.Program == "" {
				t.Errorf("error = %v, want a *adt.VerifyCleanupError naming the program", err)
			}
		})
	}
}

// dataPreviewColumns builds a datapreview response with the given columns,
// each a name followed by its values (all columns of equal length).
func dataPreviewColumns(cols ...[]string) string {
	var b strings.Builder
	rows := 0
	if len(cols) > 0 {
		rows = len(cols[0]) - 1
	}
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">
  <dataPreview:totalRows>` + strconv.Itoa(rows) + `</dataPreview:totalRows>`)
	for _, col := range cols {
		b.WriteString(`<dataPreview:columns><dataPreview:metadata dataPreview:name="` + col[0] + `" dataPreview:type="C"/><dataPreview:dataSet>`)
		for _, v := range col[1:] {
			b.WriteString("<dataPreview:data>" + v + "</dataPreview:data>")
		}
		b.WriteString(`</dataPreview:dataSet></dataPreview:columns>`)
	}
	b.WriteString(`</dataPreview:tableData>`)
	return b.String()
}

// dependencyHandler answers the data preview queries of GetObjectDependencies:
// program ZPROG uses table ZT1, and table ZT1 has a field typed with data
// element ZD1 and a check table ZT2. Every other query returns no rows.
func dependencyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		sql := requestBody(r)
		w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
		switch {
		case strings.Contains(sql, "FROM D010TAB"):
			_, _ = w.Write([]byte(dataPreviewColumns([]string{"TABNAME", "ZT1"})))
		case strings.Contains(sql, "FROM DD03L"):
			_, _ = w.Write([]byte(dataPreviewColumns([]string{"ROLLNAME", "ZD1"}, []string{"CHECKTABLE", "ZT2"})))
		default:
			_, _ = w.Write([]byte(dataPreviewColumns([]string{"X"})))
		}
	})
}

func TestGetObjectDependencies_401MidCall_StopsSending(t *testing.T) {
	isQuery := func(table string) func(*http.Request) bool {
		return func(r *http.Request) bool {
			return r.URL.Path != csrfEndpoint && strings.Contains(requestBody(r), "FROM "+table)
		}
	}
	cases := []struct {
		name       string
		objectType string
		objectName string
		trip       func(*http.Request) bool
	}{
		// The classification's first query fails; its second must not follow.
		{"classifying a program's tables", "PROG", "ZPROG", isQuery("DD02L")},
		// Depth 1 holds a table and a data element. The table's query fails;
		// the data element's must not follow.
		{"walking the DDIC chain", "TABL", "ZT1", isQuery("DD03L WHERE TABNAME IN ('ZT2')")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tw := newTripwireServer(t, dependencyHandler(), tc.trip)
			client := adt.NewClient(newTestConfig(tw.URL))

			_, err := client.GetObjectDependencies(context.Background(), tc.objectType, tc.objectName, 0, 3)

			assertStoppedAtTrip(t, tw)
			assertUnauthorized(t, err)
		})
	}
}
