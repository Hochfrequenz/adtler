package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	// httpSvcURI is an HTTP service in a registered namespace, so the test also
	// pins that the percent-encoded slashes survive as the uri query parameter.
	httpSvcURI = "/sap/bc/adt/ucon/httpservices/%2fabc%2fsrv"

	// objPropsPath is the object-properties endpoint ADT advertises in discovery.
	objPropsPath = "/sap/bc/adt/repository/informationsystem/objectproperties/values"

	// objPropsType is the media type the object-properties endpoint produces.
	objPropsType = "application/vnd.sap.adt.repository.objproperties.result.v1+xml"

	// httpSvcPackage is the package objPropsXML names.
	httpSvcPackage = "/ABC/PACK"

	// httpSvcXML is an HTTP service object document the way SAP S/4HANA serves
	// it: no adtcore:packageRef at all (adtler#260).
	httpSvcXML = `<?xml version="1.0" encoding="utf-8"?>` +
		`<http:abap xmlns:http="http://www.sap.com/uc_object_type_group/http/abapxml"` +
		` xmlns:adtcore="http://www.sap.com/adt/core"` +
		` http:handler_servicehandler="CL_EXAMPLE"` +
		` adtcore:name="/ABC/SRV" adtcore:type="HTTP" adtcore:description="Example service"/>`

	// objPropsXML is an object-properties answer, reduced to what is read.
	objPropsXML = `<?xml version="1.0" encoding="utf-8"?>` +
		`<opr:objectProperties xmlns:opr="http://www.sap.com/adt/ris/objectProperties">` +
		`<opr:object text="Example service" name="/ABC/SRV" package="` + httpSvcPackage + `" type="HTTP" expandable="false"/>` +
		`<opr:property facet="PACKAGE" name="BASIS" displayName="BASIS" text="Structure package"/>` +
		`<opr:property facet="PACKAGE" name="` + httpSvcPackage + `" displayName="` + httpSvcPackage + `" text="Example package"/>` +
		`</opr:objectProperties>`
)

// httpSvcPackageServer serves httpSvcXML and answers the object-properties
// endpoint with propsStatus (and objPropsXML on 200). It records the query and
// Accept header the object-properties request carried, and counts those
// requests.
type httpSvcPackageServer struct {
	*httptest.Server
	propsGets   int32
	propsURI    atomic.Value
	propsFacet  atomic.Value
	propsAccept atomic.Value
}

func newHTTPSvcPackageServer(svcBody string, propsStatus int) *httpSvcPackageServer {
	s := &httpSvcPackageServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case "/sap/bc/adt/ucon/httpservices//abc/srv", "/sap/bc/adt/ucon/httpservices/%2fabc%2fsrv":
			w.Header().Set("Content-Type", "application/vnd.sap.adt.uconn.http.v1+xml")
			_, _ = w.Write([]byte(svcBody))
		case objPropsPath:
			atomic.AddInt32(&s.propsGets, 1)
			s.propsURI.Store(r.URL.Query().Get("uri"))
			s.propsFacet.Store(r.URL.Query().Get("facet"))
			s.propsAccept.Store(r.Header.Get("Accept"))
			if propsStatus != http.StatusOK {
				w.WriteHeader(propsStatus)
				return
			}
			w.Header().Set("Content-Type", objPropsType)
			_, _ = w.Write([]byte(objPropsXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return s
}

// TestGetObjectInfo_HTTPService_PackageComesFromObjectProperties covers
// adtler#260: an HTTP service document names no package, so GetObjectInfo asks
// the object-properties endpoint, keyed by the object's own URI.
func TestGetObjectInfo_HTTPService_PackageComesFromObjectProperties(t *testing.T) {
	srv := newHTTPSvcPackageServer(httpSvcXML, http.StatusOK)
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), httpSvcURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != httpSvcPackage {
		t.Errorf("package: got %q, want %q", info.PackageName, httpSvcPackage)
	}
	if info.Name != "/ABC/SRV" || info.URI != httpSvcURI {
		t.Errorf("the result must still describe the service: got name %q, URI %q", info.Name, info.URI)
	}
	if got := atomic.LoadInt32(&srv.propsGets); got != 1 {
		t.Fatalf("object properties requested %d times, want 1", got)
	}
	if got := srv.propsURI.Load(); got != httpSvcURI {
		t.Errorf("uri parameter: got %q, want the object URI %q unchanged", got, httpSvcURI)
	}
	if got := srv.propsFacet.Load(); got != "package" {
		t.Errorf("facet parameter: got %q, want %q", got, "package")
	}
	if got, _ := srv.propsAccept.Load().(string); !strings.Contains(got, objPropsType) {
		t.Errorf("Accept: got %q, want it to name %q", got, objPropsType)
	}
}

// TestGetObjectInfo_HTTPService_OwnPackageSavesTheLookup keeps the lookup from
// costing a request should a release ever put the packageRef into the document.
func TestGetObjectInfo_HTTPService_OwnPackageSavesTheLookup(t *testing.T) {
	withRef := strings.Replace(httpSvcXML, `/>`, `><adtcore:packageRef adtcore:name="ZOWN"/></http:abap>`, 1)
	srv := newHTTPSvcPackageServer(withRef, http.StatusOK)
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), httpSvcURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != "ZOWN" {
		t.Errorf("package: got %q, want the document's own %q", info.PackageName, "ZOWN")
	}
	if got := atomic.LoadInt32(&srv.propsGets); got != 0 {
		t.Errorf("object properties requested %d times, want 0", got)
	}
}

// TestGetObjectInfo_HTTPService_LookupFailureKeepsTheObject pins the best-effort
// contract shared with function modules: the service itself was read, so a
// failed package lookup leaves the package empty instead of failing the call.
func TestGetObjectInfo_HTTPService_LookupFailureKeepsTheObject(t *testing.T) {
	srv := newHTTPSvcPackageServer(httpSvcXML, http.StatusForbidden)
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), httpSvcURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.Name != "/ABC/SRV" || info.PackageName != "" {
		t.Errorf("got name %q, package %q; want the service with an empty package", info.Name, info.PackageName)
	}
	if got := atomic.LoadInt32(&srv.propsGets); got != 1 {
		t.Errorf("object properties requested %d times, want 1", got)
	}
}

// cancelOnPathTransport cancels the caller's context when path is requested,
// the way a caller that gives up during the second request would.
type cancelOnPathTransport struct {
	path   string
	cancel context.CancelFunc
}

func (tr cancelOnPathTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == tr.path {
		tr.cancel()
		return nil, req.Context().Err()
	}
	return http.DefaultTransport.RoundTrip(req)
}

// TestGetObjectInfo_HTTPService_CancelledDuringLookup pins that a caller who
// cancels during the package lookup gets the cancellation, not a success that
// looks like a service without a package.
func TestGetObjectInfo_HTTPService_CancelledDuringLookup(t *testing.T) {
	srv := newHTTPSvcPackageServer(httpSvcXML, http.StatusOK)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := adt.NewClientWithTransport(
		sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"},
		cancelOnPathTransport{path: objPropsPath, cancel: cancel})

	info, err := client.GetObjectInfo(ctx, httpSvcURI)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetObjectInfo: got info %v, error %v, want context.Canceled", info, err)
	}
}
