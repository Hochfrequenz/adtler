package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

// RunUnitTests runs the ABAP Unit tests of objectURI and waits up to
// timeoutSeconds (+5 s of slack) for the result.
//
// The POST goes through the LONG-TIMEOUT HTTP client (doMutateLong), so the
// effective limit is the context deadline set here, not the short client's
// 30-second cap (issue #186). A test run can legitimately outlast 30 s — a slow
// test class, or a test method suspended at an external breakpoint while a
// debugger is attached — and http.Client.Timeout would otherwise cut it off
// regardless of timeoutSeconds.
//
// SAP answers a run that executes no test method with HTTP 200 on both
// SAP_BASIS 750 and 816, and the reason is only partly visible in the
// response. A test class skipped for its risk level carries a test-class
// alert of kind "warning" on both releases, and on 750 a test class aborted
// by a runtime error carries one of kind "runtimeAbortion"; both reach
// TestResult.Alerts. An object without active test classes gets a run-level
// alert of kind "noTestClasses" on 750 and no alert on 816, and "without
// active test classes" includes an object whose test-classes include is
// still inactive, because the run uses the active version. For any run
// without an executed test method, TestResult.InactiveURIs therefore lists
// the inactive entries related to objectURI by URI nesting, which tells the
// inactive-include case apart for classes; see InactiveURIs for its limits.
// A URI naming no existing object yields neither an alert nor an error and
// is not detected. A response body that is not an ABAP Unit run result,
// including an empty one, is returned as an error.
func (c *httpClient) RunUnitTests(ctx context.Context, objectURI string, timeoutSeconds int) (*TestResult, error) {
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSeconds+5)*time.Second)
	defer cancel()

	reqBody := adtxml.RunConfiguration{
		NS: "http://www.sap.com/adt/aunit",
		External: adtxml.External{
			Coverage: adtxml.Coverage{Active: "false"},
		},
		Options: adtxml.RunOptions{
			URIType:                   adtxml.Value{Value: "semantic"},
			TestDeterminationStrategy: adtxml.TestDetermination{SameProgram: "true", AssignedTests: "false", PublicMethods: "false"},
			TestRiskLevels:            adtxml.RiskLevels{Harmless: "true", Dangerous: "true", Critical: "true"},
			TestDurations:             adtxml.Durations{Short: "true", Medium: "true", Long: "true"},
		},
		Objects: adtxml.ObjectSets{
			NS: nsADTCore,
			Set: adtxml.ObjectSet{
				Kind: "inclusive",
				References: adtxml.AUnitObjectRefs{
					Refs: []adtxml.ObjectRef{{URI: objectURI}},
				},
			},
		},
	}

	body, err := xml.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal unit test request: %w", err)
	}

	resp, err := c.doMutateLong(reqCtx, http.MethodPost,
		"/sap/bc/adt/abapunit/testruns",
		strings.NewReader(xml.Header+string(body)),
		map[string]string{
			"Content-Type": contentTypeXML,
			"Accept":       contentTypeXML,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("RunUnitTests: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RunUnitTests reading body: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return nil, fmt.Errorf("RunUnitTests: empty response body")
	}
	var runResult adtxml.RunResult
	if err := xml.Unmarshal(data, &runResult); err != nil {
		return nil, fmt.Errorf("RunUnitTests parsing: %w", err)
	}

	result := &TestResult{Alerts: toTestAlerts(runResult.Alerts)}
	for _, prog := range runResult.Programs {
		result.Alerts = append(result.Alerts, toTestAlerts(prog.Alerts)...)
		for _, class := range prog.Classes {
			result.Alerts = append(result.Alerts, toTestAlerts(class.Alerts)...)
			for _, method := range class.Methods {
				tc := TestCase{
					Name:          method.Name,
					ExecutionTime: method.ExecutionTime,
					Passed:        len(method.Alerts) == 0,
				}
				for _, alert := range method.Alerts {
					tc.Messages = append(tc.Messages, alert.Title)
				}
				result.TestCases = append(result.TestCases, tc)
				if tc.Passed {
					result.Passed++
				} else {
					result.Failed++
				}
			}
			result.Errors += class.ErrorCount
		}
	}
	if len(result.TestCases) == 0 {
		// reqCtx, not ctx: the lookup stays within the documented
		// timeoutSeconds + 5 s budget. If that budget is spent, the lookup
		// fails and InactiveURIs stays empty; the run result is kept.
		result.InactiveURIs = c.relatedInactiveURIs(reqCtx, objectURI)
	}
	return result, nil
}

// toTestAlerts converts parsed ABAP Unit alerts, returning nil for none.
func toTestAlerts(alerts []adtxml.Alert) []TestAlert {
	var out []TestAlert
	for _, a := range alerts {
		out = append(out, TestAlert{Kind: a.Kind, Severity: a.Severity, Title: a.Title})
	}
	return out
}

// relatedInactiveURIs returns the GetInactiveObjects entries that objectURI
// relates to per objectURIMatches: the object itself, a part nested under it,
// or an object it is nested under. A failed read returns nil, because the
// check only annotates a test result and must not turn it into an error.
func (c *httpClient) relatedInactiveURIs(ctx context.Context, objectURI string) []string {
	// An empty URI would match every inactive object (objectURIMatches treats "" as a prefix of everything).
	if normalizeObjectURI(objectURI) == "" {
		return nil
	}
	inactive, err := c.GetInactiveObjects(ctx)
	if err != nil {
		return nil
	}
	var uris []string
	for _, obj := range inactive {
		if objectURIMatches(objectURI, obj.URI) {
			uris = append(uris, obj.URI)
		}
	}
	return uris
}
