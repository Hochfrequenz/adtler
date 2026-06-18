package adt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestFindPreTransportVersion(t *testing.T) {
	t.Run("returns version after the transport entry", func(t *testing.T) {
		// History is newest-first: the transport's own version, then the prior.
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900100"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: "uri-pre"},
		}
		got, err := findPreTransportVersion(versions, "DEVK900100")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "uri-pre" {
			t.Errorf("got %q, want %q", got, "uri-pre")
		}
	})

	t.Run("transport spanning multiple versions takes the first earlier one", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "3", Transport: "DEVK900100"},
			{VersionNumber: "2", Transport: "DEVK900100"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: "uri-pre"},
		}
		got, err := findPreTransportVersion(versions, "DEVK900100")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "uri-pre" {
			t.Errorf("got %q, want %q", got, "uri-pre")
		}
	})

	t.Run("transport not in history", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900001"},
			{VersionNumber: "1", Transport: "DEVK900000"},
		}
		if _, err := findPreTransportVersion(versions, "DEVK900100"); err == nil {
			t.Error("expected error when transport is absent from history")
		}
	})

	t.Run("object created by the transport (no earlier version)", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "1", Transport: "DEVK900100"},
		}
		if _, err := findPreTransportVersion(versions, "DEVK900100"); err == nil {
			t.Error("expected error when there is no version before the transport")
		}
	})

	t.Run("earlier version with empty ContentURI is treated as no earlier version", func(t *testing.T) {
		versions := []VersionInfo{
			{VersionNumber: "2", Transport: "DEVK900100"},
			{VersionNumber: "1", Transport: "DEVK900001", ContentURI: ""},
		}
		if _, err := findPreTransportVersion(versions, "DEVK900100"); err == nil {
			t.Error("expected error when the earlier version has no ContentURI")
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
