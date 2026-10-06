package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const minimalRunResultXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"><program><testClasses><testClass name="LCL_TEST">` +
	`<testMethods><testMethod name="TEST_HELLO" executionTime="0.1"/></testMethods></testClass></testClasses></program></aunit:runResult>`

// RunUnitTests on a DebugSession must use a NEW isolated session of the debug
// session's own system: not the debug session's stateful cookie jar, and not
// whatever system a ClientRegistry has active at call time.
func TestDebugSessionRunUnitTests_IsolatedAndBoundToSystem(t *testing.T) {
	var mu sync.Mutex
	var hitsA, hitsB int
	var sawStateful, sawDebugCookie bool
	newSrv := func(hits *int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == discoveryPath {
				http.SetCookie(w, &http.Cookie{Name: "SAP_SESSIONID_X", Value: "s-" + r.Host})
				w.Header().Set("X-CSRF-Token", "token")
				return
			}
			if r.URL.Path == "/sap/bc/adt/abapunit/testruns" {
				mu.Lock()
				*hits++
				if r.Header.Get("X-sap-adt-sessiontype") == "stateful" {
					sawStateful = true
				}
				if c, err := r.Cookie("debugsession"); err == nil && c.Value != "" {
					sawDebugCookie = true
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/xml")
				_, _ = w.Write([]byte(minimalRunResultXML))
				return
			}
			// Any debugger call on the debug session sets a marker cookie in ITS jar.
			http.SetCookie(w, &http.Cookie{Name: "debugsession", Value: "1"})
			w.WriteHeader(http.StatusOK)
		}))
	}
	srvA, srvB := newSrv(&hitsA), newSrv(&hitsB)
	defer srvA.Close()
	defer srvB.Close()

	reg, err := NewClientRegistry(map[string]Client{
		"sysA": NewClient(sapmcpconfig.SAPSystem{Host: srvA.URL, User: "U", Password: "P", Client: "100"}),
		"sysB": NewClient(sapmcpconfig.SAPSystem{Host: srvB.URL, User: "U", Password: "P", Client: "100"}),
	}, "sysA")
	if err != nil {
		t.Fatal(err)
	}
	dbg := NewDebugSession(reg, "U")
	// Put a cookie into the debug session's own jar.
	if _, err := dbg.GetStack(context.Background()); err != nil {
		t.Fatalf("GetStack: %v", err)
	}
	if _, err := reg.Select("sysB"); err != nil {
		t.Fatal(err)
	}

	res, err := dbg.RunUnitTests(context.Background(), "/sap/bc/adt/programs/programs/ztest", 60)
	if err != nil {
		t.Fatalf("RunUnitTests: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	mu.Lock()
	defer mu.Unlock()
	if hitsA != 1 || hitsB != 0 {
		t.Errorf("test run went to sysA=%d sysB=%d, want 1/0 (bound to the debug session's system)", hitsA, hitsB)
	}
	if sawStateful {
		t.Error("unit-test request must not carry X-sap-adt-sessiontype: stateful")
	}
	if sawDebugCookie {
		t.Error("unit-test request must not reuse the debug session's cookie jar")
	}
}
