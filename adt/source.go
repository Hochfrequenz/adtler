package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
)

// FetchETag reads the ETag for any object URI by GETting the bare URI with
// the type-appropriate Accept header (via acceptHeaderForURI / discovery).
// This is the same pattern DeleteObject uses for optimistic locking.
//
// Unlike GetSource (which hardcodes /source/main and text/plain), this works
// for ALL object types including CLAS, DTEL, DOMA, and TABL — because it
// delegates content negotiation to acceptHeaderForURI which already knows
// the correct vendor MIME type per URI prefix.
//
// Used as a fallback by LockMap.ResolveETag when GetSource fails (e.g. 400
// for CLAS on S/4, 404 for DTEL/DOMA). See adtler#9, adtler#14.
func (c *httpClient) FetchETag(ctx context.Context, objectURI string) (string, error) {
	accept := c.acceptHeaderForURI(objectURI)
	resp, err := c.doRead(ctx, objectURI, map[string]string{"Accept": accept})
	if err != nil {
		return "", fmt.Errorf("FetchETag: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", fmt.Errorf("FetchETag: %w", err)
	}
	_, _ = io.ReadAll(resp.Body) // drain body to allow connection reuse
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("FetchETag: no ETag returned for %s", objectURI)
	}
	return etag, nil
}

func (c *httpClient) GetSource(ctx context.Context, objectURI string) (*SourceResult, error) {
	resp, err := c.doRead(ctx, objectURI+"/source/main", map[string]string{"Accept": "text/plain"})
	if err != nil {
		return nil, fmt.Errorf("GetSource: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetSource reading body: %w", err)
	}
	return &SourceResult{Source: string(body), ETag: resp.Header.Get("ETag")}, nil
}

// objectStructure is the XML response from the objectstructure endpoint.
type objectStructure struct {
	XMLName xml.Name `xml:"objectStructureElement"`
	Links   []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
}

// parseDefinitionEndLine extracts the end line of the definitionBlock from objectstructure XML.
// The href format is "./source/main#start=1,0;end=26,8" — we need the line from "end=26,...".
func parseDefinitionEndLine(data []byte) (int, error) {
	var obj objectStructure
	if err := xml.Unmarshal(data, &obj); err != nil {
		return 0, fmt.Errorf("parsing objectstructure: %w", err)
	}
	for _, link := range obj.Links {
		if link.Rel != "http://www.sap.com/adt/relations/source/definitionBlock" {
			continue
		}
		// href: "./source/main#start=1,0;end=26,8"
		idx := strings.Index(link.Href, "end=")
		if idx < 0 {
			return 0, fmt.Errorf("no end= in definitionBlock href: %s", link.Href)
		}
		endPart := link.Href[idx+4:] // "26,8"
		comma := strings.Index(endPart, ",")
		if comma < 0 {
			return 0, fmt.Errorf("no comma in end position: %s", endPart)
		}
		line, err := strconv.Atoi(endPart[:comma])
		if err != nil {
			return 0, fmt.Errorf("parsing end line %q: %w", endPart[:comma], err)
		}
		return line, nil
	}
	return 0, fmt.Errorf("definitionBlock link not found in objectstructure")
}

