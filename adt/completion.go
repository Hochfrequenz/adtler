package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func (c *httpClient) GetCompletions(ctx context.Context, objectURI, source string, line, column int) ([]CompletionItem, error) {
	// SAP handler CL_CC_ADT_RES_BASE->determine_input_data calls
	// map_objref_to_include with set_use_source_based_position=true, which
	// extracts the (program, include, line, line_offset) tuple from the URI
	// fragment. The line/column must therefore be encoded in the URI itself
	// as `...#start=L,C`, not passed as separate top-level query params.
	sourceURI := objectURI + "/source/main#start=" + strconv.Itoa(line) + "," + strconv.Itoa(column)
	params := url.Values{}
	params.Set("uri", sourceURI)
	path := "/sap/bc/adt/abapsource/codecompletion/proposal?" + params.Encode()

	resp, err := c.doMutate(ctx, "POST", path,
		strings.NewReader(source),
		map[string]string{
			"Content-Type": "text/plain; charset=utf-8",
			"Accept":       "application/vnd.sap.as+xml",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("GetCompletions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetCompletions reading body: %w", err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var comps adtxml.Completions
	if err := xml.Unmarshal(data, &comps); err != nil {
		return nil, fmt.Errorf("GetCompletions parsing: %w", err)
	}
	result := make([]CompletionItem, len(comps.Items))
	for i, c := range comps.Items {
		result[i] = CompletionItem{Text: c.Identifier}
	}
	return result, nil
}
