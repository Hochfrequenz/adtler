package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// NavigateToDefinition resolves the ABAP symbol at the given source position
// to its definition URI. The sourceURI must include a line/column fragment
// (e.g. .../source/main#start=15,5) pointing at the symbol to navigate from.
//
// SAP's /sap/bc/adt/navigation/target endpoint returns an XML response where:
//   - The root element's uri attribute is the CANONICAL form of the input
//     position (case-normalized, same object). This is what the old parser
//     returned — always an echo of the input.
//   - The actual navigation target — when a cross-reference IS resolved — is
//     in a child <objectReference> element's uri attribute.
//
// When no cross-reference exists at the given position (e.g. the cursor is on
// whitespace, a keyword, or a non-navigable token), the endpoint returns the
// input position without a child objectReference. In that case this function
// returns the root URI (the canonical echo).
//
// See adtler#8 / mcp-server-abap#286.
func (c *httpClient) NavigateToDefinition(ctx context.Context, sourceURI string) (string, error) {
	path := "/sap/bc/adt/navigation/target?uri=" + url.QueryEscape(sourceURI)
	resp, err := c.doMutate(ctx, http.MethodPost, path, nil,
		map[string]string{"Accept": "application/xml"})
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

	// Parse the response looking for both the root URI (echo) and a child
	// objectReference URI (the actual navigation target). The child element
	// may use adtcore: namespace prefix — Go's xml.Unmarshal matches by
	// local name when no namespace is specified in the struct tag.
	var ref struct {
		URI       string `xml:"uri,attr"`
		TargetRef struct {
			URI  string `xml:"uri,attr"`
			Type string `xml:"type,attr"`
			Name string `xml:"name,attr"`
		} `xml:"objectReference"`
		// Some SAP releases wrap the target in a <link> element instead.
		Links []struct {
			Href string `xml:"href,attr"`
			Rel  string `xml:"rel,attr"`
		} `xml:"link"`
	}
	if err := xml.Unmarshal(data, &ref); err != nil {
		return "", fmt.Errorf("NavigateToDefinition parsing: %w", err)
	}

	// Prefer the objectReference child (the actual navigation target).
	if ref.TargetRef.URI != "" {
		return ref.TargetRef.URI, nil
	}
	// Fall back to a <link> with a navigation-related rel.
	for _, l := range ref.Links {
		if l.Href != "" && l.Href != ref.URI {
			return l.Href, nil
		}
	}
	// No target found — return the root URI (canonical echo of the input).
	return ref.URI, nil
}
