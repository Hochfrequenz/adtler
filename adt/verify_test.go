package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
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
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<asx:abap xmlns:asx="http://www.sap.com/abapxml"><asx:values><DATA><LOCK_HANDLE>lh-1</LOCK_HANDLE></DATA></asx:values></asx:abap>`))
		case r.URL.Query().Get("_action") == "UNLOCK":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusOK)
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
