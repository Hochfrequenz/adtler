package adt

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// PrettyPrint sends no Accept header: S/4 (SAP_BASIS 816) formats the
// source without one, measured while auditing #154. The call has no side
// effect, so the header check SAP runs last cannot hide a change here.
func (c *httpClient) PrettyPrint(ctx context.Context, source string) (string, error) {
	resp, err := c.doMutate(ctx, "POST",
		"/sap/bc/adt/abapsource/prettyprinter",
		strings.NewReader(source),
		map[string]string{"Content-Type": "text/plain; charset=utf-8"},
	)
	if err != nil {
		return "", fmt.Errorf("PrettyPrint: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return "", err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("PrettyPrint reading body: %w", err)
	}
	return string(data), nil
}
