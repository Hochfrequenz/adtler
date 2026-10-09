package adt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

var checkObjectURIRe = regexp.MustCompile(`adtcore:uri="([^"]+)"`)

// verifySourceServer mocks the full VerifySource round-trip: CSRF, create temp
// program, lock, get-source (for etag), set-source, unlock, syntax check, and
// delete. The /checkruns handler echoes the requested object URI as the
// report's triggeringUri (which SyntaxCheck correlates on) and, when withError
// is set, includes one error-severity message.
func verifySourceServer(withError bool) *httptest.Server {
	return verifySourceServerWithState(withError, &verifyServerState{})
}

// verifyServerState lets a test observe and steer the delete part of the
// VerifySource mock. Like S/4HANA (adtler#187), the server refuses a DELETE
// with 403 while a lock is held, and only LOCK/UNLOCK change that. A non-zero
// deleteStatus makes the DELETE fail with that status regardless of locks.
type verifyServerState struct {
	mu           sync.Mutex
	locked       bool
	lockRequests int
	deleted      bool
	deleteStatus int
}

func verifySourceServerWithState(withError bool, st *verifyServerState) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == logoffPath:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == checkrunsPath && r.Method == http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			uri := ""
			if m := checkObjectURIRe.FindStringSubmatch(string(body)); m != nil {
				uri = m[1]
			}
			msg := ""
			if withError {
				msg = `<chkrun:checkMessage chkrun:uri="` + uri + `/source/main#start=2,5" chkrun:type="E" chkrun:shortText="Field &quot;FOO&quot; is unknown."/>`
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkrun:checkRunReports xmlns:chkrun="http://www.sap.com/adt/checkrun">
  <chkrun:checkReport chkrun:reporter="abapCheckRun" chkrun:triggeringUri="` + uri + `" chkrun:status="processed" chkrun:statusText="Syntax check performed">
    <chkrun:checkMessageList>` + msg + `</chkrun:checkMessageList>
  </chkrun:checkReport>
</chkrun:checkRunReports>`))
		case r.URL.Path == programsEndpoint && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated) // create temp program
		case strings.HasSuffix(r.URL.Path, "/source/main"):
			if r.Method == http.MethodGet {
				w.Header().Set("ETag", "etag-1")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("REPORT zx."))
			} else { // PUT set-source
				w.WriteHeader(http.StatusOK)
			}
		case r.URL.Query().Get("_action") == "LOCK":
			st.mu.Lock()
			st.locked = true
			st.lockRequests++
			st.mu.Unlock()
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA><LOCK_HANDLE>lh-1</LOCK_HANDLE></DATA></asx:values></asx:abap>`))
		case r.URL.Query().Get("_action") == actionUnlock:
			st.mu.Lock()
			st.locked = false
			st.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet: // DeleteObject's ETag fetch on the bare object URI
			w.Header().Set("ETag", "etag-object")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="ZX" adtcore:type="PROG/P"/>`))
		case r.Method == http.MethodDelete:
			st.mu.Lock()
			defer st.mu.Unlock()
			switch {
			case st.locked:
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionResourceNoAccess"/><message lang="EN">User USERA is currently editing the program</message></exc:exception>`))
			case st.deleteStatus != 0:
				w.WriteHeader(st.deleteStatus)
				_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionPreconditionFailed"/><message lang="EN">Client ETag does not match the object ETag</message></exc:exception>`))
			default:
				st.deleted = true
				w.WriteHeader(http.StatusNoContent)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestVerifySource(t *testing.T) {
	t.Run("valid source (no error messages)", func(t *testing.T) {
		srv := verifySourceServer(false)
		defer srv.Close()
		cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
		client := adt.NewClient(cfg)

		valid, msgs, err := client.VerifySource(context.Background(), "REPORT zx.")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !valid {
			t.Errorf("valid = false, want true (no error messages)")
		}
		if len(msgs) != 0 {
			t.Errorf("expected 0 messages, got %d: %+v", len(msgs), msgs)
		}
	})

	t.Run("invalid source (error message)", func(t *testing.T) {
		srv := verifySourceServer(true)
		defer srv.Close()
		cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
		client := adt.NewClient(cfg)

		valid, msgs, err := client.VerifySource(context.Background(), "REPORT zx. DATA x TYPE foo.")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if valid {
			t.Errorf("valid = true, want false (an E message is present)")
		}
		if len(msgs) != 1 || msgs[0].Type != "E" {
			t.Fatalf("expected 1 E message, got %+v", msgs)
		}
	})
}

// TestVerifySource_RemovesTheTemporaryProgram is the regression test for
// adtler#187. The cleanup used to lock the program and then delete it with the
// handle; on S/4HANA the held lock blocked the delete, the error was
// discarded, and every call left a program in $TMP. The mock behaves like
// S/4HANA: a DELETE while a lock is held is refused.
func TestVerifySource_RemovesTheTemporaryProgram(t *testing.T) {
	st := &verifyServerState{}
	srv := verifySourceServerWithState(false, st)
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	if _, _, err := client.VerifySource(context.Background(), "REPORT zx."); err != nil {
		t.Fatalf("VerifySource: %v", err)
	}
	if !st.deleted {
		t.Error("the temporary program was not deleted")
	}
	if st.lockRequests != 1 {
		t.Errorf("lock requests: got %d, want 1 (only the one that writes the source; the cleanup must not lock)", st.lockRequests)
	}
}

// A cleanup failure used to vanish. It must reach the caller, named by the
// program that is left behind, while the syntax-check result stays available.
func TestVerifySource_ReportsFailedCleanup(t *testing.T) {
	st := &verifyServerState{deleteStatus: http.StatusPreconditionFailed}
	srv := verifySourceServerWithState(true, st)
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	valid, msgs, err := client.VerifySource(context.Background(), "REPORT zx. DATA x TYPE foo.")
	if err == nil {
		t.Fatal("expected an error: the temporary program could not be deleted")
	}
	if !strings.Contains(err.Error(), "Z_ADTLER_VERIFY_") {
		t.Errorf("error should name the program left behind, got: %v", err)
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.Type != "ExceptionPreconditionFailed" {
		t.Errorf("the delete's ADTError should stay reachable with errors.As, got: %v", err)
	}
	if valid || len(msgs) != 1 {
		t.Errorf("the syntax-check result should still be returned: valid=%v, %d messages", valid, len(msgs))
	}
}

// verifyRoundTrip adapts a function to http.RoundTripper.
type verifyRoundTrip func(*http.Request) (*http.Response, error)

func (f verifyRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A caller whose context ends while VerifySource holds the lock must not leave
// the program and the lock behind: the cleanup runs on its own context, releases
// the lock that could not be released with the dead one, and deletes the
// program. The mock refuses a DELETE while a lock is held, like S/4HANA.
func TestVerifySource_CleansUpAfterTheCallersContextEnds(t *testing.T) {
	st := &verifyServerState{}
	srv := verifySourceServerWithState(false, st)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	transport := verifyRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/source/main") {
			cancel() // the caller gives up right after the lock was taken
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	client := adt.NewClientWithTransport(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}, transport)

	if _, _, err := client.VerifySource(ctx, "REPORT zx."); err == nil {
		t.Fatal("expected an error: the caller's context ended during the call")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.locked {
		t.Error("the lock was left behind")
	}
	if !st.deleted {
		t.Error("the temporary program was left behind")
	}
}
