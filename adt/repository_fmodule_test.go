package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	// fmoduleURI is a function module URI as search_objects returns it.
	fmoduleURI = "/sap/bc/adt/functions/groups/zmy_fg/fmodules/zmy_fm"

	// fmoduleAccept is the one media type SAP S/4HANA produces for a function
	// module. It is also what the 406 response of adtler#169 names.
	fmoduleAccept = "application/vnd.sap.adt.functions.fmodules.v3+xml"

	// fmoduleName is the name carried by every function module document below.
	fmoduleName = "ZMY_FM"

	// fmoduleXML is a function module object document, reduced to what
	// parseGenericObjectInfo reads.
	fmoduleXML = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<fmodule:abapFunctionModule xmlns:fmodule="http://www.sap.com/adt/functions/fmodules"` +
		` xmlns:adtcore="http://www.sap.com/adt/core"` +
		` adtcore:name="` + fmoduleName + `" adtcore:type="FUGR/FF" adtcore:description="Example function module">` +
		`<adtcore:packageRef adtcore:name="ZFMPACK"/></fmodule:abapFunctionModule>`
)

// strictFunctionModuleServer behaves like SAP S/4HANA for a function module:
// it answers 200 only to a request whose Accept header names the fmodules
// media type and 406 to everything else, including */*. Refusing */* too
// means a client that reaches the object only through the 406 retry fails
// this server — the first request has to be right.
func strictFunctionModuleServer(offers *[]string, mu *sync.Mutex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != fmoduleURI {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		accept := r.Header.Get("Accept")
		mu.Lock()
		*offers = append(*offers, accept)
		mu.Unlock()
		if !strings.Contains(accept, fmoduleAccept) {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte(notAcceptableXML))
			return
		}
		w.Header().Set("Content-Type", fmoduleAccept)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(fmoduleXML))
	}))
}

// TestGetObjectInfo_FunctionModule_FirstRequestCarriesFmodulesType covers
// adtler#169: the first request for a function module must already offer the
// fmodules media type, so the lookup does not cost a 406 and a retry on
// S/4HANA. The same call must also report the URI it was asked for.
func TestGetObjectInfo_FunctionModule_FirstRequestCarriesFmodulesType(t *testing.T) {
	var (
		offers []string
		mu     sync.Mutex
	)
	srv := strictFunctionModuleServer(&offers, &mu)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	info, err := client.GetObjectInfo(context.Background(), fmoduleURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(offers) != 1 {
		t.Fatalf("server saw %d requests (%v), want exactly 1 — a retry means the first offer was wrong", len(offers), offers)
	}
	if !strings.Contains(offers[0], fmoduleAccept) {
		t.Errorf("first offer was %q, want it to contain %q", offers[0], fmoduleAccept)
	}
	if info.Name != fmoduleName {
		t.Errorf("name: got %q, want %q", info.Name, fmoduleName)
	}
	if info.PackageName != "ZFMPACK" {
		t.Errorf("package: got %q, want %q", info.PackageName, "ZFMPACK")
	}
	if info.URI != fmoduleURI {
		t.Errorf("URI: got %q, want %q — GetObjectInfo must report the URI it fetched", info.URI, fmoduleURI)
	}
}

const (
	// fmoduleGroupURI is the function group that contains fmoduleURI.
	fmoduleGroupURI = "/sap/bc/adt/functions/groups/zmy_fg"

	// fmoduleXMLWithoutPackage is a function module object document the way SAP
	// ERP 6.0 and SAP S/4HANA serve it: no package reference at all (adtler#169).
	fmoduleXMLWithoutPackage = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<fmodule:abapFunctionModule xmlns:fmodule="http://www.sap.com/adt/functions/fmodules"` +
		` xmlns:adtcore="http://www.sap.com/adt/core"` +
		` adtcore:name="` + fmoduleName + `" adtcore:type="FUGR/FF" adtcore:description="Example function module"/>`

	// fmoduleGroupXML is the function group document, which does name its package.
	fmoduleGroupXML = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<group:abapFunctionGroup xmlns:group="http://www.sap.com/adt/functions/groups"` +
		` xmlns:adtcore="http://www.sap.com/adt/core"` +
		` adtcore:name="ZMY_FG" adtcore:type="FUGR/F">` +
		`<adtcore:packageRef adtcore:name="ZGROUPPACK"/></group:abapFunctionGroup>`
)

// packageLookupServer serves a function module document (fmoduleBody) and the
// function group document of its group (groupStatus, fmoduleGroupXML). It
// counts the GETs each of the two documents received.
func packageLookupServer(fmoduleBody string, groupStatus int, fmoduleGets, groupGets *int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case fmoduleURI:
			atomic.AddInt32(fmoduleGets, 1)
			w.Header().Set("Content-Type", fmoduleAccept)
			_, _ = w.Write([]byte(fmoduleBody))
		case fmoduleGroupURI:
			atomic.AddInt32(groupGets, 1)
			if groupStatus != http.StatusOK {
				w.WriteHeader(groupStatus)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(fmoduleGroupXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestGetObjectInfo_FunctionModule_PackageComesFromFunctionGroup covers the
// package half of adtler#169: a function module belongs to the package of its
// function group, and its own document does not say which.
func TestGetObjectInfo_FunctionModule_PackageComesFromFunctionGroup(t *testing.T) {
	var fmoduleGets, groupGets int32
	srv := packageLookupServer(fmoduleXMLWithoutPackage, http.StatusOK, &fmoduleGets, &groupGets)
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), fmoduleURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != "ZGROUPPACK" {
		t.Errorf("package: got %q, want the function group's package %q", info.PackageName, "ZGROUPPACK")
	}
	if info.Name != fmoduleName || info.URI != fmoduleURI {
		t.Errorf("the result must still describe the function module: got name %q, URI %q", info.Name, info.URI)
	}
	if got := atomic.LoadInt32(&groupGets); got != 1 {
		t.Errorf("function group document fetched %d times, want 1", got)
	}
}

// TestGetObjectInfo_FunctionModule_OwnPackageSavesTheGroupLookup keeps the
// lookup from costing a request when the document already names a package.
func TestGetObjectInfo_FunctionModule_OwnPackageSavesTheGroupLookup(t *testing.T) {
	var fmoduleGets, groupGets int32
	srv := packageLookupServer(fmoduleXML, http.StatusOK, &fmoduleGets, &groupGets)
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), fmoduleURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != "ZFMPACK" {
		t.Errorf("package: got %q, want the document's own package %q", info.PackageName, "ZFMPACK")
	}
	if got := atomic.LoadInt32(&groupGets); got != 0 {
		t.Errorf("function group document fetched %d times, want 0 — the document already named a package", got)
	}
}

// TestGetObjectInfo_FunctionModule_GroupLookupFailureKeepsTheObject pins what
// happens when the group cannot be read: the function module was read fine, so
// the call still returns it, without a package, rather than failing as a whole.
func TestGetObjectInfo_FunctionModule_GroupLookupFailureKeepsTheObject(t *testing.T) {
	var fmoduleGets, groupGets int32
	srv := packageLookupServer(fmoduleXMLWithoutPackage, http.StatusForbidden, &fmoduleGets, &groupGets)
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), fmoduleURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.Name != fmoduleName {
		t.Errorf("name: got %q, want %q", info.Name, fmoduleName)
	}
	if info.PackageName != "" {
		t.Errorf("package: got %q, want empty when the group could not be read", info.PackageName)
	}
}

// TestGetObjectInfo_FunctionModule_GeneratedGroupHasNoPackage covers a function
// group SAP generated for its own use: it is in no package, so both documents
// name none and the answer stays empty after exactly one group lookup.
func TestGetObjectInfo_FunctionModule_GeneratedGroupHasNoPackage(t *testing.T) {
	var groupGets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case fmoduleURI:
			_, _ = w.Write([]byte(fmoduleXMLWithoutPackage))
		case fmoduleGroupURI:
			atomic.AddInt32(&groupGets, 1)
			_, _ = w.Write([]byte(strings.Replace(fmoduleGroupXML, `<adtcore:packageRef adtcore:name="ZGROUPPACK"/>`, "", 1)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), fmoduleURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != "" {
		t.Errorf("package: got %q, want empty — a generated group is in no package", info.PackageName)
	}
	if got := atomic.LoadInt32(&groupGets); got != 1 {
		t.Errorf("function group document fetched %d times, want 1", got)
	}
}

// TestGetObjectInfo_FunctionGroup_NoSecondLookup keeps the fallback to function
// modules: a function group without a package is not looked up again, and
// neither is anything else that is not a function module.
func TestGetObjectInfo_FunctionGroup_NoSecondLookup(t *testing.T) {
	var groupGets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case fmoduleGroupURI:
			atomic.AddInt32(&groupGets, 1)
			_, _ = w.Write([]byte(strings.Replace(fmoduleGroupXML, `<adtcore:packageRef adtcore:name="ZGROUPPACK"/>`, "", 1)))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), fmoduleGroupURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != "" {
		t.Errorf("package: got %q, want empty", info.PackageName)
	}
	if got := atomic.LoadInt32(&groupGets); got != 1 {
		t.Errorf("function group document fetched %d times, want 1 — only function modules fall back to their group", got)
	}
}

// cancelOnGroupTransport cancels the caller's context when the function group is
// requested, the way a caller that gives up during the second request would.
type cancelOnGroupTransport struct {
	next   http.RoundTripper
	cancel context.CancelFunc
}

func (tr cancelOnGroupTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == fmoduleGroupURI {
		tr.cancel()
		return nil, req.Context().Err()
	}
	return tr.next.RoundTrip(req)
}

// TestGetObjectInfo_FunctionModule_CancelledDuringGroupLookup pins that a caller
// who cancels during the group lookup gets the cancellation, not a success that
// looks like a function module without a package.
func TestGetObjectInfo_FunctionModule_CancelledDuringGroupLookup(t *testing.T) {
	var fmoduleGets, groupGets int32
	srv := packageLookupServer(fmoduleXMLWithoutPackage, http.StatusOK, &fmoduleGets, &groupGets)
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client := adt.NewClientWithTransport(
		sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"},
		cancelOnGroupTransport{next: http.DefaultTransport, cancel: cancel})

	info, err := client.GetObjectInfo(ctx, fmoduleURI)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("GetObjectInfo: got info %v, error %v, want context.Canceled", info, err)
	}
}

// TestGetObjectInfo_FunctionModule_NamespacedNames covers function modules in a
// registered namespace, whose URIs carry the namespace slashes percent-encoded.
// The group URI has to keep that encoding: cutting at /fmodules/ must not
// decode or lose it, or the group request would address a different path.
func TestGetObjectInfo_FunctionModule_NamespacedNames(t *testing.T) {
	const (
		nsFmoduleURI = "/sap/bc/adt/functions/groups/%2fabc%2ffg/fmodules/%2fabc%2ffm"
		nsGroupURI   = "/sap/bc/adt/functions/groups/%2fabc%2ffg"
	)
	var (
		mu      sync.Mutex
		gotPath []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		escaped := strings.ToLower(r.URL.EscapedPath())
		if r.URL.Path != csrfEndpoint {
			mu.Lock()
			gotPath = append(gotPath, escaped)
			mu.Unlock()
		}
		switch escaped {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case nsFmoduleURI:
			_, _ = w.Write([]byte(fmoduleXMLWithoutPackage))
		case nsGroupURI:
			_, _ = w.Write([]byte(fmoduleGroupXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), nsFmoduleURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != "ZGROUPPACK" {
		t.Errorf("package: got %q, want the function group's package %q (requests seen: %v)", info.PackageName, "ZGROUPPACK", gotPath)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(gotPath) != 2 || gotPath[0] != nsFmoduleURI || gotPath[1] != nsGroupURI {
		t.Errorf("requests: got %v, want the function module then its group, namespace slashes still encoded", gotPath)
	}
}

const (
	// fincludeURI is a function group include URI as search_objects returns it.
	fincludeURI = fmoduleGroupURI + "/includes/lzmy_fgtop"

	// fincludeXML is a function group include document the way SAP ERP 6.0 and
	// SAP S/4HANA serve it (adtler#232): no packageRef, only a containerRef to
	// the function group, which GetObjectInfo does not read as the package.
	fincludeXML = `<?xml version="1.0" encoding="UTF-8"?>` +
		`<finclude:abapFunctionGroupInclude xmlns:finclude="http://www.sap.com/adt/functions/fincludes"` +
		` xmlns:adtcore="http://www.sap.com/adt/core"` +
		` adtcore:name="LZMY_FGTOP" adtcore:type="FUGR/I">` +
		`<adtcore:containerRef adtcore:uri="` + fmoduleGroupURI + `" adtcore:type="FUGR/F" adtcore:name="ZMY_FG" adtcore:packageName="ZGROUPPACK"/>` +
		`</finclude:abapFunctionGroupInclude>`
)

// TestGetObjectInfo_FunctionGroupInclude_PackageComesFromFunctionGroup covers
// adtler#232: a function group include, like a function module, belongs to the
// package of its function group, and its own document names no package.
func TestGetObjectInfo_FunctionGroupInclude_PackageComesFromFunctionGroup(t *testing.T) {
	var includeGets, groupGets int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
		case fincludeURI:
			atomic.AddInt32(&includeGets, 1)
			_, _ = w.Write([]byte(fincludeXML))
		case fmoduleGroupURI:
			atomic.AddInt32(&groupGets, 1)
			_, _ = w.Write([]byte(fmoduleGroupXML))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	info, err := client.GetObjectInfo(context.Background(), fincludeURI)
	if err != nil {
		t.Fatalf("GetObjectInfo: unexpected error: %v", err)
	}
	if info.PackageName != "ZGROUPPACK" {
		t.Errorf("package: got %q, want the function group's package %q", info.PackageName, "ZGROUPPACK")
	}
	if info.Name != "LZMY_FGTOP" || info.URI != fincludeURI {
		t.Errorf("the result must still describe the include: got name %q, URI %q", info.Name, info.URI)
	}
	if got := atomic.LoadInt32(&includeGets); got != 1 {
		t.Errorf("include document fetched %d times, want 1", got)
	}
	if got := atomic.LoadInt32(&groupGets); got != 1 {
		t.Errorf("function group document fetched %d times, want 1", got)
	}
}
