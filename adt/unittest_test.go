package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestRunUnitTests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == aunitTestRunsPath {
			body, _ := io.ReadAll(r.Body)
			reqBody := string(body)
			if !strings.Contains(reqBody, "aunit:runConfiguration") {
				t.Error("request body missing aunit:runConfiguration root element")
			}
			if !strings.Contains(reqBody, "objectSet") {
				t.Error("request body missing objectSet element")
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0"?>
<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">
  <program adtcore:uri="/sap/bc/adt/classes/classes/ZCL_TEST" adtcore:name="ZCL_TEST">
    <testClasses><testClass adtcore:name="ZCL_TEST" aunit:testCount="2" aunit:errorCount="0" aunit:failureCount="1">
      <testMethods>
        <testMethod adtcore:name="TEST_PASS" executionTime="0.001"><alerts/></testMethod>
        <testMethod adtcore:name="TEST_FAIL" executionTime="0.002">
          <alerts><alert kind="failedAssertion" severity="critical"><title>Assertion failed</title></alert></alerts>
        </testMethod>
      </testMethods>
    </testClass></testClasses>
  </program>
</aunit:runResult>`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.RunUnitTests(context.Background(), "/sap/bc/adt/classes/classes/ZCL_TEST", 30)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed != 1 {
		t.Errorf("passed: got %d, want 1", result.Passed)
	}
	if result.Failed != 1 {
		t.Errorf("failed: got %d, want 1", result.Failed)
	}
	if len(result.TestCases) != 2 {
		t.Fatalf("expected 2 test cases, got %d", len(result.TestCases))
	}
}

// emptyRunResult816 is SAP_BASIS 816's answer for a run that executed no
// test method, captured verbatim.
const emptyRunResult816 = `<?xml version="1.0" encoding="utf-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"/>`

// unitTestServer mocks the ABAP Unit run endpoint and the inactive-objects
// read. runBody is written verbatim as the run response. inactiveBody is the
// inactive-objects response; inactiveStatus, if non-zero, makes that read fail
// with the given status instead. inactiveReads counts the inactive-objects
// requests.
func unitTestServer(t *testing.T, runBody, inactiveBody string, inactiveStatus int, inactiveReads *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case aunitTestRunsPath:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(runBody))
		case inactiveObjectsPath:
			inactiveReads.Add(1)
			if inactiveStatus != 0 {
				w.WriteHeader(inactiveStatus)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(inactiveBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// runUnitTestsAgainst runs RunUnitTests for objectURI against srv.
func runUnitTestsAgainst(t *testing.T, srv *httptest.Server, objectURI string) (*adt.TestResult, error) {
	t.Helper()
	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	return adt.NewClient(cfg).RunUnitTests(context.Background(), objectURI, 30)
}

// TestRunUnitTests_UnparsableBody pins case 3 of #212: a response that is not
// an ABAP Unit run result must be an error, not an empty result.
func TestRunUnitTests_UnparsableBody(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, "<html><body>gateway error</body></html>", "", 0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err == nil {
		t.Fatalf("expected a parse error, got result %+v", result)
	}
	if !strings.Contains(err.Error(), "parsing") {
		t.Errorf("err = %v, want a parsing error", err)
	}
}

// TestRunUnitTests_EmptyRunResult pins that S/4's answer for a run without
// tests, an empty <aunit:runResult/> element, is a valid result and not a
// parse failure.
func TestRunUnitTests_EmptyRunResult(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, emptyRunResult816,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"/>`,
		0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.TestCases) != 0 || len(result.Alerts) != 0 {
		t.Errorf("expected an empty result, got %+v", result)
	}
}

