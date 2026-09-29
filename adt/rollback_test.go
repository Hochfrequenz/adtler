package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// wantPreTransportURI is the ContentURI findPreTransportVersion should
// return in every subtest below where a pre-transport version does exist.
const wantPreTransportURI = "uri-pre"

func TestFindPreTransportVersion(t *testing.T) {
	t.Run("returns version after the transport entry", func(t *testing.T) {
		// History is newest-first: the transport's own version, then the prior.
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900100"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: wantPreTransportURI},
		}
		got, err := findPreTransportVersion(versions, []string{"DEVK900100"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != wantPreTransportURI {
			t.Errorf("got %q, want %q", got, wantPreTransportURI)
		}
	})

	t.Run("transport spanning multiple versions takes the first earlier one", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "3", Transport: "DEVK900100"},
			{VersionNumber: "2", Transport: "DEVK900100"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: wantPreTransportURI},
		}
		got, err := findPreTransportVersion(versions, []string{"DEVK900100"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != wantPreTransportURI {
			t.Errorf("got %q, want %q", got, wantPreTransportURI)
		}
	})

	t.Run("transport not in history", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900001"},
			{VersionNumber: "1", Transport: "DEVK900000"},
		}
		if _, err := findPreTransportVersion(versions, []string{"DEVK900100"}); err == nil {
			t.Error("expected error when transport is absent from history")
		}
	})

	t.Run("object created by the transport (no earlier version)", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "1", Transport: "DEVK900100"},
		}
		if _, err := findPreTransportVersion(versions, []string{"DEVK900100"}); err == nil {
			t.Error("expected error when there is no version before the transport")
		}
	})

	t.Run("lowercase transport number still matches the server's uppercase Transport", func(t *testing.T) {
		// v.Transport is always uppercase (parseVersionFeed reads it straight
		// from the server); a caller may pass a lowercase transport number
		// through unchanged, exactly like GetTransportObjects/
		// matchesTransportNumber already tolerate.
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900100"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: wantPreTransportURI},
		}
		got, err := findPreTransportVersion(versions, []string{"devk900100"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != wantPreTransportURI {
			t.Errorf("got %q, want %q", got, wantPreTransportURI)
		}
	})

	t.Run("earlier version with empty ContentURI is treated as no earlier version", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900100"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: ""},
		}
		if _, err := findPreTransportVersion(versions, []string{"DEVK900100"}); err == nil {
			t.Error("expected error when the earlier version has no ContentURI")
		}
	})

	t.Run("matches task number when request number is absent (S/4 VRSD behaviour)", func(t *testing.T) {
		// S/4 records the task number (DEVK900101) rather than the request (DEVK900100).
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900101"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: "uri-pre"},
		}
		got, err := findPreTransportVersion(versions, []string{"DEVK900100", "DEVK900101"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "uri-pre" {
			t.Errorf("got %q, want %q", got, "uri-pre")
		}
	})
}

// TestRollbackTransport_SkipsNonRestorable confirms the filtering: non-R3TR
// entries and non-source object types are skipped without any restore work
// (so only the transport-objects read is needed).
func TestRollbackTransport_SkipsNonRestorable(t *testing.T) {
	const transport = "DEVK900100"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/cts/transportrequests/"+transport {
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">
  <tm:request tm:number="` + transport + `">
    <tm:abap_object tm:pgmid="LIMU" tm:type="REPS" tm:name="ZFOO_PART"/>
    <tm:abap_object tm:pgmid="R3TR" tm:type="TABL" tm:name="ZTABLE"/>
  </tm:request>
</tm:root>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := NewClient(cfg)

	result, err := client.RollbackTransport(context.Background(), transport)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Restored) != 0 || len(result.Failed) != 0 {
		t.Errorf("expected nothing restored/failed, got restored=%v failed=%v", result.Restored, result.Failed)
	}
	if len(result.Skipped) != 2 {
		t.Fatalf("expected 2 skipped, got %d: %+v", len(result.Skipped), result.Skipped)
	}
	reasons := map[string]string{}
	for _, e := range result.Skipped {
		reasons[e.Name] = e.Reason
	}
	if reasons["ZFOO_PART"] != "not R3TR" {
		t.Errorf("LIMU object reason = %q, want %q", reasons["ZFOO_PART"], "not R3TR")
	}
	if reasons["ZTABLE"] != "non-source object type" {
		t.Errorf("TABL object reason = %q, want %q", reasons["ZTABLE"], "non-source object type")
	}
}

func TestRollbackObject_UsesRequestNumberAndUnlocksBeforeActivation(t *testing.T) {
	const (
		request           = "<request>"
		task              = "<task>"
		object            = "/sap/bc/adt/programs/programs/ZTEST"
		rollbackCSRFRoute = "/sap/bc/adt/discovery"
	)
	var corrNr string
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == rollbackCSRFRoute:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == object+"/source/main/versions":
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry><content src="/pre-transport-version"/><link rel="http://www.sap.com/adt/relations/transport" name="&lt;task&gt;"/></entry>
  <entry><content src="/older-version"/><link rel="http://www.sap.com/adt/relations/transport" name="OLD"/></entry>
</feed>`))
		case r.URL.Path == "/older-version":
			_, _ = w.Write([]byte("REPORT ZTEST.\n"))
		case r.URL.Path == object && r.URL.Query().Get("_action") == "LOCK":
			calls = append(calls, "lock")
			_, _ = w.Write([]byte("lock-handle"))
		case r.URL.Path == object+"/source/main" && r.Method == http.MethodGet:
			w.Header().Set("ETag", `"current"`)
			_, _ = w.Write([]byte("REPORT ZTEST.\nCURRENT."))
		case r.URL.Path == object+"/source/main" && r.Method == http.MethodPut:
			calls = append(calls, "set-source")
			corrNr = r.URL.Query().Get("corrNr")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == object && r.URL.Query().Get("_action") == "UNLOCK":
			calls = append(calls, "unlock")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/sap/bc/adt/activation" && r.Method == http.MethodPost:
			calls = append(calls, "activate")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/sap/bc/adt/activation/inactiveobjects":
			_, _ = w.Write([]byte(`<root/>`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}).(*httpClient)
	if err := client.rollbackObject(context.Background(), object, []string{request, task}); err != nil {
		t.Fatalf("rollbackObject: %v", err)
	}
	if corrNr != request {
		t.Errorf("SetSource corrNr = %q, want request number %q", corrNr, request)
	}
	wantCalls := []string{"lock", "set-source", "unlock", "activate"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("call order = %v, want %v", calls, wantCalls)
	}
}
