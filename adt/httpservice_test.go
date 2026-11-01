package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	httpServicesPath = "/sap/bc/adt/ucon/httpservices"
	httpServiceType  = "application/vnd.sap.adt.uconn.http.v1+xml"
	handlerAttr      = `http:handler_servicehandler="ZCL_EXAMPLE_HANDLER"`
)

// createRecorder is a fake ADT server that records the create POST.
type createRecorder struct {
	path, contentType, body string
	loggedOut               bool
}

func newCreateServer(t *testing.T, createStatus int) (*httptest.Server, *createRecorder) {
	t.Helper()
	rec := &createRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case logoffPath:
			rec.loggedOut = true
		default:
			b, _ := io.ReadAll(r.Body)
			rec.path, rec.contentType, rec.body = r.URL.Path, r.Header.Get("Content-Type"), string(b)
			w.WriteHeader(createStatus)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func newFakeClient(srv *httptest.Server) adt.Client {
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
}

func TestObjectURI_HTTPService(t *testing.T) {
	got, err := adt.ObjectURI("HTTP", "ZSVC")
	if err != nil {
		t.Fatalf("ObjectURI: %v", err)
	}
	if want := httpServicesPath + "/zsvc"; got != want {
		t.Errorf("ObjectURI = %q, want %q", got, want)
	}
}

func TestCreateHTTPService_SendsHandlerClass(t *testing.T) {
	srv, rec := newCreateServer(t, http.StatusCreated)

	err := newFakeClient(srv).CreateHTTPService(context.Background(), "ZSVC", "$TMP", "Example service", "zcl_example_handler", "")
	if err != nil {
		t.Fatalf("CreateHTTPService: %v", err)
	}
	if rec.path != httpServicesPath {
		t.Errorf("path = %q, want %q", rec.path, httpServicesPath)
	}
	if rec.contentType != httpServiceType {
		t.Errorf("Content-Type = %q, want %q", rec.contentType, httpServiceType)
	}
	for _, want := range []string{
		`<http:abap`, `xmlns:http="http://www.sap.com/uc_object_type_group/http/abapxml"`,
		`adtcore:name="ZSVC"`, `adtcore:type="HTTP"`, `adtcore:description="Example service"`,
		handlerAttr, `<adtcore:packageRef adtcore:name="$TMP">`,
	} {
		if !strings.Contains(rec.body, want) {
			t.Errorf("body lacks %s:\n%s", want, rec.body)
		}
	}
	if !rec.loggedOut {
		t.Error("CreateHTTPService did not log out after the create (adtler#4 workaround)")
	}
}

// SAP S/4HANA creates and activates an HTTP service without a handler class,
// so CreateObject can create one; it just sends no handler attribute.
func TestCreateObject_HTTPServiceWithoutHandler(t *testing.T) {
	srv, rec := newCreateServer(t, http.StatusCreated)

	if err := newFakeClient(srv).CreateObject(context.Background(), "HTTP", "ZSVC", "$TMP", "Example service", ""); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	if rec.path != httpServicesPath || rec.contentType != httpServiceType {
		t.Errorf("got POST %q as %q", rec.path, rec.contentType)
	}
	if strings.Contains(rec.body, "handler_servicehandler") {
		t.Errorf("body sends a handler attribute although none was given:\n%s", rec.body)
	}
}

func TestCreateHTTPService_UnavailableOnECC(t *testing.T) {
	srv, _ := newCreateServer(t, http.StatusNotFound)

	err := newFakeClient(srv).CreateHTTPService(context.Background(), "ZSVC", "$TMP", "x", "ZCL_EXAMPLE_HANDLER", "")
	if err == nil {
		t.Fatal("CreateHTTPService on a system without the endpoint: got nil error")
	}
	if !strings.Contains(err.Error(), "not available on this SAP system") || !strings.Contains(err.Error(), "HTTP") {
		t.Errorf("error should say HTTP services are not available on this system, got: %v", err)
	}
}

func TestGetHTTPService(t *testing.T) {
	const doc = `<?xml version="1.0" encoding="utf-8"?><http:abap http:handler_id="ZSVC" http:handler_version="A" ` +
		`http:handler_servicehandler="ZCL_EXAMPLE_HANDLER" http:header_canonicalurl="/sap/bc/http/sap/ZSVC" ` +
		`http:text_shorttext="Example service" adtcore:name="ZSVC" adtcore:type="HTTP" adtcore:description="Example service" ` +
		`xmlns:http="http://www.sap.com/uc_object_type_group/http/abapxml" xmlns:adtcore="http://www.sap.com/adt/core"></http:abap>`
	var gotPath, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAccept = r.URL.Path, r.Header.Get("Accept")
		w.Header().Set("Content-Type", httpServiceType)
		_, _ = io.WriteString(w, doc)
	}))
	defer srv.Close()

	svc, err := newFakeClient(srv).GetHTTPService(context.Background(), "ZSVC")
	if err != nil {
		t.Fatalf("GetHTTPService: %v", err)
	}
	if gotPath != httpServicesPath+"/zsvc" || !strings.Contains(gotAccept, httpServiceType) {
		t.Errorf("GET %q with Accept %q", gotPath, gotAccept)
	}
	want := adt.HTTPService{Name: "ZSVC", Description: "Example service", HandlerClass: "ZCL_EXAMPLE_HANDLER", URL: "/sap/bc/http/sap/ZSVC"}
	if *svc != want {
		t.Errorf("GetHTTPService = %+v, want %+v", *svc, want)
	}
}

// S/4 answers 406 to an ETag read of an HTTP service that offers only
// application/xml, which made DeleteObject fail before HTTP had an Accept entry.
func TestDeleteObject_HTTPServiceOffersItsMediaType(t *testing.T) {
	deleted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case r.Method == http.MethodGet && !strings.Contains(r.Header.Get("Accept"), httpServiceType):
			w.WriteHeader(http.StatusNotAcceptable)
		case r.Method == http.MethodGet:
			w.Header().Set("ETag", "etag1")
		case r.Method == http.MethodDelete:
			deleted = true
		}
	}))
	defer srv.Close()

	if err := newFakeClient(srv).DeleteObject(context.Background(), httpServicesPath+"/zsvc", "", ""); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if !deleted {
		t.Error("DELETE was not sent")
	}
}
