package adt

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

// GetABAPDoc retrieves ABAP keyword documentation from the SAP system.
//
// The keyword is looked up via the SAP public documentation servlet at
// /sap/public/bc/abap/docu using the ABEN<keyword> convention (e.g.,
// keyword "DATA" → object "ABENDATA", keyword "SELECT" → "ABENSELECT").
// The response is HTML; this function strips tags and returns plain text.
//
// Before adtler#18 (mcp-server-abap#297) this used the ADT endpoint
// /sap/bc/adt/docu/abap/langu with keyword=… — which returned the
// documentation homepage regardless of the keyword on both R/3 and S/4.
// The public servlet is the path SAP's own ABAP help UI uses internally.
func (c *httpClient) GetABAPDoc(ctx context.Context, keyword string) (string, error) {
	params := url.Values{}
	params.Set("format", "eclipse")
	if c.cfg.Client != "" {
		params.Set("sap-client", c.cfg.Client)
	}
	if keyword != "" {
		params.Set("object", "ABEN"+strings.ToUpper(keyword))
	}
	path := "/sap/public/bc/abap/docu?" + params.Encode()

	resp, err := c.doRead(ctx, path, map[string]string{
		"Accept": "text/html",
	})
	if err != nil {
		return "", fmt.Errorf("GetABAPDoc: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
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
