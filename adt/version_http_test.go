package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

func TestGetVersionHistory_AcceptHeader(t *testing.T) {
	var gotAccept string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sap/bc/adt/programs/programs/ZTEST/source/main/versions" {
			gotAccept = r.Header.Get("Accept")
			w.Header().Set("Content-Type", "application/atom+xml;type=feed")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>00001</id>
    <updated>2025-01-15T10:30:00Z</updated>
    <title>Version 1</title>
    <author><name>DEVELOPER</name></author>
    <content src="/sap/bc/adt/programs/programs/ZTEST/source/main/versions/20250115103000/00001/content"/>
  </entry>
</feed>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	versions, err := client.GetVersionHistory(context.Background(), "/sap/bc/adt/programs/programs/ZTEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("expected 1 version, got %d", len(versions))
	}
	if versions[0].Include != "" {
		t.Errorf("expected empty include for program, got %q", versions[0].Include)
	}

	wantAccept := "application/atom+xml;type=feed"
	if gotAccept != wantAccept {
		t.Errorf("Accept header: got %q, want %q", gotAccept, wantAccept)
	}
}

func TestGetVersionHistory_ClassIncludes(t *testing.T) {
	var gotPaths []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch path {
		case "/sap/bc/adt/oo/classes/ZCL_TEST/includes/main/versions":
			gotPaths = append(gotPaths, path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>00003</id>
    <updated>2025-01-17T09:00:00Z</updated>
    <author><name>DEV3</name></author>
    <content src="/sap/bc/adt/oo/classes/ZCL_TEST/includes/main/versions/20250117090000/00003/content"/>
  </entry>
</feed>`))
		case "/sap/bc/adt/oo/classes/ZCL_TEST/includes/definitions/versions":
			gotPaths = append(gotPaths, path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>00001</id>
    <updated>2025-01-15T10:00:00Z</updated>
    <author><name>DEV1</name></author>
    <content src="/sap/bc/adt/oo/classes/ZCL_TEST/includes/definitions/versions/20250115100000/00001/content"/>
  </entry>
</feed>`))
		case "/sap/bc/adt/oo/classes/ZCL_TEST/includes/implementations/versions":
			gotPaths = append(gotPaths, path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>00002</id>
    <updated>2025-01-16T14:00:00Z</updated>
    <author><name>DEV2</name></author>
    <content src="/sap/bc/adt/oo/classes/ZCL_TEST/includes/implementations/versions/20250116140000/00002/content"/>
  </entry>
</feed>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	versions, err := client.GetVersionHistory(context.Background(), "/sap/bc/adt/oo/classes/ZCL_TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should fetch the global source and the three includes that exist
	// (macros and testclasses answer 404 and contribute nothing).
	if len(gotPaths) != 3 {
		t.Fatalf("expected 3 served requests, got %d: %v", len(gotPaths), gotPaths)
	}

	want := []struct{ include, author string }{
		{"main", "DEV3"},
		{"definitions", "DEV1"},
		{"implementations", "DEV2"},
	}
	if len(versions) != len(want) {
		t.Fatalf("expected %d versions, got %d", len(want), len(versions))
	}
	for i, w := range want {
		if versions[i].Include != w.include || versions[i].Author != w.author {
			t.Errorf("version[%d]: got include %q author %q, want %q %q", i, versions[i].Include, versions[i].Author, w.include, w.author)
		}
	}
}

func TestGetVersionHistory_ClassDoesNotUseSourceMain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// /source/main/versions should NOT be called for classes
		if path == "/sap/bc/adt/oo/classes/ZCL_TEST/source/main/versions" {
			t.Error("should not request /source/main/versions for classes")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// Serve empty feeds for includes
		if path == "/sap/bc/adt/oo/classes/ZCL_TEST/includes/main/versions" ||
			path == "/sap/bc/adt/oo/classes/ZCL_TEST/includes/definitions/versions" ||
			path == "/sap/bc/adt/oo/classes/ZCL_TEST/includes/implementations/versions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom"></feed>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	_, err := client.GetVersionHistory(context.Background(), "/sap/bc/adt/oo/classes/ZCL_TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestGetVersionHistory_ClassMainIsRequired: a local include without history
// answers 404 and is left out, but the global source must answer, or the
// history would silently miss every method change (#134).
func TestGetVersionHistory_ClassMainIsRequired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	_, err := client.GetVersionHistory(context.Background(), "/sap/bc/adt/oo/classes/ZCL_TEST")
	if err == nil || !strings.Contains(err.Error(), "main") {
		t.Fatalf("want an error naming the main include, got %v", err)
	}
}
