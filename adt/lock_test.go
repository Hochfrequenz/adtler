package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestLockObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Query().Get("_action") != "LOCK" {
			t.Errorf("expected _action=LOCK, got %q", r.URL.Query().Get("_action"))
		}
		if accept := r.Header.Get("Accept"); accept != "application/vnd.sap.as+xml;charset=UTF-8;dataname=com.sap.adt.lock.result" {
			t.Errorf("Accept header: got %q, want %q", accept, "application/vnd.sap.as+xml;charset=UTF-8;dataname=com.sap.adt.lock.result")
		}
		if st := r.Header.Get("X-sap-adt-sessiontype"); st != "stateful" {
			t.Errorf("sessiontype header: got %q, want stateful", st)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA><LOCK_HANDLE>lock-handle-xyz</LOCK_HANDLE></DATA></asx:values></asx:abap>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	handle, err := client.LockObject(context.Background(), "/sap/bc/adt/programs/programs/ZTEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle != "lock-handle-xyz" {
		t.Errorf("handle: got %q", handle)
	}
}

func TestUnlockObject(t *testing.T) {
	var gotAction string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotAction = r.URL.Query().Get("_action")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.UnlockObject(context.Background(), "/sap/bc/adt/programs/programs/ZTEST", "lock-handle-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAction != "UNLOCK" {
		t.Errorf("action: got %q, want UNLOCK", gotAction)
	}
}

// aibap.mcp#494: the reporter's raw-HTTP diagnosis required URL-encoding the
// lock handle in the query string, since real handles contain '+', '=', '/'.
// UnlockObject builds its query string via raw concatenation
// ("?_action=UNLOCK&lockHandle="+lockHandle), never through url.Values —
// unlike every other lockHandle-in-query call site (SetIncludeSource,
// CreateTestInclude, setSourceWithLockParam). A raw '+' in the handle is
// decoded as a space by any form-urlencoded-convention query parser
// (Go's r.URL.Query() included), silently corrupting the handle the server
// receives — a plausible contributor to "invalid lock handle" / lock
// release failures reported elsewhere (#383, #430, #449).
func TestUnlockObject_EncodesLockHandleWithSpecialCharacters(t *testing.T) {
	const rawHandle = `AB+C=D/E`
	var gotHandle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		gotHandle = r.URL.Query().Get("lockHandle")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if err := client.UnlockObject(context.Background(), "/sap/bc/adt/programs/programs/ZTEST", rawHandle); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotHandle != rawHandle {
		t.Errorf("server received lockHandle %q, want %q (unencoded '+' corrupts to space)", gotHandle, rawHandle)
	}
}
