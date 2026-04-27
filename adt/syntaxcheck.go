package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func (c *httpClient) SyntaxCheck(ctx context.Context, objectURI string) ([]SyntaxMessage, error) {
	results := c.batchSyntaxCheckChunkWithVersion(ctx, []string{objectURI}, "inactive")
	if results[0].Error != "" {
		return nil, fmt.Errorf("SyntaxCheck: %s", results[0].Error)
	}
	msgs := results[0].Messages

	// Detect the false-positive pattern: when there's no inactive version
	// (object never saved since last activation, or object doesn't exist at
	// all), SAP checks empty source and returns "REPORT/PROGRAM statement
	// missing" at line 1 col 0 as the sole error — indistinguishable from
	// a real syntax error in the message shape.
	//
	// On detection:
	//   1. Verify the object exists via GetObjectInfo. If not → return a
	//      clear "object not found" error instead of the misleading parser
	//      message.
	//   2. If the object exists, fall back to checking the ACTIVE version.
	//      An active program always has source (at minimum the stub SAP
	//      generates on creation), so the false-positive can't recur.
	//
	// See adtler#11 / mcp-server-abap#290.
	if isFalsePositiveReportMissing(msgs) {
		if _, err := c.GetObjectInfo(ctx, objectURI); err != nil {
			return nil, fmt.Errorf("SyntaxCheck: object does not exist: %w", err)
		}
		results = c.batchSyntaxCheckChunkWithVersion(ctx, []string{objectURI}, "active")
		if results[0].Error != "" {
			return nil, fmt.Errorf("SyntaxCheck: %s", results[0].Error)
		}
		return results[0].Messages, nil
	}
	return msgs, nil
}

// isFalsePositiveReportMissing detects the "REPORT/PROGRAM statement is
// missing" error that SAP returns when checking an empty inactive payload.
// The pattern is: exactly one error, at line 1 col 0, with text mentioning
// REPORT or PROGRAM and "missing" or "fehlt" (German).
func isFalsePositiveReportMissing(msgs []SyntaxMessage) bool {
	if len(msgs) != 1 {
		return false
	}
	m := msgs[0]
	if m.Type != "E" || m.Line != 1 || m.Column != 0 {
		return false
	}
	hasKeyword := strings.Contains(m.Text, "REPORT") || strings.Contains(m.Text, "PROGRAM")
	hasMissing := strings.Contains(m.Text, "missing") || strings.Contains(m.Text, "fehlt")
	return hasKeyword && hasMissing
}

// ObjectSyntaxResult holds the syntax check result for a single object.
type ObjectSyntaxResult struct {
	ObjectURI string          `json:"object_uri"`
	Messages  []SyntaxMessage `json:"messages"`
	Error     string          `json:"error,omitempty"`
}

// batchCheckChunkSize is the maximum number of objects per checkruns request.
// SAP endpoints have undocumented request size limits; 10 is a safe default.
const batchCheckChunkSize = 10

// BatchSyntaxCheck runs syntax checks on multiple objects using the native
// batch capability of /sap/bc/adt/checkruns. Objects are sent in chunks
// of batchCheckChunkSize to stay within SAP request size limits.
// Results are correlated back to objects via the report's triggeringUri.
func (c *httpClient) BatchSyntaxCheck(ctx context.Context, objectURIs []string) []ObjectSyntaxResult {
	results := make([]ObjectSyntaxResult, len(objectURIs))
	for start := 0; start < len(objectURIs); start += batchCheckChunkSize {
		end := start + batchCheckChunkSize
		if end > len(objectURIs) {
			end = len(objectURIs)
		}
		chunk := objectURIs[start:end]
		chunkResults := c.batchSyntaxCheckChunk(ctx, chunk)
		copy(results[start:], chunkResults)
	}
	return results
}

// batchSyntaxCheckChunk runs a single batched syntax check request for a chunk of objects.
func (c *httpClient) batchSyntaxCheckChunk(ctx context.Context, objectURIs []string) []ObjectSyntaxResult {
	return c.batchSyntaxCheckChunkWithVersion(ctx, objectURIs, "inactive")
}

// batchSyntaxCheckChunkWithVersion is the version-parameterised core of
// batchSyntaxCheckChunk. SyntaxCheck uses it to retry with version="active"
// when the inactive check produces a false-positive.
func (c *httpClient) batchSyntaxCheckChunkWithVersion(ctx context.Context, objectURIs []string, version string) []ObjectSyntaxResult {
	// Build XML with all objects in a single checkObjectList.
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString(`<chkrun:checkObjectList xmlns:chkrun="http://www.sap.com/adt/checkrun" `)
	sb.WriteString(`xmlns:adtcore="http://www.sap.com/adt/core">`)
	for _, uri := range objectURIs {
		sb.WriteString(`<chkrun:checkObject adtcore:uri="` + html.EscapeString(uri) + `" chkrun:version="` + version + `"/>`)
	}
	sb.WriteString(`</chkrun:checkObjectList>`)

	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/checkruns",
		strings.NewReader(sb.String()),
		map[string]string{
			"Content-Type": "application/vnd.sap.adt.checkobjects+xml",
			"Accept":       "application/vnd.sap.adt.checkmessages+xml",
		},
	)
	if err != nil {
		// On HTTP-level failure, return the error for all objects.
		results := make([]ObjectSyntaxResult, len(objectURIs))
		for i, uri := range objectURIs {
			results[i] = ObjectSyntaxResult{ObjectURI: uri, Error: fmt.Sprintf("BatchSyntaxCheck: %s", err)}
		}
		return results
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		results := make([]ObjectSyntaxResult, len(objectURIs))
		for i, uri := range objectURIs {
			results[i] = ObjectSyntaxResult{ObjectURI: uri, Error: err.Error()}
		}
		return results
	}

	data, _ := io.ReadAll(resp.Body)
	var reports adtxml.CheckRunReports
	xml.Unmarshal(data, &reports) //nolint:errcheck

	// Index reports by triggeringUri for correlation.
	reportsByURI := make(map[string]*adtxml.CheckRunReport, len(reports.Reports))
	for i := range reports.Reports {
		reportsByURI[reports.Reports[i].TriggerURI] = &reports.Reports[i]
	}

	results := make([]ObjectSyntaxResult, len(objectURIs))
	for i, uri := range objectURIs {
		results[i] = ObjectSyntaxResult{ObjectURI: uri}
		report, ok := reportsByURI[uri]
		if !ok {
			continue // no report = no messages = clean
		}
		for _, m := range report.Messages {
			line, col := parseMessagePosition(m.URI)
			results[i].Messages = append(results[i].Messages, SyntaxMessage{
				Type:   m.Type,
				Text:   m.ShortText,
				Line:   line,
				Column: col,
			})
		}
	}
	return results
}

// parseMessagePosition extracts line and column from a checkMessage URI fragment.
// Format: ".../source/main#start=42,5" → line=42, col=5
func parseMessagePosition(uri string) (int, int) {
	idx := strings.Index(uri, "#start=")
	if idx < 0 {
		return 0, 0
	}
	parts := strings.SplitN(uri[idx+7:], ",", 2)
	line, _ := strconv.Atoi(parts[0])
	col := 0
	if len(parts) == 2 {
		col, _ = strconv.Atoi(parts[1])
	}
	return line, col
}
