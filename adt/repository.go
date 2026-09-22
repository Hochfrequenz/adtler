package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func (c *httpClient) BrowsePackage(ctx context.Context, packageName string) ([]ObjectInfo, error) {
	params := url.Values{}
	params.Set("parent_type", ObjectTypePackage)
	params.Set("parent_name", packageName)
	path := "/sap/bc/adt/repository/nodestructure?" + params.Encode()

	resp, err := c.doMutate(ctx, http.MethodPost, path, nil,
		map[string]string{
			"Accept":       "application/vnd.sap.as+xml",
			"Content-Type": contentTypeXML,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("BrowsePackage: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, _ := io.ReadAll(resp.Body)
	tc, err := adtxml.UnmarshalASXData[adtxml.PackageTreeContent](data)
	if err != nil {
		return nil, fmt.Errorf("BrowsePackage parsing: %w", err)
	}
	result := make([]ObjectInfo, 0, len(tc.Nodes))
	for _, n := range tc.Nodes {
		if n.ObjectName == "" {
			continue // skip empty root node
		}
		result = append(result, ObjectInfo{
			URI:         n.ObjectURI,
			Type:        n.ObjectType,
			Name:        n.ObjectName,
			Description: n.Description,
		})
	}
	return result, nil
}

// objectTypeAcceptHeaders maps ADT URI path prefixes to their required Accept headers.
var objectTypeAcceptHeaders = map[string]string{
	"/sap/bc/adt/programs/programs":         "application/vnd.sap.adt.programs.programs.v2+xml",
	"/sap/bc/adt/programs/includes":         "application/vnd.sap.adt.programs.includes.v2+xml",
	"/sap/bc/adt/oo/classes":                "application/vnd.sap.adt.oo.classes.v4+xml",
	"/sap/bc/adt/oo/interfaces":             "application/vnd.sap.adt.oo.interfaces.v5+xml",
	"/sap/bc/adt/functions/groups":          "application/vnd.sap.adt.functions.groups.v3+xml",
	"/sap/bc/adt/ddic/dataelements":         "application/vnd.sap.adt.dataelements.v2+xml",
	"/sap/bc/adt/ddic/domains":              "application/vnd.sap.adt.domains.v2+xml",
	"/sap/bc/adt/ddic/tables":               "application/vnd.sap.adt.tables.v2+xml",
	"/sap/bc/adt/ddic/tabletypes":           "application/vnd.sap.adt.tabletype.v1+xml",
	"/sap/bc/adt/ddic/typegroups":           "application/vnd.sap.adt.ddic.typegroups.v2+xml",
	"/sap/bc/adt/ddic/ddl/sources":          "application/vnd.sap.adt.ddlSource+xml",
	"/sap/bc/adt/ddic/ddlx/sources":         "application/vnd.sap.adt.ddic.ddlx.v1+xml",
	"/sap/bc/adt/ddic/ddla/sources":         "application/vnd.sap.adt.ddic.ddla.v1+xml",
	"/sap/bc/adt/ddic/srvd/sources":         "application/vnd.sap.adt.ddic.srvd.v1+xml",
	"/sap/bc/adt/packages":                  "application/vnd.sap.adt.packages.v2+xml",
	"/sap/bc/adt/bo/behaviordefinitions":    "application/vnd.sap.adt.blues.v1+xml",
	"/sap/bc/adt/businessservices/bindings": "application/vnd.sap.adt.businessservices.servicebinding.v2+xml",
	"/sap/bc/adt/acm/dcl/sources":           "application/vnd.sap.adt.dclSource+xml",
	"/sap/bc/adt/vit/wb/object_type":        vitObjectPropertiesContentType,
}

// fugrIncludeContentType is the vendor MIME type S/4 requires for function
// group include sub-resources (.../functions/groups/<fg>/includes/<inc>).
// The bare /sap/bc/adt/functions/groups prefix maps to a *different* type
// (functions.groups.v3+xml) which S/4 rejects with HTTP 406 for include
// URIs. See adtler#17 / mcp-server-abap#296.
const fugrIncludeContentType = "application/vnd.sap.adt.functions.fincludes.v2+xml"

// vitObjectPropertiesContentType is the vendor MIME type required by SAP for
// all VIT (Visual IT Tools) object types whose ADT URIs start with
// /sap/bc/adt/vit/wb/object_type/. SAP advertises this type in the 406
// response when the wrong Accept header is sent. See adtler#72.
const vitObjectPropertiesContentType = "application/vnd.sap.adt.basic.object.properties+xml"

// acceptHeaderForURI returns the best Accept header for a given object URI.
// It first checks the ADT discovery cache (populated from /sap/bc/adt/discovery
// during CSRF fetch) for supported content types, then falls back to the
// hardcoded objectTypeAcceptHeaders map.
//
// Sub-resources whose content type differs from their parent endpoint
// (e.g. function group includes vs. the function group itself) are handled
// by an explicit pre-check before the prefix loop, because the
// objectTypeAcceptHeaders map only does straight prefix matching.
func (c *httpClient) acceptHeaderForURI(objectURI string) string {
	// Sub-resource special cases — must run before the longest-prefix loop
	// because the parent prefix would otherwise win and return the wrong type.
	if strings.HasPrefix(objectURI, "/sap/bc/adt/functions/groups/") &&
		strings.Contains(objectURI, "/includes/") {
		return fugrIncludeContentType + ", application/xml"
	}

	// Find the best matching prefix from the hardcoded map.
	bestPrefix := ""
	hardcoded := ""
	for prefix, accept := range objectTypeAcceptHeaders {
		if strings.HasPrefix(objectURI, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix = prefix
			hardcoded = accept
		}
	}
	if bestPrefix == "" {
		return contentTypeXML
	}
	// Check if discovery knows this endpoint. If it does, use the first
	// content type the system supports (discovery lists them in preference
	// order). Otherwise fall back to the hardcoded value.
	c.mu.Lock()
	accepted := c.discovery[bestPrefix]
	c.mu.Unlock()
	if len(accepted) > 0 {
		return accepted[0] + ", application/xml"
	}
	return hardcoded + ", application/xml"
}

// acceptAnyMediaType is the Accept header a read falls back to when ADT
// refuses the first, specific offer with 406. See readWithAcceptFallback.
const acceptAnyMediaType = "*/*"

// readWithAcceptFallback GETs uri with the given Accept header and, if ADT
// answers 406, asks once more with */*.
//
// An ADT 406 means the Accept header is wrong, never that the resource is
// missing — ADT publishes exactly one media type per object kind and will
// produce nothing else. It is easy to misread: adtler#65 recorded a service
// binding's 406 as a missing endpoint and went looking for another path,
// when the path had been right all along and only the offer was wrong. The
// same run saw a behavior definition's object document and two DDIC tables
// answer 406 to "application/xml" and 200 to */*.
//
// No client can know every vendor media type SAP will ever publish, so on a
// 406 it stops guessing and lets the server choose. The specific offer is
// still made first: asking for */* up front would change what the server
// returns for the object kinds that work today. Only 406 is retried — a 404
// is a missing resource, and a wider Accept header cannot conjure one.
// Callers pass a specific offer: acceptHeaderForURI returns a vendor type or
// "application/xml", never */*, so the retry can never repeat the first
// request. There is deliberately no guard for that case — an unreachable
// branch and a test for an impossible state cost more than they protect.
func (c *httpClient) readWithAcceptFallback(ctx context.Context, uri, accept string) (*http.Response, error) {
	resp, err := c.doRead(ctx, uri, map[string]string{"Accept": accept})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusNotAcceptable {
		return resp, nil
	}
	// Drain and close before reusing the connection for the retry.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return c.doRead(ctx, uri, map[string]string{"Accept": acceptAnyMediaType})
}

func (c *httpClient) GetObjectInfo(ctx context.Context, objectURI string) (*ObjectInfo, error) {
	accept := c.acceptHeaderForURI(objectURI)
	resp, err := c.readWithAcceptFallback(ctx, objectURI, accept)
	if err != nil {
		return nil, fmt.Errorf("GetObjectInfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}

	data, _ := io.ReadAll(resp.Body)
	return parseGenericObjectInfo(data)
}

// parseGenericObjectInfo extracts ObjectInfo from any ADT object XML response.
// All ADT object types share adtcore:name, adtcore:type, adtcore:description
// attributes on the root element and an <adtcore:packageRef> child element.
func parseGenericObjectInfo(data []byte) (*ObjectInfo, error) {
	var obj struct {
		Name        string `xml:"name,attr"`
		Type        string `xml:"type,attr"`
		Description string `xml:"description,attr"`
		PackageRef  struct {
			Name string `xml:"name,attr"`
		} `xml:"packageRef"`
	}
	if err := xml.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("GetObjectInfo parsing: %w", err)
	}
	// Go's XML decoder happily parses a document whose elements and attributes
	// mean nothing to this struct — an HTML page, for one — and leaves every
	// field zero without reporting an error. Every ADT object document carries
	// adtcore:name, so an empty name means the body was not one, and saying so
	// beats handing back an object that reports no name, type or package as if
	// it were real. Reachable because the */* fallback lets the server choose
	// the representation, and discovery advertises text/html beside the vendor
	// types. See adtler#65.
	if obj.Name == "" {
		return nil, fmt.Errorf("GetObjectInfo parsing: response carried no adtcore:name — it is not an ADT object document")
	}
	return &ObjectInfo{
		Name:        obj.Name,
		Type:        obj.Type,
		Description: obj.Description,
		PackageName: obj.PackageRef.Name,
	}, nil
}
