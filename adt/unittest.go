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
