package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// acceptAnything is the Accept header a client falls back to when the
// server rejects its first, more specific offer.
const acceptAnything = "*/*"

// serviceBindingXML is the object document a service binding answers with.
// Only the attributes parseGenericObjectInfo reads are present.
const serviceBindingXML = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<srvb:serviceBinding xmlns:srvb="http://www.sap.com/adt/businessservices/odatav4"` +
	` xmlns:adtcore="http://www.sap.com/adt/core"` +
	` adtcore:name="ZSB_EXAMPLE" adtcore:type="SRVB/SVB" adtcore:description="Example binding">` +
	`<adtcore:packageRef adtcore:name="ZPACKAGE"/></srvb:serviceBinding>`

// notAcceptableXML is what ADT answers when the Accept header names a media
// type the resource does not produce. The exception ID is the stable part —
// the message text is translated into the caller's logon language.
const notAcceptableXML = `<?xml version="1.0" encoding="utf-8"?>` +
	`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
	`<type id="ExceptionResourceNotAcceptable"/>` +
	`<message lang="EN">The message content is not acceptable</message></exc:exception>`

// notAcceptableServer serves objectURI only to a request whose Accept header
// contains */*, and answers 406 to every other offer. That is how ADT
// behaves: it publishes exactly one media type per object kind and produces
// nothing else.
func notAcceptableServer(t *testing.T, objectURI string, offers *[]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != objectURI {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		accept := r.Header.Get("Accept")
		*offers = append(*offers, accept)
		if !strings.Contains(accept, acceptAnything) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte(notAcceptableXML))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.businessservices.servicebinding.v2+xml")
		w.Header().Set("ETag", "20260922120000001")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(serviceBindingXML))
	}))
}

// TestGetObjectInfo_RetriesOnNotAcceptable covers the finding from adtler#65
// that reaches past the three object types it names: an ADT 406 means the
// Accept header is wrong, never that the resource is missing. The issue read
// a service binding's 406 as a missing endpoint and went looking for another
// path; the path was right and only the offer was wrong.
//
// A client cannot know every vendor media type SAP will ever publish, so on
// a 406 it asks the same URI again with */* and lets the server choose. The
// first, specific offer is still made first — this is a fallback, not a
// replacement, because asking for */* up front changes what the server
// returns for the object kinds that work today.
func TestGetObjectInfo_RetriesOnNotAcceptable(t *testing.T) {
	const objectURI = "/sap/bc/adt/businessservices/bindings/zsb_example"
	var offers []string
	srv := notAcceptableServer(t, objectURI, &offers)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	info, err := client.GetObjectInfo(context.Background(), objectURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.Name != "ZSB_EXAMPLE" {
		t.Errorf("name: got %q, want %q", info.Name, "ZSB_EXAMPLE")
	}
	if len(offers) != 2 {
		t.Fatalf("server saw %d requests (%v), want 2: the specific offer, then the */* retry", len(offers), offers)
	}
	if strings.Contains(offers[0], acceptAnything) {
		t.Errorf("first offer was %q — the specific media type must be tried first", offers[0])
	}
	if !strings.Contains(offers[1], acceptAnything) {
		t.Errorf("retry offer was %q, want it to contain %q", offers[1], acceptAnything)
	}
}

// TestFetchETag_RetriesOnNotAcceptable is the same rule on the other reader.
// FetchETag is how LockMap resolves an ETag for object kinds that have no
// /source/main, which is exactly the set most likely to answer 406.
func TestFetchETag_RetriesOnNotAcceptable(t *testing.T) {
	const objectURI = "/sap/bc/adt/businessservices/bindings/zsb_example"
	var offers []string
	srv := notAcceptableServer(t, objectURI, &offers)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	// FetchETag is reached through an interface assertion, the same way
	// LockMap.ResolveETag reaches it (adt/lockmap.go).
	fetcher, ok := client.(interface {
		FetchETag(ctx context.Context, objectURI string) (string, error)
	})
	if !ok {
		t.Fatalf("client does not implement FetchETag")
	}
	etag, err := fetcher.FetchETag(context.Background(), objectURI)
	if err != nil {
		t.Fatalf("FetchETag: unexpected error: %v", err)
	}
	if etag != "20260922120000001" {
		t.Errorf("etag: got %q", etag)
	}
	if len(offers) != 2 {
		t.Fatalf("server saw %d requests (%v), want 2", len(offers), offers)
	}
}

// TestGetObjectInfo_DoesNotRetryOnNotFound keeps the fallback from
// over-reaching. A 404 is a missing resource and asking again with a wider
// Accept header cannot change that — retrying would double every failed
// lookup's cost and blur the diagnosis the caller gets back.
func TestGetObjectInfo_DoesNotRetryOnNotFound(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	if _, err := client.GetObjectInfo(context.Background(), "/sap/bc/adt/oo/classes/zcl_missing"); err == nil {
		t.Fatal("GetObjectInfo on a missing object: got nil error, want a failure")
	}
	if requests != 1 {
		t.Errorf("server saw %d requests, want 1: a 404 must not be retried", requests)
	}
}
