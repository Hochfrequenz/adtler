package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (c *httpClient) NavigateToDefinition(ctx context.Context, sourceURI, source string) (string, error) {
	// SAP handler CL_SEDI_ADT_RES_NAVIGATION->post reads the source text
	// from the request body via get_handler_for_plain_text and combines it
	// with the cursor position from the URI fragment to resolve the target.
	// Without a body the navigation cannot run and the response stays empty,
	// which earlier looked to callers like an "echo" of the input URI.
	path := "/sap/bc/adt/navigation/target?uri=" + url.QueryEscape(sourceURI)
	resp, err := c.doMutate(ctx, http.MethodPost, path, strings.NewReader(source),
		map[string]string{
			"Content-Type": "text/plain; charset=utf-8",
			"Accept":       "application/xml",
		})
	if err != nil {
		return "", fmt.Errorf("NavigateToDefinition: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("NavigateToDefinition reading body: %w", err)
	}

	// SAP returns an objectReference with adtcore:uri attribute:
	// <objectReference xmlns:adtcore="http://www.sap.com/adt/core"
	//   adtcore:uri="/sap/bc/adt/oo/classes/zcl_target" adtcore:name="ZCL_TARGET"/>
	var ref struct {
		URI string `xml:"http://www.sap.com/adt/core uri,attr"`
	}
	if err := xml.Unmarshal(data, &ref); err != nil {
		return "", fmt.Errorf("NavigateToDefinition parsing: %w", err)
	}
	if ref.URI == "" {
		// Fallback: try without namespace (older systems may not use namespaced attributes)
		var plain struct {
			URI string `xml:"uri,attr"`
		}
		_ = xml.Unmarshal(data, &plain)
		ref.URI = plain.URI
	}
	return ref.URI, nil
}