// TestRunUnitTests_OutsideMethodAlerts pins that alerts SAP attaches to the
// run, to a program or to a test class, outside any test method, reach
// TestResult.Alerts with their kind, in document order. The run-level alert
// and the test-class alert are SAP_BASIS 750 captures with names replaced by
// placeholders; only the program-level alert is synthetic (no capture carried
// one).
func TestRunUnitTests_OutsideMethodAlerts(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, `<?xml version="1.0" encoding="utf-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"><alerts><alert kind="noTestClasses" severity="tolerable"><title>Program 'ZCL_TEST======================CP' Does not Contain any Test Classes.</title><details><detail text="You can find further informations in document &lt;CHAP&gt; &lt;SAUNIT_NO_TEST_CLASS&gt;"><link rel=""/></detail></details><stack/></alert></alerts>
  <program adtcore:uri="/sap/bc/adt/oo/classes/zcl_test" adtcore:name="ZCL_TEST" xmlns:adtcore="http://www.sap.com/adt/core">
    <alerts><alert kind="warning" severity="tolerable"><title>Program-level alert</title></alert></alerts>
    <testClasses><testClass adtcore:name="LTC_TEST">
      <alerts><alert kind="warning" severity="tolerable"><title>No execution, risk level of test class exceeds upper limit</title><details><detail text="You can find further informations in document &lt;CHAP&gt; &lt;SAUNIT_TEST_PROPS&gt;"><link rel=""/></detail></details><stack/></alert></alerts><testMethods/>
    </testClass></testClasses>
  </program>
</aunit:runResult>`,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"/>`,
		0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []adt.TestAlert{
		{Kind: "noTestClasses", Severity: "tolerable", Title: "Program 'ZCL_TEST======================CP' Does not Contain any Test Classes."},
		{Kind: "warning", Severity: "tolerable", Title: "Program-level alert"},
		{Kind: "warning", Severity: "tolerable", Title: "No execution, risk level of test class exceeds upper limit"},
	}
	if !slices.Equal(result.Alerts, want) {
		t.Errorf("Alerts = %+v, want %+v", result.Alerts, want)
	}
}

// TestRunUnitTests_ZeroTests_ReportsInactiveParts is the regression guard for
// case 2 of #212: when no test method ran, the inactive parts related to the
// requested object are listed, so an inactive test-classes include no longer
// looks like an object without tests. A sibling class whose name merely
// extends the requested one, and an unrelated class, must not be listed.
func TestRunUnitTests_ZeroTests_ReportsInactiveParts(t *testing.T) {
	const base = "/sap/bc/adt/oo/classes/zcl_test"
	want := []string{
		base,
		base + "/includes/testclasses",
		base + "/source/main#type=CLAS%2FOM;name=GET",
	}
	var entries strings.Builder
	for _, uri := range append(slices.Clone(want), "/sap/bc/adt/oo/classes/zcl_test2", "/sap/bc/adt/oo/classes/zcl_other") {
		entries.WriteString(`<entry><object><ref uri="` + uri + `" type="CLAS/OC" name="X" packageName="$TMP"/></object></entry>`)
	}
	var reads atomic.Int32
	srv := unitTestServer(t, emptyRunResult816,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core">`+entries.String()+`</ioc:inactiveObjects>`,
		0, &reads)
	defer srv.Close()

	// Upper-case on purpose: callers pass upper-case names, SAP lists lower case.
	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/ZCL_TEST")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !slices.Equal(result.InactiveURIs, want) {
		t.Errorf("InactiveURIs = %q, want %q", result.InactiveURIs, want)
	}
}

// TestRunUnitTests_TestsExecuted_SkipsInactiveCheck pins that the extra read
// happens only for a run that executed nothing.
func TestRunUnitTests_TestsExecuted_SkipsInactiveCheck(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, `<?xml version="1.0" encoding="utf-8"?>
<aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit" xmlns:adtcore="http://www.sap.com/adt/core">
  <program adtcore:uri="/sap/bc/adt/oo/classes/zcl_test" adtcore:name="ZCL_TEST">
    <testClasses><testClass adtcore:name="LTC_TEST">
      <testMethods><testMethod adtcore:name="RUNS" executionTime="0"/></testMethods>
    </testClass></testClasses>
  </program>
</aunit:runResult>`, "", 0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Passed != 1 {
		t.Errorf("Passed = %d, want 1", result.Passed)
	}
	if n := reads.Load(); n != 0 {
		t.Errorf("inactive-objects reads = %d, want 0", n)
	}
	if result.InactiveURIs != nil {
		t.Errorf("InactiveURIs = %q, want nil", result.InactiveURIs)
	}
}

