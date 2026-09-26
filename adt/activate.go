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

// ActivateObjects activates one or more ABAP objects, then confirms the
// outcome by re-checking GetInactiveObjects: some systems (notably ECC, for
// a namespaced CLAS in a transportable package) return 2xx from the
// activation POST with a body that never carries an error message, while
// the object stays inactive (Hochfrequenz/aibap.mcp#500). Trusting that
// body alone is not enough, so on any apparent success this re-reads the
// inactive-objects list and overrides Success to false — with a synthesized
// message — if a requested object is still listed there.
//
// This mirrors ReleaseTransport (adt/transport.go): if the
// activation body already carries an explicit error, verification is
// skipped (nothing more to learn). If the verification read itself fails,
// the unverified (optimistic) result is returned unchanged rather than
// turning a transport-layer hiccup into a false failure.
func (c *httpClient) ActivateObjects(ctx context.Context, objectURIs []string) (*ActivationResult, error) {
	result, err := c.postActivation(ctx, objectURIs)
	if err != nil {
		return nil, err
	}
	if !result.Success {
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
// still lists, per objectURIMatches.
func stillInactiveURIs(objectURIs []string, inactive []ObjectInfo) []string {
	var result []string
	for _, uri := range objectURIs {
		for _, obj := range inactive {
			if objectURIMatches(uri, obj.URI) {
				result = append(result, uri)
				break
			}
		}
	}
	return result
}

// objectURIMatches reports whether requested and candidate refer to the same
// object, tolerating the ways two URIs for the same thing can legitimately
// differ here:
//   - a trailing slash, or a query/fragment suffix such as
//     "?version=inactive" or "#start=5,0"
//   - percent-encoding hex-digit case (%2f vs %2F) or object-name case
//     (SAP is not guaranteed to echo namespace/name casing consistently)
//   - one being a specific include nested under the other (an inactive
//     entry may point at ".../source/main" while the caller requested the
//     bare object URI, or vice versa — aibap.mcp#500's own reproduction
//     passed an includes/* URI as the "object")
//
// A plain unidirectional prefix check (as an earlier version of this
// function used) misses the last case whenever the include URI is the
// *shorter* of the two, and is case-sensitive, so it silently fails open —
// degrading to "not still inactive" — on any of the above. Failing open
// only means falling back to the pre-verification optimistic behavior, not
// a new false positive, but it defeats the point of verifying.
func objectURIMatches(requested, candidate string) bool {
	requested = normalizeObjectURI(requested)
	candidate = normalizeObjectURI(candidate)
	if strings.EqualFold(requested, candidate) {
		return true
	}
	shorter, longer := requested, candidate
	if len(longer) < len(shorter) {
		shorter, longer = longer, shorter
	}
	return len(longer) > len(shorter) &&
		strings.EqualFold(longer[:len(shorter)], shorter) &&
		longer[len(shorter)] == '/'
}

// normalizeObjectURI strips a query string or fragment and any trailing
// slash, so "…/source/main#start=5,0" and "…/source/main/" compare equal to
// "…/source/main".
func normalizeObjectURI(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	return strings.TrimSuffix(uri, "/")
}

// postActivation performs the activation POST and parses whatever message
// body comes back. An empty or unparseable body — the common shape on ECC
// even for a genuinely successful activation (Hochfrequenz/aibap.mcp#34) —
// yields Success:true with no messages, same as a body that explicitly says
// so; ActivateObjects treats both the same way and verifies regardless.
func (c *httpClient) postActivation(ctx context.Context, objectURIs []string) (*ActivationResult, error) {
	objects := make([]adtxml.ActivationObject, len(objectURIs))
	for i, uri := range objectURIs {
		objects[i] = adtxml.ActivationObject{URI: uri}
	}
	bodyXML, err := xml.Marshal(adtxml.ActivationRequest{
		NS:      nsADTCore,
		Objects: objects,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal activation request: %w", err)
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
		return nil, fmt.Errorf("ActivateObjects: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, _ := io.ReadAll(resp.Body)
	var msgs adtxml.ActivationMessages
	xml.Unmarshal(data, &msgs) //nolint:errcheck // an empty/unparseable body is treated as "no messages", see doc comment

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
	return result, nil
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
