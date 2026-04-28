package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

// defaultATCCheckVariant is used when the caller passes an empty variant.
// SAP R/3's standard ATC check variant is "DEFAULT" (also reported by
// GetATCCustomizing on R/3). S/4 callers should pass their site-specific
// variant explicitly (often "ZCB_CLEAN_ABAP_1") — DEFAULT may produce
// different findings on S/4 than the system default.
const defaultATCCheckVariant = "DEFAULT"

func (c *httpClient) GetATCCustomizing(ctx context.Context) (*ATCCustomizingResult, error) {
	resp, err := c.doRead(ctx, "/sap/bc/adt/atc/customizing", map[string]string{
		"Accept": "application/vnd.sap.atc.customizing-v1+xml",
	})
	if err != nil {
		return nil, fmt.Errorf("GetATCCustomizing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetATCCustomizing reading body: %w", err)
	}

	var cust adtxml.ATCCustomizing
	if err := xml.Unmarshal(data, &cust); err != nil {
		return nil, fmt.Errorf("GetATCCustomizing parsing: %w", err)
	}

	result := &ATCCustomizingResult{
		Properties: make(map[string]string, len(cust.Properties)),
	}
	for _, p := range cust.Properties {
		result.Properties[p.Name] = p.Value
		if p.Name == "systemCheckVariant" {
			result.SystemCheckVariant = p.Value
		}
	}
	return result, nil
}

// RunATCCheck runs an ATC check against the given object URIs. It implements
// the canonical 3-step Eclipse ADT flow:
//
//  1. POST /sap/bc/adt/atc/worklists?checkVariant={variant} — creates a
//     worklist seeded with the requested check variant. The response body
//     is the worklist ID as plain text.
//  2. POST /sap/bc/adt/atc/runs?worklistId={id} — triggers the actual run.
//     The body carries the object references but no longer carries
//     checkVariant (it's already established on the worklist).
//  3. GET /sap/bc/adt/atc/worklists/{id} — fetches findings.
//
// The earlier 2-step shortcut (POST /runs with worklistId="0000000000",
// checkVariant in body) returned HTTP 500 with empty body on R/3 — see
// adtler#12. The 3-step shape was observed in the abap-adt-api reference
// implementation (src/api/atc.ts) and verified against the SAP ADT REST
// API on both R/3 and S/4.
//
// If checkVariant is empty, "DEFAULT" is used. Callers on S/4 should pass
// their site-specific variant explicitly.
func (c *httpClient) RunATCCheck(ctx context.Context, objectURIs []string, checkVariant string) (*ATCResult, error) {
	if checkVariant == "" {
		checkVariant = defaultATCCheckVariant
	}

	if err := c.ensureCSRF(ctx); err != nil {
		return nil, fmt.Errorf("RunATCCheck: %w", err)
	}

	// Step 1: Create a worklist for this variant. SAP returns the worklist
	// ID as plain text in the response body.
	worklistID, err := c.createATCWorklist(ctx, checkVariant)
	if err != nil {
		return nil, fmt.Errorf("RunATCCheck: %w", err)
	}

	// Step 2: Trigger the run, scoping it to the requested object URIs.
	worklistID, err = c.triggerATCRun(ctx, worklistID, objectURIs)
	if err != nil {
		return nil, fmt.Errorf("RunATCCheck: %w", err)
	}

	// Step 3: Fetch the resulting findings.
	return c.fetchATCWorklist(ctx, worklistID)
}

// createATCWorklist performs step 1 of the ATC flow and returns the
// SAP-assigned worklist ID. The response body is plain text.
func (c *httpClient) createATCWorklist(ctx context.Context, checkVariant string) (string, error) {
	q := url.Values{}
	q.Set("checkVariant", checkVariant)
	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/atc/worklists?"+q.Encode(),
		nil,
		map[string]string{
			"Accept": "text/plain",
		},
	)
	if err != nil {
		return "", fmt.Errorf("create worklist: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", fmt.Errorf("create worklist: %w", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("create worklist reading body: %w", err)
	}
	id := strings.TrimSpace(string(body))
	if id == "" {
		return "", fmt.Errorf("create worklist: SAP returned empty worklist ID")
	}
	return id, nil
}

// triggerATCRun performs step 2: posts the object references to the runs
// endpoint, scoped to worklistID. SAP echoes back the worklist ID (and a
// timestamp) in a <worklistRun> envelope; if the response carries a
// non-empty ID, that ID supersedes the input (same supersession rule
// observed in abap-adt-api).
func (c *httpClient) triggerATCRun(ctx context.Context, worklistID string, objectURIs []string) (string, error) {
	body := buildATCRunBody(objectURIs)

	ct := c.NegotiateContentType("/sap/bc/adt/atc/runs",
		[]string{"application/vnd.sap.adt.atc.runs.v1+xml", "application/xml"},
		"application/xml")

	q := url.Values{}
	q.Set("worklistId", worklistID)
	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/atc/runs?"+q.Encode(),
		strings.NewReader(body),
		map[string]string{
			"Content-Type": ct,
			"Accept":       "application/xml",
		},
	)
	if err != nil {
		return "", fmt.Errorf("trigger run: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", fmt.Errorf("trigger run: %w", err)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("trigger run reading body: %w", err)
	}
	var run adtxml.ATCWorklistRun
	if err := xml.Unmarshal(respBody, &run); err == nil && run.WorklistID != "" {
		return run.WorklistID, nil
	}
	return worklistID, nil
}

// fetchATCWorklist performs step 3: GETs the findings for worklistID.
func (c *httpClient) fetchATCWorklist(ctx context.Context, worklistID string) (*ATCResult, error) {
	resp, err := c.doRead(ctx, "/sap/bc/adt/atc/worklists/"+worklistID, map[string]string{
		"Accept": "application/atc.worklist.v1+xml",
	})
	if err != nil {
		return nil, fmt.Errorf("RunATCCheck fetching worklist: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RunATCCheck reading worklist: %w", err)
	}

	var worklist adtxml.ATCWorklist
	if err := xml.Unmarshal(data, &worklist); err != nil {
		return nil, fmt.Errorf("RunATCCheck parsing worklist: %w", err)
	}

	result := &ATCResult{WorklistID: worklist.ID}
	for _, obj := range worklist.ObjectSets {
		for _, f := range obj.Findings {
			result.Findings = append(result.Findings, ATCFinding{
				ObjectURI:    obj.URI,
				Priority:     f.Priority,
				CheckID:      f.CheckID,
				CheckTitle:   f.CheckTitle,
				MessageTitle: f.MessageTitle,
				Location:     f.Location,
			})
		}
	}
	return result, nil
}

// buildATCRunBody builds the XML body posted to /sap/bc/adt/atc/runs.
// The variant is no longer carried here — it is established on the
// worklist by createATCWorklist.
func buildATCRunBody(objectURIs []string) string {
	var refs strings.Builder
	for _, uri := range objectURIs {
		var escaped strings.Builder
		_ = xml.EscapeText(&escaped, []byte(uri))
		fmt.Fprintf(&refs, `<adtcore:objectReference adtcore:uri="%s"/>`, escaped.String())
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<atc:run xmlns:atc="http://www.sap.com/adt/atc" ` +
		`xmlns:adtcore="http://www.sap.com/adt/core" maximumVerdicts="100">` +
		`<objectSets>` +
		`<objectSet kind="inclusive">` +
		`<adtcore:objectReferences>` + refs.String() + `</adtcore:objectReferences>` +
		`</objectSet>` +
		`</objectSets>` +
		`</atc:run>`
}
