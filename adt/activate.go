package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func (c *httpClient) ActivateObjects(ctx context.Context, objectURIs []string) (*ActivationResult, error) {
	result, _, err := c.activateObjects(ctx, objectURIs)
	return result, err
}

// ActivateObjectsVerified activates objects and, when the activation
// response body did not itself carry a definitive answer (empty or
// unparseable — the ECC behavior described in Hochfrequenz/aibap.mcp#500),
// re-checks GetInactiveObjects to catch a silent no-op: SAP returning 2xx
// while leaving the object inactive. If the requested object is still
// listed as inactive, Success is forced to false with a synthesized
// message, even though the raw activation response looked clean.
//
// An empty activation body is not itself proof of failure — it is also the
// common shape for a genuinely successful activation on this stack
// (Hochfrequenz/aibap.mcp#34) — so this only overrides the result when
// GetInactiveObjects confirms the object did not activate. If that
// verification read itself fails, the unverified (optimistic) result is
// returned unchanged, mirroring ReleaseTransportVerified's fallback.
func (c *httpClient) ActivateObjectsVerified(ctx context.Context, objectURIs []string) (*ActivationResult, error) {
	result, inconclusive, err := c.activateObjects(ctx, objectURIs)
	if err != nil {
		return nil, err
	}
	if !inconclusive || !result.Success {
		return result, nil
	}

	inactive, err := c.GetInactiveObjects(ctx)
	if err != nil {
		return result, nil
	}

	stillInactive := stillInactiveURIs(objectURIs, inactive)
	if len(stillInactive) == 0 {
		return result, nil
	}

	result.Success = false
	for _, uri := range stillInactive {
		result.Messages = append(result.Messages, ActivationMessage{
			ObjectURI: uri,
			Type:      "E",
			Text:      "activation reported success but the object is still listed as inactive (SAP silently did not activate it)",
		})
	}
	return result, nil
}

// stillInactiveURIs returns the subset of objectURIs that GetInactiveObjects
// still lists — matched by prefix, since an inactive entry's URI may point
// at a specific include (e.g. ".../source/main") nested under the requested
// object URI rather than the object URI itself.
func stillInactiveURIs(objectURIs []string, inactive []ObjectInfo) []string {
	var result []string
	for _, uri := range objectURIs {
		for _, obj := range inactive {
			if obj.URI == uri || (strings.HasPrefix(obj.URI, uri) && strings.HasPrefix(obj.URI[len(uri):], "/")) {
				result = append(result, uri)
				break
			}
		}
	}
	return result
}

// activateObjects performs the activation POST and reports whether the
// response body was inconclusive (empty or unparseable) — the signal
// ActivateObjectsVerified uses to decide whether a follow-up check is
// warranted.
func (c *httpClient) activateObjects(ctx context.Context, objectURIs []string) (*ActivationResult, bool, error) {
	objects := make([]adtxml.ActivationObject, len(objectURIs))
	for i, uri := range objectURIs {
		objects[i] = adtxml.ActivationObject{URI: uri}
	}
	bodyXML, err := xml.Marshal(adtxml.ActivationRequest{
		NS:      nsADTCore,
		Objects: objects,
	})
	if err != nil {
		return nil, false, fmt.Errorf("marshal activation request: %w", err)
	}

	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/activation?method=activate&preauditRequested=true",
		strings.NewReader(xml.Header+string(bodyXML)),
		map[string]string{
			"Content-Type": contentTypeXML,
			"Accept":       "application/xml",
		},
	)
	if err != nil {
		return nil, false, fmt.Errorf("ActivateObjects: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := checkResponse(resp); err != nil {
		return nil, false, err
	}

	data, _ := io.ReadAll(resp.Body)
	var msgs adtxml.ActivationMessages
	unmarshalErr := xml.Unmarshal(data, &msgs)
	inconclusive := len(data) == 0 || unmarshalErr != nil

	result := &ActivationResult{Success: true}
	for _, m := range msgs.Messages {
		msg := ActivationMessage{
			ObjectURI: m.Href,
			Type:      m.Type,
			Text:      m.ShortText.Text,
		}
		result.Messages = append(result.Messages, msg)
		if m.Type == "E" {
			result.Success = false
		}
	}
	return result, inconclusive, nil
}

func (c *httpClient) GetInactiveObjects(ctx context.Context) ([]ObjectInfo, error) {
	resp, err := c.doRead(ctx, "/sap/bc/adt/activation/inactiveobjects",
		map[string]string{"Accept": "application/vnd.sap.adt.inactivectsobjects.v1+xml"})
	if err != nil {
		return nil, fmt.Errorf("GetInactiveObjects: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetInactiveObjects reading body: %w", err)
	}

	var root struct {
		Entries []struct {
			Object struct {
				Ref struct {
					URI         string `xml:"uri,attr"`
					Type        string `xml:"type,attr"`
					Name        string `xml:"name,attr"`
					PackageName string `xml:"packageName,attr"`
				} `xml:"ref"`
			} `xml:"object"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("GetInactiveObjects parsing: %w", err)
	}

	var result []ObjectInfo
	for _, e := range root.Entries {
		ref := e.Object.Ref
		if ref.Name == "" {
			continue // skip entries without an object (transport-only entries)
		}
		result = append(result, ObjectInfo{
			URI:         ref.URI,
			Type:        ref.Type,
			Name:        ref.Name,
			PackageName: ref.PackageName,
		})
	}
	return result, nil
}
