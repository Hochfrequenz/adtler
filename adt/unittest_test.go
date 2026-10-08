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
// is the SAP_BASIS 750 capture for a class without active test classes, with
// the class name replaced by a placeholder; the program- and class-level
// alerts are synthetic.
func TestRunUnitTests_OutsideMethodAlerts(t *testing.T) {
	var reads atomic.Int32
	srv := unitTestServer(t, `<?xml version="1.0" encoding="utf-8"?><aunit:runResult xmlns:aunit="http://www.sap.com/adt/aunit"><alerts><alert kind="noTestClasses" severity="tolerable"><title>Program 'ZCL_TEST======================CP' Does not Contain any Test Classes.</title><details><detail text="You can find further informations in document &lt;CHAP&gt; &lt;SAUNIT_NO_TEST_CLASS&gt;"><link rel=""/></detail></details><stack/></alert></alerts>
  <program adtcore:uri="/sap/bc/adt/oo/classes/zcl_test" adtcore:name="ZCL_TEST" xmlns:adtcore="http://www.sap.com/adt/core">
    <alerts><alert kind="warning" severity="tolerable"><title>Program-level alert</title></alert></alerts>
    <testClasses><testClass adtcore:name="LTC_TEST">
      <alerts><alert kind="exception" severity="critical"><title>Class-level alert</title></alert></alerts>
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
		{Kind: "exception", Severity: "critical", Title: "Class-level alert"},
	}
	if !slices.Equal(result.Alerts, want) {
		t.Errorf("Alerts = %+v, want %+v", result.Alerts, want)
	}
}
