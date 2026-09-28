package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// Issue #56: ADTError discarded the <properties> map and the embedded T100
// message-key. These tests drive the parser via SetSource, the same
// entrypoint TestADTErrorParsed uses, so parseADTError's real code path is
// exercised rather than a struct built by hand.

func serveExceptionBody(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(body))
	}))
}

func setSourceErr(t *testing.T, srv *httptest.Server) *adt.ADTError {
	t.Helper()
	cfg := newTestConfig(srv.URL)
	client := adt.NewClient(cfg)
	_, err := client.SetSource(context.Background(), "/sap/bc/adt/programs/programs/ZTEST", "REPORT ZTEST.", "", "", `"etag123"`)
	if err == nil {
		t.Fatal("expected error")
	}
	adtErr, ok := err.(*adt.ADTError)
	if !ok {
		t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
	}
	return adtErr
}

// #56 reproducer: 403 / EU/510 (S/4) — "currently editing" own-enqueue collision.
const euEnqueueLockBody = `<?xml version="1.0"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceNoAccess"/>
  <message lang="EN">User DACHNERM is currently editing Z_ADT_MCP_TEST_REPORT</message>
  <properties>
    <entry key="T100KEY-ID">EU</entry>
    <entry key="T100KEY-NO">510</entry>
    <entry key="T100KEY-V1">DACHNERM</entry>
    <entry key="T100KEY-V2">Z_ADT_MCP_TEST_REPORT</entry>
  </properties>
</exc:exception>`