// TestRunUnitTests_ZeroTests_InactiveReadFails pins that a failing
// inactive-objects read only leaves InactiveURIs empty; the run result itself
// is still returned without an error.
func TestRunUnitTests_ZeroTests_InactiveReadFails(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, emptyRunResult816, "", http.StatusInternalServerError, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n := reads.Load(); n != 1 {
		t.Errorf("inactive-objects reads = %d, want 1", n)
	}
	if result.InactiveURIs != nil {
		t.Errorf("InactiveURIs = %q, want nil", result.InactiveURIs)
	}
}

// TestRunUnitTests_RuntimeAbortionClassAlert pins the SAP_BASIS 750 answer for
// a test method that ends in an uncatchable runtime error: the test class
// comes back with no test method and a test-class alert of kind
// "runtimeAbortion". Before #212 this read as a clean 0/0/0 run. Captured
// verbatim, with names replaced by placeholders.
func TestRunUnitTests_RuntimeAbortionClassAlert(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, `<?xml version="1.0" encoding="utf-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"><alerts/><program adtcore:uri="/sap/bc/adt/oo/classes/zcl_test" adtcore:type="CLAS/OC" adtcore:name="ZCL_TEST" adtcore:packageName="$TMP" xmlns:adtcore="http://www.sap.com/adt/core"><alerts/><testClasses><testClass adtcore:uri="/sap/bc/adt/oo/classes/zcl_test/includes/testclasses#start=6,6" adtcore:type="CLAS/OCN/testclasses" adtcore:name="LTC" adtcore:packageName="$TMP"><alerts><alert kind="runtimeAbortion" severity="fatal"><title>Runtime Error &lt;GETWA_NOT_ASSIGNED&gt;</title><details><detail text="[Field symbol has not been assigned yet.]"><link rel=""/></detail><detail text="Test Class 'LTC' in Main Program 'ZCL_TEST======================CP'."><link rel=""/></detail></details><stack><stackEntry adtcore:uri="/sap/bc/adt/oo/classes/zcl_test/includes/testclasses#start=10,0" adtcore:type="CLAS/OCN/testclasses" adtcore:name="ZCL_TEST" adtcore:packageName="$TMP" adtcore:description="Include: &lt;ZCL_TEST======================CCAU&gt; Line: &lt;10&gt;"/></stack></alert></alerts><testMethods/></testClass></testClasses></program></aunit:runResult>`,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"/>`,
		0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.TestCases) != 0 {
		t.Errorf("TestCases = %+v, want none", result.TestCases)
	}
	want := []adt.TestAlert{{Kind: "runtimeAbortion", Severity: "fatal", Title: "Runtime Error <GETWA_NOT_ASSIGNED>"}}
	if !slices.Equal(result.Alerts, want) {
		t.Errorf("Alerts = %+v, want %+v", result.Alerts, want)
	}
}

// TestRunUnitTests_EmptyBody pins that an empty 200 body is reported as such
// rather than as an opaque parse error or an empty result.
func TestRunUnitTests_EmptyBody(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, "", "", 0, &reads)
	defer srv.Close()

	_, err := runUnitTestsAgainst(t, srv, "/sap/bc/adt/oo/classes/zcl_test")
	if err == nil || !strings.Contains(err.Error(), "empty response body") {
		t.Fatalf("err = %v, want an empty-response-body error", err)
	}
}

// TestRunUnitTests_ZeroTests_EmptyURIListsNothing pins that an empty object
// URI does not list every inactive object: objectURIMatches would treat it as
// a prefix of all of them.
func TestRunUnitTests_ZeroTests_EmptyURIListsNothing(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, emptyRunResult816,
		`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"><entry><object><ref uri="/sap/bc/adt/oo/classes/zcl_other" type="CLAS/OC" name="X" packageName="$TMP"/></object></entry></ioc:inactiveObjects>`,
		0, &reads)
	defer srv.Close()

	result, err := runUnitTestsAgainst(t, srv, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.InactiveURIs != nil {
		t.Errorf("InactiveURIs = %q, want nil", result.InactiveURIs)
	}
}
