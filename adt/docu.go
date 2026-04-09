package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// GetABAPDoc retrieves ABAP keyword documentation from the SAP system.
//
// The keyword is looked up via the SAP public documentation servlet at
// /sap/public/bc/abap/docu using the query= parameter (e.g. query=DATA,
// query=SELECT). This is the path SAP's own ABAP help search form uses
// internally — the R/3 homepage's JavaScript constructs it as
// /sap/public/bc/abap/docu?format=ECLIPSE&query=...
//
// On systems where the public servlet is not active (e.g. S/4 with the
// ICF node /sap/public/bc/abap/docu disabled), the call returns a clear
// error rather than silently returning the homepage.
//
// The response is HTML; this function strips tags and returns plain text.
//
// See adtler#18 / mcp-server-abap#297.
func (c *httpClient) GetABAPDoc(ctx context.Context, keyword string) (string, error) {
	params := url.Values{}
	params.Set("format", "eclipse")
	if c.cfg.Client != "" {
		params.Set("sap-client", c.cfg.Client)
	}
	if keyword != "" {
		params.Set("query", strings.ToUpper(keyword))
	}
	path := "/sap/public/bc/abap/docu?" + params.Encode()

	resp, err := c.doRead(ctx, path, map[string]string{
		"Accept": "text/html",
	})
	if err != nil {
		return "", fmt.Errorf("GetABAPDoc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden {
		return "", fmt.Errorf("GetABAPDoc: ABAP keyword documentation is not available on this system — " +
			"the ICF service /sap/public/bc/abap/docu may need to be activated via transaction SICF")
	}
	if err := checkResponse(resp); err != nil {
		return "", err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("GetABAPDoc reading body: %w", err)
	}

	// Strip HTML tags for clean text output.
	text := htmlTagRe.ReplaceAllString(string(data), " ")
	text = strings.Join(strings.Fields(text), " ")
	return strings.TrimSpace(text), nil
}
