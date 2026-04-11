package adt

import (
	"context"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// TestClient extends Client with test-only hooks for unit tests in the
// external adt_test package. It exposes unexported helpers on *httpClient
// so tests can exercise discovery-driven content negotiation without
// adding them to the public API.
type TestClient interface {
	Client
	SourceContentTypeForTest(endpoint string) string
	LoadDiscoveryForTest(ctx context.Context) error
}

// NewClientForTest creates a TestClient from a SAPSystem config. It returns
// the concrete *httpClient (cast to TestClient) so tests can call internal
// helpers without those helpers being exported in production builds.
func NewClientForTest(cfg sapmcpconfig.SAPSystem) TestClient {
	return NewClient(cfg).(*httpClient)
}

// SourceContentTypeForTest exposes the unexported sourceContentType helper
// so tests in the external adt_test package can verify discovery-driven
// source content negotiation.
func (c *httpClient) SourceContentTypeForTest(endpoint string) string {
	return c.sourceContentType(endpoint)
}

// LoadDiscoveryForTest triggers a CSRF+discovery preflight so tests can
// populate c.discovery without issuing a business request first.
func (c *httpClient) LoadDiscoveryForTest(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetchCSRFToken(ctx)
}
