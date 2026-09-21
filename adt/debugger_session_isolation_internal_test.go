package adt

import (
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// TestNewDebugSession_UsesIsolatedSession guards against a debug session
// sharing its parent Client's cookie jar. Every debugger call that carries
// X-sap-adt-sessiontype: stateful (Attach, Step, GetVariable, GetStack) pins
// the underlying HTTP session to one backend server instance. If DebugSession
// shares the same *httpClient (and therefore the same cookie jar) as every
// other ADT tool, a stuck stateful debug call — e.g. a stepContinue that
// never returns — leaves every other request sharing that jar routed to (or
// blocked behind) the same stuck session, wedging unrelated calls like
// GetSource or SearchObjects until the whole client is restarted. See the
// aibap.mcp issue this guards.
//
// The fix mirrors RunClass's existing freshSession isolation (issue #106):
// a DebugSession must get its own cookie jar and CSRF token while still
// reusing the parent's transport/connection pool and credentials.
func TestNewDebugSession_UsesIsolatedSession(t *testing.T) {
	parent, ok := NewClient(sapmcpconfig.SAPSystem{Host: "https://example.invalid", User: "U", Password: "P", Client: "100"}).(*httpClient)
	if !ok {
		t.Fatalf("NewClient did not return *httpClient")
	}

	dbg := NewDebugSession(parent, "U")

	if dbg.client == parent {
		t.Fatal("DebugSession shares the parent *httpClient directly — no isolation, a stuck debug call wedges every other ADT call")
	}
	if dbg.client.http.Jar == parent.http.Jar {
		t.Error("DebugSession's cookie jar is the parent's — a stuck stateful debug session can wedge every other ADT call sharing it")
	}
	if dbg.client.http.Transport != parent.http.Transport {
		t.Error("DebugSession does not reuse the parent's transport/connection pool")
	}
}
