package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

// GetHTTPService reads an HTTP service's description, handler class and the
// URL it answers at. CreateHTTPService is in object.go beside CreateObject.
func (c *httpClient) GetHTTPService(ctx context.Context, name string) (*HTTPService, error) {
	uri, err := ObjectURI(objTypeHTTP, name)
	if err != nil {
		return nil, err
	}
	resp, err := c.readWithAcceptFallback(ctx, uri, c.acceptHeaderForURI(uri))
	if err != nil {
		return nil, fmt.Errorf("GetHTTPService: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetHTTPService: %w", err)
	}
	var doc adtxml.HTTPService
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("GetHTTPService parsing: %w", err)
	}
	// adtxml.HTTPService names no root element, so any XML document (or an
	// HTML page) parses without error into empty fields.
	if doc.Name == "" {
		return nil, fmt.Errorf("GetHTTPService parsing: response carried no adtcore:name — it is not an HTTP service document")
	}
	return &HTTPService{Name: doc.Name, Description: doc.Description, HandlerClass: doc.HandlerClass, URL: doc.CanonicalURL}, nil
}
