package adt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	copiesTarget     = "AAAK900300"
	copiesSource     = "AAAK900200"
	copiesSourceTask = "AAAK900201"
	copiesPackage    = "ZPACKAGE"
	copiesTMType     = "application/vnd.sap.adt.transportorganizer.v1+xml"
	copiesRelPrefix  = "http://www.sap.com/cts/relations/"
)

// copiesRequestXML builds a single-request transportorganizer.v1 body in the
// shape S/4 returns for GET /sap/bc/adt/cts/transportrequests/{n}, with the
// given status, atom relations on the request and optional task.
func copiesRequestXML(number, status, task string, rels ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>`)
	b.WriteString(`<tm:root tm:object_type="R" adtcore:name="` + number + `" xmlns:tm="http://www.sap.com/cts/adt/tm" xmlns:adtcore="http://www.sap.com/adt/core">`)
	b.WriteString(`<tm:request tm:number="` + number + `" tm:owner="USERA" tm:desc="fixture" tm:type="T" tm:status="` + status + `">`)
	for _, rel := range rels {
		b.WriteString(`<atom:link href="/sap/bc/adt/cts/transportrequests/` + number + `" rel="` + copiesRelPrefix + rel + `" type="application/xml" xmlns:atom="http://www.w3.org/2005/Atom"/>`)
	}
	if task != "" {
		b.WriteString(`<tm:task tm:number="` + task + `" tm:parent="` + number + `" tm:owner="USERA" tm:status="` + status + `"/>`)
	}
	b.WriteString(`</tm:request></tm:root>`)
	return b.String()
}

// copiesSupportedTargetXML is a modifiable target request on a system that
// advertises the 2105 add-objects-from-package relation.
var copiesSupportedTargetXML = copiesRequestXML(copiesTarget, "D", "", "modify", "addobject", "addobjectsfrompackage")

type copiesPut struct {
	path, query, contentType, body string
}

// copiesServer serves transport reads from reads (keyed by transport number)
// and records every PUT. Any other write fails the test.
func copiesServer(t *testing.T, reads map[string]string, putStatus int) (*httptest.Server, func() []copiesPut) {
	t.Helper()
	var mu sync.Mutex
	var puts []copiesPut
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "/sap/bc/adt/cts/transportrequests/"
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, prefix):
			body, ok := reads[strings.TrimPrefix(r.URL.Path, prefix)]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", copiesTMType)
			_, _ = w.Write([]byte(body))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, prefix):
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			puts = append(puts, copiesPut{r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), string(b)})
			mu.Unlock()
			w.WriteHeader(putStatus)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []copiesPut {
		mu.Lock()
		defer mu.Unlock()
		return append([]copiesPut(nil), puts...)
	}
}

func copiesClient(srv *httptest.Server) adt.Client {
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
}

func assertNotSupported(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a not-supported error, got nil")
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
	}
	if adtErr.Type != adt.ExceptionTypeAddObjectsUnsupported {
		t.Errorf("Type = %q, want %q", adtErr.Type, adt.ExceptionTypeAddObjectsUnsupported)
	}
	if kind := adt.ClassifyError(err); kind != adt.ErrorNotSupported {
		t.Errorf("ClassifyError = %v, want %v", kind, adt.ErrorNotSupported)
	}
}

func TestCopyTransportObjects_ModifiableSourceCopiesRequestAndTasks(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{
		copiesTarget: copiesSupportedTargetXML,
		copiesSource: copiesRequestXML(copiesSource, "D", copiesSourceTask, "modify"),
	}, http.StatusOK)

	if err := copiesClient(srv).CopyTransportObjects(context.Background(), copiesSource, copiesTarget, true); err != nil {
		t.Fatalf("CopyTransportObjects: %v", err)
	}

	got := puts()
	if len(got) != 2 {
		t.Fatalf("got %d PUTs, want 2 (request and its task): %+v", len(got), got)
	}
	for i, from := range []string{copiesSource, copiesSourceTask} {
		p := got[i]
		if p.path != "/sap/bc/adt/cts/transportrequests/"+copiesTarget {
			t.Errorf("PUT %d path = %q, want the target request", i, p.path)
		}
		if p.query != "" {
			t.Errorf("PUT %d query = %q, want none (documentation is copied by default)", i, p.query)
		}
		if p.contentType != copiesTMType {
			t.Errorf("PUT %d Content-Type = %q", i, p.contentType)
		}
		if !strings.Contains(p.body, `tm:useraction="addobjectfromrequest"`) {
			t.Errorf("PUT %d body lacks the user action: %s", i, p.body)
		}
		if !strings.Contains(p.body, `tm:number="`+from+`"`) {
			t.Errorf("PUT %d body does not name source %s: %s", i, from, p.body)
		}
		// S/4 rejects the action when the body also carries a <tm:request>
		// (with its empty tm:number): "Check of condition failed".
		if strings.Contains(p.body, "<tm:request") {
			t.Errorf("PUT %d body carries a <tm:request> element: %s", i, p.body)
		}
	}
}

func TestCopyTransportObjects_WithoutDocumentationSetsSAPsInvertedFlag(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{
		copiesTarget: copiesSupportedTargetXML,
		copiesSource: copiesRequestXML(copiesSource, "R", ""),
	}, http.StatusOK)

	if err := copiesClient(srv).CopyTransportObjects(context.Background(), copiesSource, copiesTarget, false); err != nil {
		t.Fatalf("CopyTransportObjects: %v", err)
	}
	got := puts()
	if len(got) != 1 {
		t.Fatalf("got %d PUTs, want 1", len(got))
	}
	// SAP passes "withdocumentation" straight into TR_COPY_COMM's
	// wi_without_documentation, so X means "skip the documentation".
	if got[0].query != "withdocumentation=X" {
		t.Errorf("query = %q, want withdocumentation=X", got[0].query)
	}
}

func TestCopyTransportObjects_ReleasedSourceCopiesRequestOnly(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{
		copiesTarget: copiesSupportedTargetXML,
		copiesSource: copiesRequestXML(copiesSource, "R", copiesSourceTask),
	}, http.StatusOK)

	if err := copiesClient(srv).CopyTransportObjects(context.Background(), copiesSource, copiesTarget, true); err != nil {
		t.Fatalf("CopyTransportObjects: %v", err)
	}
	got := puts()
	if len(got) != 1 || !strings.Contains(got[0].body, `tm:number="`+copiesSource+`"`) {
		t.Fatalf("want exactly one PUT naming the released request, got %+v", got)
	}
}

func TestCopyTransportObjects_UnsupportedSystemSendsNoPUT(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{
		// ECC: the worklist shape, without any add-related relation.
		copiesTarget: eccWorklistXML,
	}, http.StatusOK)

	err := copiesClient(srv).CopyTransportObjects(context.Background(), copiesSource, copiesTarget, true)
	assertNotSupported(t, err)
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent to an unsupported system", n)
	}
}

func TestCopyTransportObjects_TargetWithoutRelationSendsNoPUT(t *testing.T) {
	// A released target carries none of the mutation relations, on any
	// release. The gate fails closed: no relation, no PUT.
	srv, puts := copiesServer(t, map[string]string{
		copiesTarget: copiesRequestXML(copiesTarget, "R", "", "adturi"),
	}, http.StatusOK)

	err := copiesClient(srv).CopyTransportObjects(context.Background(), copiesSource, copiesTarget, true)
	assertNotSupported(t, err)
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent without the capability relation", n)
	}
}

func TestCopyTransportObjects_SAPErrorNamesFailingSource(t *testing.T) {
	srv, _ := copiesServer(t, map[string]string{
		copiesTarget: copiesSupportedTargetXML,
		copiesSource: copiesRequestXML(copiesSource, "D", copiesSourceTask),
	}, http.StatusBadRequest)

	err := copiesClient(srv).CopyTransportObjects(context.Background(), copiesSource, copiesTarget, true)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), copiesSource) {
		t.Errorf("error does not name the failing source: %v", err)
	}
}

func TestCopyTransportObjects_RejectsInvalidArguments(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{}, http.StatusOK)
	c := copiesClient(srv)
	for name, args := range map[string][2]string{
		"empty source":  {"", copiesTarget},
		"empty target":  {copiesSource, ""},
		"same request":  {copiesTarget, copiesTarget},
		"path in input": {"AAAK900200/x", copiesTarget},
	} {
		if err := c.CopyTransportObjects(context.Background(), args[0], args[1], true); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent for invalid arguments", n)
	}
}

func TestAddPackageToTransport_SendsPackageObject(t *testing.T) {
	for _, tc := range []struct {
		subpackages bool
		wantQuery   string
	}{
		{false, ""},
		{true, "includesubpackageobjects=X"},
	} {
		srv, puts := copiesServer(t, map[string]string{copiesTarget: copiesSupportedTargetXML}, http.StatusOK)

		if err := copiesClient(srv).AddPackageToTransport(context.Background(), "zpackage", copiesTarget, tc.subpackages); err != nil {
			t.Fatalf("AddPackageToTransport(subpackages=%v): %v", tc.subpackages, err)
		}
		got := puts()
		if len(got) != 1 {
			t.Fatalf("got %d PUTs, want 1", len(got))
		}
		p := got[0]
		if p.path != "/sap/bc/adt/cts/transportrequests/"+copiesTarget {
			t.Errorf("path = %q", p.path)
		}
		if p.query != tc.wantQuery {
			t.Errorf("subpackages=%v: query = %q, want %q", tc.subpackages, p.query, tc.wantQuery)
		}
		for _, want := range []string{
			`tm:useraction="addobjectsfrompackage"`,
			`tm:pgmid="R3TR"`,
			`tm:type="DEVC"`,
			`tm:name="` + copiesPackage + `"`,
		} {
			if !strings.Contains(p.body, want) {
				t.Errorf("body lacks %s: %s", want, p.body)
			}
		}
		// S/4 rejects an empty tm:number anywhere in the body.
		if strings.Contains(p.body, `tm:number=""`) {
			t.Errorf("body carries an empty tm:number: %s", p.body)
		}
	}
}

func TestAddPackageToTransport_UnsupportedSystemSendsNoPUT(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{copiesTarget: eccWorklistXML}, http.StatusOK)

	err := copiesClient(srv).AddPackageToTransport(context.Background(), copiesPackage, copiesTarget, false)
	assertNotSupported(t, err)
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent to an unsupported system", n)
	}
}

func TestAddPackageToTransport_RejectsEmptyPackage(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{}, http.StatusOK)
	if err := copiesClient(srv).AddPackageToTransport(context.Background(), " ", copiesTarget, false); err == nil {
		t.Fatal("expected an error for an empty package name")
	}
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent for an empty package", n)
	}
}
