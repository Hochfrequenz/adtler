package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
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
// Activating any one URI of a class pool — the class, its /source/main, or
// its /includes/testclasses — activates every inactive part of that class
// pool with it: the class, its includes, and its method and source-unit
// entries. (A class recorded in a request on SAP_BASIS 750 first gets a
// pre-audit list instead; postActivation answers it, see #279.)
// Measured on local ($TMP) classes on SAP ERP 6.0 EHP8
// (SAP_BASIS 750) and SAP S/4HANA 2025 (SAP_BASIS 816);
// TestActivateObjects_ClassPoolSubIncludes_Integration guards it. A
// requested URI that stays inactive despite a successful POST — or a part
// nested under it, or one it is nested under — is the silent no-op above
// and comes back as Success: false (#70).
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
//
// The first POST asks for a pre-audit. Where SAP answers it with a list of
// inactive objects instead of activating (#279), the activation is sent
// again without the pre-audit, for the requested objects plus the listed
// objects that belong to them (see preauditFollowUp), and that answer is
// parsed instead.
func (c *httpClient) postActivation(ctx context.Context, objectURIs []string) (*ActivationResult, error) {
	objects := make([]adtxml.ActivationObject, len(objectURIs))
	for i, uri := range objectURIs {
		objects[i] = adtxml.ActivationObject{URI: uri}
	}
	data, err := c.sendActivation(ctx, objects, true)
	if err != nil {
		return nil, err
	}
	if followUp := preauditFollowUp(objects, data); followUp != nil {
		if data, err = c.sendActivation(ctx, followUp, false); err != nil {
			return nil, err
		}
	}

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

// sendActivation POSTs one activation request for objects and returns the
// response body.
func (c *httpClient) sendActivation(ctx context.Context, objects []adtxml.ActivationObject, preaudit bool) ([]byte, error) {
	bodyXML, err := xml.Marshal(adtxml.ActivationRequest{
		NS:      nsADTCore,
		Objects: objects,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal activation request: %w", err)
	}

	resp, err := c.doMutate(ctx, http.MethodPost,
		"/sap/bc/adt/activation?method=activate&preauditRequested="+strconv.FormatBool(preaudit),
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
	return data, nil
}

// preauditFollowUp returns the objects of the second activation request when
// body is a pre-audit answer (adtxml.ActivationPreaudit), and nil otherwise.
//
// On SAP ERP 6.0 EHP8 (SAP_BASIS 750) a class whose parts are inactive and
// recorded in a request is not activated by the first POST: SAP answers 200
// with the inactive parts (the class, its methods, its source units) and
// expects them back, which is what Eclipse's "activate these objects too?"
// dialog does. Activating the class URI alone again changes nothing (#279).
//
// The follow-up holds the requested objects, enriched with the type, name
// and parent of their own entry, and every listed object nested under or
// parented by one of them. Other listed objects are left out: they are other
// inactive work of the same user, which the caller did not ask to activate.
// Entries that carry only a transport are skipped. Measured 2026-11-05 on
// the ECC system: the class plus its listed parts, sent without a pre-audit,
// activated every part.
func preauditFollowUp(requested []adtxml.ActivationObject, body []byte) []adtxml.ActivationObject {
	var preaudit adtxml.ActivationPreaudit
	if err := xml.Unmarshal(body, &preaudit); err != nil {
		return nil
	}
	followUp := append([]adtxml.ActivationObject(nil), requested...)
	for _, e := range preaudit.Entries {
		ref := e.Object.Ref
		if ref == nil || ref.URI == "" || !belongsToRequested(requested, ref) {
			continue
		}
		obj := adtxml.ActivationObject{URI: ref.URI, Type: ref.Type, Name: ref.Name, ParentURI: ref.ParentURI}
		if i := indexOfURI(followUp, ref.URI); i >= 0 {
			followUp[i] = obj
			continue
		}
		followUp = append(followUp, obj)
	}
	return followUp
}

// belongsToRequested reports whether ref is one of the requested objects, is
// nested under one, or names one as its parent. The match is one-way: an
// object a requested URI is nested under (a function group, for a requested
// function module) is not pulled in, since activating it would also activate
// its other inactive parts.
func belongsToRequested(requested []adtxml.ActivationObject, ref *adtxml.ActivationPreauditRef) bool {
	for _, r := range requested {
		if sameOrNestedUnder(ref.URI, r.URI) || (ref.ParentURI != "" && sameOrNestedUnder(ref.ParentURI, r.URI)) {
			return true
		}
	}
	return false
}

// sameOrNestedUnder reports whether uri is root or a path below it, ignoring
// case, a query or fragment, and a trailing slash (see normalizeObjectURI).
func sameOrNestedUnder(uri, root string) bool {
	uri, root = strings.ToLower(normalizeObjectURI(uri)), strings.ToLower(normalizeObjectURI(root))
	return uri == root || strings.HasPrefix(uri, root+"/")
}

// indexOfURI returns the index of the object whose URI equals uri, ignoring
// case and a trailing slash, or -1. Fragments count: two methods of one class
// share the URI up to their #type=…;name=… fragment.
func indexOfURI(objects []adtxml.ActivationObject, uri string) int {
	for i, o := range objects {
		if strings.EqualFold(strings.TrimSuffix(o.URI, "/"), strings.TrimSuffix(uri, "/")) {
			return i
		}
	}
	return -1
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
