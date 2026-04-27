package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// wantMessageClassType is the vendor MIME type S/4 requires for the
// /sap/bc/adt/messageclass endpoint. Defined locally so the test asserts
// the literal string the bug report named, not whatever constant
// messageclass.go currently references.
const wantMessageClassType = "application/vnd.sap.adt.mc.messageclass+xml"

// TestGetMessageClass_SendsVendorAcceptHeader regression-tests adtler#5:
// S/4 rejects "application/xml" with HTTP 406 and explicitly names the
// vendor MIME type as the only accepted representation. The fix is to send
// that vendor type unconditionally; R/3 accepts it as well.
func TestGetMessageClass_SendsVendorAcceptHeader(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/sap/bc/adt/messageclass/zmctest" {
			t.Errorf("path: got %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("ETag", `"etag-mc-1"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<mc:messageClass adtcore:name="ZMCTEST" adtcore:description="t"
    xmlns:mc="http://www.sap.com/adt/MessageClass"
    xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:name="$TMP"/>
</mc:messageClass>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetMessageClass(context.Background(), "ZMCTEST"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAccept != wantMessageClassType {
		t.Errorf("Accept header: got %q, want %q", gotAccept, wantMessageClassType)
	}
}

// TestSetMessages_SendsVendorAcceptAndContentType regression-tests adtler#5
// for the PUT path. SetMessages must send both Content-Type *and* Accept as
// the vendor type — without Accept, S/4 returns 406 even though Content-Type
// is correct.
func TestSetMessages_SendsVendorAcceptAndContentType(t *testing.T) {
	var gotAccept, gotContentType, gotIfMatch string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut {
			gotAccept = r.Header.Get("Accept")
			gotContentType = r.Header.Get("Content-Type")
			gotIfMatch = r.Header.Get("If-Match")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	err := client.SetMessages(context.Background(), "ZMCTEST", `"etag-1"`, []adt.Message{
		{Number: "001", Text: "Hello", SelfExpl: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotContentType != wantMessageClassType {
		t.Errorf("Content-Type: got %q, want %q", gotContentType, wantMessageClassType)
	}
	if gotAccept != wantMessageClassType {
		t.Errorf("Accept: got %q, want %q", gotAccept, wantMessageClassType)
	}
	if gotIfMatch != `"etag-1"` {
		t.Errorf("If-Match: got %q", gotIfMatch)
	}
}