// GetClassDefinition returns only the definition part of a class source.
// It fetches objectstructure (for the definition line range) and source/main concurrently,
// then truncates the source to the definition block.
func (c *httpClient) GetClassDefinition(ctx context.Context, objectURI string) (*SourceResult, error) {
	type structResult struct {
		data []byte
		err  error
	}
	type sourceResult struct {
		result *SourceResult
		err    error
	}

	var wg sync.WaitGroup
	wg.Add(2)

	var sr sourceResult
	var str structResult

	go func() {
		defer wg.Done()
		sr.result, sr.err = c.GetSource(ctx, objectURI)
	}()
	go func() {
		defer wg.Done()
		resp, err := c.doRead(ctx, objectURI+"/objectstructure", map[string]string{"Accept": "application/xml"})
		if err != nil {
			str.err = fmt.Errorf("GetClassDefinition objectstructure: %w", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		if err := checkResponse(resp); err != nil {
			str.err = err
			return
		}
		str.data, str.err = io.ReadAll(resp.Body)
	}()

	wg.Wait()

	if sr.err != nil {
		return nil, sr.err
	}
	if str.err != nil {
		return nil, str.err
	}

	endLine, err := parseDefinitionEndLine(str.data)
	if err != nil {
		return nil, err
	}

	// Truncate source to definition block (lines are 1-based).
	lines := strings.Split(sr.result.Source, "\n")
	if endLine > len(lines) {
		endLine = len(lines)
	}
	definition := strings.Join(lines[:endLine], "\n")

	return &SourceResult{Source: definition, ETag: sr.result.ETag}, nil
}

// validIncludes lists the valid class include types.
var validIncludes = map[string]bool{
	"testclasses": true, "definitions": true, "implementations": true, "macros": true,
}

// classIncludePath returns the OO include path for a class include.
// Path format: /sap/bc/adt/oo/classes/{CLASS}/includes/{type}
// Note: NO /source/main suffix — unlike the main class source.
func classIncludePath(objectURI, include string) (string, error) {
	if !validIncludes[include] {
		return "", fmt.Errorf("unknown include type %q (valid: testclasses, definitions, implementations, macros)", include)
	}
	return objectURI + "/includes/" + include, nil
}

func (c *httpClient) GetIncludeSource(ctx context.Context, objectURI, include string) (*SourceResult, error) {
	path, err := classIncludePath(objectURI, include)
	if err != nil {
		return nil, err
	}
	resp, err := c.doRead(ctx, path, map[string]string{"Accept": "text/plain"})
	if err != nil {
		return nil, fmt.Errorf("GetIncludeSource: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetIncludeSource reading body: %w", err)
	}
	return &SourceResult{Source: string(body), ETag: resp.Header.Get("ETag")}, nil
}

func (c *httpClient) SetIncludeSource(ctx context.Context, objectURI, include, source, lockHandle, transport, etag string) (string, error) {
	path, err := classIncludePath(objectURI, include)
	if err != nil {
		return "", err
	}
	headers := map[string]string{
		"Content-Type":          "text/plain; charset=utf-8",
		"Accept":                "text/plain",
		"X-sap-adt-sessiontype": "stateful",
	}
	if etag != "" {
		headers["If-Match"] = etag
	}
	// Include endpoints expect lockHandle as query parameter, not header.
	params := url.Values{}
	if lockHandle != "" {
		params.Set("lockHandle", lockHandle)
	}
	if transport != "" {
		params.Set("corrNr", transport)
	}
	writePath := path
	if len(params) > 0 {
		writePath += "?" + params.Encode()
	}
	resp, err := c.doMutate(ctx, http.MethodPut, writePath,
		strings.NewReader(source),
		headers,
	)
	if err != nil {
		return "", fmt.Errorf("SetIncludeSource: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", err
	}
	return resp.Header.Get("ETag"), nil
}

// CreateTestInclude creates the test classes include for a class that doesn't have one yet.
// Requires a stateful lock on the parent class (LockObject uses stateful sessions).
func (c *httpClient) CreateTestInclude(ctx context.Context, objectURI, lockHandle, transport string) error {
	body := `<?xml version="1.0" encoding="UTF-8"?>
<class:abapClassInclude xmlns:class="http://www.sap.com/adt/oo/classes"
  xmlns:adtcore="http://www.sap.com/adt/core"
  adtcore:name="dummy" class:includeType="testclasses"/>`

	params := url.Values{}
	params.Set("lockHandle", lockHandle)
	if transport != "" {
		params.Set("corrNr", transport)
	}

	path := objectURI + "/includes?" + params.Encode()
	resp, err := c.doMutate(ctx, http.MethodPost, path,
		strings.NewReader(body),
		map[string]string{
			"Content-Type":          "application/*",
			"X-sap-adt-sessiontype": "stateful",
		},
	)
	if err != nil {
		return fmt.Errorf("CreateTestInclude: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}

func (c *httpClient) SetSource(ctx context.Context, objectURI, source, lockHandle, transport, etag string) (string, error) {
	// R/3 and S/4 use DIFFERENT lock handle delivery mechanisms:
	//   - R/3 reads the X-SAP-Lock-Handle HTTP header
	//   - S/4 reads the ?lockHandle= query parameter
	// Sending both simultaneously doesn't work: R/3 treats the query
	// parameter as authoritative and rejects it.
	//
	// Strategy: try header delivery first (R/3 path). If the server
	// returns 423 ExceptionResourceInvalidLockHandle, retry with query
	// parameter delivery (S/4 path). R/3 succeeds on the first try;
	// S/4 succeeds on the retry. One extra round-trip on S/4 only.
	//
	// See adtler#4 integration test history:
	//   v2 (header only): R/3 ✅, S/4 ❌ 423
	//   v3 (query only):  R/3 ❌ 423, S/4 ✅
	//   v4 (both):        R/3 ❌ (query poisons), S/4 ✅
	//   v5 (header-first + retry): R/3 ✅, S/4 ✅  ← this version
	newETag, err := c.setSourceWithLockHeader(ctx, objectURI, source, lockHandle, transport, etag)
	if err != nil && lockHandle != "" && isInvalidLockHandle(err) {
		return c.setSourceWithLockParam(ctx, objectURI, source, lockHandle, transport, etag)
	}
	return newETag, err
}

func (c *httpClient) setSourceWithLockHeader(ctx context.Context, objectURI, source, lockHandle, transport, etag string) (string, error) {
	headers := map[string]string{
		"Content-Type":          "text/plain; charset=utf-8",
		"Accept":                "text/plain",
		"If-Match":              etag,
		"X-sap-adt-sessiontype": "stateful",
	}
	if lockHandle != "" {
		headers["X-SAP-Lock-Handle"] = lockHandle
	}
	path := objectURI + "/source/main"
	if transport != "" {
		path += "?corrNr=" + url.QueryEscape(transport)
	}
	return c.doSetSource(ctx, path, source, headers)
}

func (c *httpClient) setSourceWithLockParam(ctx context.Context, objectURI, source, lockHandle, transport, etag string) (string, error) {
	headers := map[string]string{
		"Content-Type":          "text/plain; charset=utf-8",
		"Accept":                "text/plain",
		"If-Match":              etag,
		"X-sap-adt-sessiontype": "stateful",
	}
	params := url.Values{}
	if lockHandle != "" {
		params.Set("lockHandle", lockHandle)
	}
	if transport != "" {
		params.Set("corrNr", transport)
	}
	path := objectURI + "/source/main"
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	return c.doSetSource(ctx, path, source, headers)
}

func (c *httpClient) doSetSource(ctx context.Context, path, source string, headers map[string]string) (string, error) {
	resp, err := c.doMutate(ctx, http.MethodPut, path,
		strings.NewReader(source),
		headers,
	)
	if err != nil {
		return "", fmt.Errorf("SetSource: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", err
	}
	return resp.Header.Get("ETag"), nil
}