func TestADTError_PropertiesParsed(t *testing.T) {
	srv := serveExceptionBody(euEnqueueLockBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	want := map[string]string{
		"T100KEY-ID": "EU",
		"T100KEY-NO": "510",
		"T100KEY-V1": "DACHNERM",
		"T100KEY-V2": "Z_ADT_MCP_TEST_REPORT",
	}
	if len(adtErr.Properties) != len(want) {
		t.Fatalf("Properties: got %v, want %v", adtErr.Properties, want)
	}
	for k, v := range want {
		if adtErr.Properties[k] != v {
			t.Errorf("Properties[%q]: got %q, want %q", k, adtErr.Properties[k], v)
		}
	}
}

func TestADTError_T100KeyPromoted(t *testing.T) {
	srv := serveExceptionBody(euEnqueueLockBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	if adtErr.T100KeyID != "EU" {
		t.Errorf("T100KeyID: got %q, want %q", adtErr.T100KeyID, "EU")
	}
	if adtErr.T100KeyNo != "510" {
		t.Errorf("T100KeyNo: got %q, want %q", adtErr.T100KeyNo, "510")
	}
	wantVars := [4]string{"DACHNERM", "Z_ADT_MCP_TEST_REPORT", "", ""}
	if adtErr.T100Vars != wantVars {
		t.Errorf("T100Vars: got %v, want %v", adtErr.T100Vars, wantVars)
	}
}

// ECC bodies are sparser: only a bare top-level corrNr, no T100KEY at all.
// The parser must tolerate any subset rather than requiring specific keys.
const eccCorrNrOnlyBody = `<?xml version="1.0"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceSaveFailure"/>
  <message lang="EN">Object locked</message>
  <properties>
    <entry key="corrNr">S4UK902339</entry>
  </properties>
</exc:exception>`

func TestADTError_PropertiesParsed_SparseECCBody(t *testing.T) {
	srv := serveExceptionBody(eccCorrNrOnlyBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	if got := adtErr.Properties["corrNr"]; got != "S4UK902339" {
		t.Errorf("Properties[corrNr]: got %q, want %q", got, "S4UK902339")
	}
	if adtErr.T100KeyID != "" || adtErr.T100KeyNo != "" {
		t.Errorf("expected empty T100Key fields, got ID=%q NO=%q", adtErr.T100KeyID, adtErr.T100KeyNo)
	}
}

func TestADTError_NoProperties_LeavesMapNilOrEmpty(t *testing.T) {
	const body = `<?xml version="1.0"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceLocked"/>
  <message lang="EN">Object is locked</message>
</exc:exception>`
	srv := serveExceptionBody(body)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	if len(adtErr.Properties) != 0 {
		t.Errorf("Properties: got %v, want empty", adtErr.Properties)
	}
}

func TestADTError_IsEnqueueLock_MatchesEU510(t *testing.T) {
	srv := serveExceptionBody(euEnqueueLockBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	user, object, ok := adtErr.IsEnqueueLock()
	if !ok {
		t.Fatalf("IsEnqueueLock: got ok=false, want true (err: %v)", adtErr)
	}
	if user != "DACHNERM" {
		t.Errorf("user: got %q, want %q", user, "DACHNERM")
	}
	if object != "Z_ADT_MCP_TEST_REPORT" {
		t.Errorf("object: got %q, want %q", object, "Z_ADT_MCP_TEST_REPORT")
	}
}

func TestADTError_IsEnqueueLock_NoMatchOnUnrelatedType(t *testing.T) {
	srv := serveExceptionBody(eccCorrNrOnlyBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	if _, _, ok := adtErr.IsEnqueueLock(); ok {
		t.Errorf("IsEnqueueLock: got ok=true, want false for unrelated error")
	}
}

// #56 reproducer: 500 / CTS_WBO_API/020 (S/4) — transport conflict, the
// blocking transport is recoverable from the structured corrNr property.
const ctsTransportLockedBody = `<?xml version="1.0"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceSaveFailure"/>
  <message lang="EN">Object R3TR PROG Z_ADT_MCP_TEST_REPORT is already locked in request S4UK902339 of user KLEINK</message>
  <properties>
    <entry key="corrNr">S4UK902339</entry>
    <entry key="T100KEY-ID">CTS_WBO_API</entry>
    <entry key="T100KEY-NO">020</entry>
    <entry key="T100KEY-V3">S4UK902339</entry>
    <entry key="T100KEY-V4">KLEINK</entry>
  </properties>
</exc:exception>`

func TestADTError_IsTransportLocked_MatchesCTSWBOAPI020(t *testing.T) {
	srv := serveExceptionBody(ctsTransportLockedBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	corrNr, owner, ok := adtErr.IsTransportLocked()
	if !ok {
		t.Fatalf("IsTransportLocked: got ok=false, want true (err: %v)", adtErr)
	}
	if corrNr != "S4UK902339" {
		t.Errorf("corrNr: got %q, want %q", corrNr, "S4UK902339")
	}
	if owner != "KLEINK" {
		t.Errorf("owner: got %q, want %q", owner, "KLEINK")
	}
}

func TestADTError_IsTransportLocked_NoMatchWithoutCTSWBOAPIKey(t *testing.T) {
	srv := serveExceptionBody(euEnqueueLockBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	if _, _, ok := adtErr.IsTransportLocked(); ok {
		t.Errorf("IsTransportLocked: got ok=true, want false for unrelated error")
	}
}

// #56 reproducer: 423 / SADT_RESOURCE/026 (S/4) — invalid lock handle.
// IsInvalidLockHandle lifts the package-internal isInvalidLockHandle to an
// exported ADTError predicate.
func TestADTError_IsInvalidLockHandle_MatchesType(t *testing.T) {
	const body = `<?xml version="1.0"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionResourceInvalidLockHandle"/>
  <message lang="EN">Resource INCLUDE Z_ADT_MCP_TEST_REPORT is not locked (invalid lock handle: DEADBEEF)</message>
  <properties>
    <entry key="T100KEY-ID">SADT_RESOURCE</entry>
    <entry key="T100KEY-NO">026</entry>
    <entry key="T100KEY-V3">DEADBEEF</entry>
  </properties>
</exc:exception>`
	srv := serveExceptionBody(body)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	if !adtErr.IsInvalidLockHandle() {
		t.Errorf("IsInvalidLockHandle: got false, want true")
	}
}

func TestADTError_IsInvalidLockHandle_FalseForUnrelatedType(t *testing.T) {
	srv := serveExceptionBody(euEnqueueLockBody)
	defer srv.Close()

	adtErr := setSourceErr(t, srv)

	if adtErr.IsInvalidLockHandle() {
		t.Errorf("IsInvalidLockHandle: got true, want false")
	}
}
