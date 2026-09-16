package adt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// TestRemoveFromTransport_ECCUnsupported_NeverSendsPUT is the acceptance
// test for Task 7's gate: when the parent transport's capability read comes
// back as ECC's worklist shape (eccWorklistXML — no removeobject, no
// addobject anywhere), RemoveFromTransport must return an error without
// ever issuing the PUT. The handler fails the test itself if a PUT reaches
// it, which is the actual point of the gate: on a pre-7.53 system that PUT
// is not rejected by SAP, it is silently reinterpreted by a legacy
// change-owner handler with a missing target user (see issue #125). A gate
// that only produced a clearer error message on the same PUT would not
// close that hole.
func TestRemoveFromTransport_ECCUnsupported_NeverSendsPUT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/sap/bc/adt/cts/transportrequests/HFQK900178":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(eccWorklistXML))
		case r.Method == http.MethodPut:
			t.Fatalf("PUT must never be sent once the capability read confirms unsupported, got PUT %s", r.URL.Path)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.RemoveFromTransport(context.Background(),
		"HFQK900635", "HFQK900178", "R3TR", "PROG", "ZTEST", "PROG/P", "000001")
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("errors.As(*adt.ADTError): got false for error %v", err)
	}
	if got := adt.ClassifyError(err); got != adt.ErrorNotSupported {
		t.Errorf("ClassifyError = %v, want ErrorNotSupported", got)
	}
	const wantMsg = "this system's ADT does not advertise a remove-object operation for transport entries " +
		"(added in AS ABAP 7.53 SP00 / ABAP Platform 1809); remove the entry in SE09 instead"
	if adtErr.Message != wantMsg {
		t.Errorf("message = %q, want %q", adtErr.Message, wantMsg)
	}
}

// TestRemoveFromTransport_S4Supported_StillIssuesPUT pins that a system
// whose capability read comes back as s4SingleRequestXML (removeobject and
// addobject both present) is unaffected by the gate: RemoveFromTransport
// still issues the same PUT, with the same body, that it did before Task 7.
func TestRemoveFromTransport_S4Supported_StillIssuesPUT(t *testing.T) {
	var gotPath, gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/sap/bc/adt/cts/transportrequests/S4UK904438":
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(s4SingleRequestXML))
		case r.Method == http.MethodPut:
			gotPath = r.URL.Path
			gotMethod = r.Method
			data, _ := io.ReadAll(r.Body)
			gotBody = string(data)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.RemoveFromTransport(context.Background(),
		"S4UK904439", "S4UK904438", "R3TR", "PROG", "ZTEST", "PROG/P", "000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method: got %q, want PUT", gotMethod)
	}
	if gotPath != "/sap/bc/adt/cts/transportrequests/S4UK904439" {
		t.Errorf("path: got %q, want task number in path", gotPath)
	}
	if !strings.Contains(gotBody, `tm:useraction="removeobject"`) {
		t.Errorf("body missing removeobject useraction:\n%s", gotBody)
	}
}

// TestRemoveFromTransport_CapabilityReadFails_StillIssuesPUT pins the other
// half of the fail-open rule: a capability read that fails outright (500 on
// the parent transport GET) leaves the state at RemoveObjectSupportUnknown,
// and RemoveFromTransport proceeds with the PUT exactly as it did before
// this gate existed. Failing closed here would break setups that work today
// merely because their transport could not be classified.
func TestRemoveFromTransport_CapabilityReadFails_StillIssuesPUT(t *testing.T) {
	var putSeen bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/sap/bc/adt/cts/transportrequests/DEVK900123":
			w.WriteHeader(http.StatusInternalServerError)
		case r.Method == http.MethodPut:
			putSeen = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.RemoveFromTransport(context.Background(),
		"DEVK900124", "DEVK900123", "R3TR", "PROG", "ZTEST", "PROG/P", "000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !putSeen {
		t.Error("expected the PUT to be issued even though the capability read failed")
	}
}
