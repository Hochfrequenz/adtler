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

// BrowsePackage lists the objects of one package, sub-packages included. Every
// returned ObjectInfo carries the browsed package (upper case) in PackageName.
// For a sub-package that is the package it is listed in, its parent; GetObjectInfo
// on the same sub-package names the sub-package itself.
func (c *httpClient) BrowsePackage(ctx context.Context, packageName string) ([]ObjectInfo, error) {
	params := url.Values{}
	params.Set("parent_type", ObjectTypePackage)
	params.Set("parent_name", packageName)
	// Without this parameter the response carries no description at all
	// (measured on SAP S/4HANA, on-premise: none of 92 objects, against 67 with
	// it; the 25 others have none to give). SAP ERP 6.0 sends none either way and
	// accepts the parameter. See adtler#152.
	params.Set("withShortDescriptions", "true")
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
	// The response names no package per object, but every node is, by
	// definition, an object of the package that was browsed (a sub-package is
	// listed in its parent). ADT writes package names in upper case, which is
	// also what GetObjectInfo returns, so the caller's spelling is normalised.
	// See adtler#152.
	browsed := strings.ToUpper(packageName)
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
			PackageName: browsed,
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
	"/sap/bc/adt/fiori/uiad":                "application/vnd.sap.adt.blues.v2+xml",
}

// fugrIncludeContentType is the vendor MIME type S/4 requires for function
// group include sub-resources (.../functions/groups/<fg>/includes/<inc>).
// The bare /sap/bc/adt/functions/groups prefix maps to a *different* type
// (functions.groups.v3+xml) which S/4 rejects with HTTP 406 for include
// URIs. See adtler#17 / mcp-server-abap#296.
const fugrIncludeContentType = "application/vnd.sap.adt.functions.fincludes.v2+xml"

// fugrModuleContentType is the vendor MIME type S/4 requires for function
// module sub-resources (.../functions/groups/<fg>/fmodules/<fm>). Like the
// includes, they share the function group URI prefix but not its media type:
// S/4 answers the group type with 406 and names this one as the only type it
// produces. The version is the one that 406 response names; a system that does
// not know it answers 406 as well and the */* retry in readWithAcceptFallback
// covers that case. See adtler#169.
//
// The version is fixed here, as it is for the includes, and does not come from
// discovery. A release that publishes only another version answers 406:
// GetObjectInfo and FetchETag recover through the */* retry, DeleteObject has
// no retry. CreateFunctionModule sends the v2 type on create.
const fugrModuleContentType = "application/vnd.sap.adt.functions.fmodules.v3+xml"

// vitObjectPropertiesContentType is the vendor MIME type required by SAP for
// all VIT (Visual IT Tools) object types whose ADT URIs start with
// /sap/bc/adt/vit/wb/object_type/. SAP advertises this type in the 406
// response when the wrong Accept header is sent. See adtler#72.
const vitObjectPropertiesContentType = "application/vnd.sap.adt.basic.object.properties+xml"

// vitObjectTypePrefix is the URI prefix of every VIT object document.
const vitObjectTypePrefix = "/sap/bc/adt/vit/wb/object_type/"

// vitObjectNotFound reports a VIT object that SAP did not find.
//
// VIT answers 200 for a name that does not exist and echoes the name and type
// back without a package, so the status alone cannot tell GetObjectInfo that
// the object is missing (adtler#251). The package can: every real object of
// the VIT types this client reads (UIAC, ADVC/LRP, LRCC/LRP, WDCC/YG) carried
// an <adtcore:packageRef>, and no echo did. Measured on 2026-10-31 over every
// object of these types up to 60 per system and package class, on SAP S/4HANA
// 2025, on-premise (SAP_BASIS 816) and on SAP ERP 6.0 EHP8 (SAP_BASIS 750), which
// serves only wdccyg and echoes the same way. Neither system held a local
// ($TMP) object of these types, so that case is unmeasured.
//
// The rule is confined to VIT. Other endpoints answer a missing object with
// 404, and some of their real objects carry no package of their own (function
// modules, see packageOfFunctionGroup).
//
// The error is shaped like the 404 the other endpoints send, so that
// ClassifyError reports ErrorNotFound and callers need no VIT case of their
// own. SAP's HTTP status was 200; the message says so.
//
// SAP decodes a '+' in the URI path as a blank. A name containing '+' must be
// sent as %2B, or SAP looks up a different name and echoes that one, which
// ends up here as well. The message says so when the URI carries a raw '+',
// because the object may well exist under the name the caller meant.
func vitObjectNotFound(objectURI string) error {
	msg := "GetObjectInfo: " + objectURI + " does not exist (VIT answered 200 and echoed the name " +
		"without a package, which it does for a name it did not find)"
	if strings.Contains(objectURI, "+") {
		msg += "; the URI contains a raw '+', which SAP reads as a blank — encode it as %2B"
	}
	return &ADTError{
		StatusCode: http.StatusNotFound,
		Type:       ExceptionTypeResourceNotFound,
		Message:    msg,
	}
}

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
	if strings.HasPrefix(objectURI, "/sap/bc/adt/functions/groups/") &&
		strings.Contains(objectURI, "/fmodules/") {
		return fugrModuleContentType + ", application/xml"
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
	info, err := parseGenericObjectInfo(data, objectURI)
	if err != nil {
		return nil, err
	}
	if info.PackageName == "" && strings.HasPrefix(objectURI, vitObjectTypePrefix) {
		return nil, vitObjectNotFound(objectURI)
	}
	if info.PackageName == "" {
		if groupURI, ok := functionGroupURIOfPart(objectURI); ok {
			pkg, err := c.packageOfFunctionGroup(ctx, groupURI)
			if err != nil {
				return nil, err
			}
			info.PackageName = pkg
		}
	}
	return info, nil
}

// functionGroupURIOfPart returns the URI of the function group that a function
// module (.../functions/groups/<fg>/fmodules/<fm>) or a function group include
// (.../functions/groups/<fg>/includes/<inc>) belongs to.
func functionGroupURIOfPart(objectURI string) (string, bool) {
	const prefix = "/sap/bc/adt/functions/groups/"
	rest, ok := strings.CutPrefix(objectURI, prefix)
	if !ok {
		return "", false
	}
	group, part, ok := strings.Cut(rest, "/")
	if !ok || group == "" {
		return "", false
	}
	if !strings.HasPrefix(part, "fmodules/") && !strings.HasPrefix(part, "includes/") {
		return "", false
	}
	return prefix + group, true
}

// packageOfFunctionGroup reads the package from a function group's object
// document. Function modules and function group includes have no package of
// their own: they live in the package of their function group, and their own
// documents carry no packageRef on SAP ERP 6.0 or SAP S/4HANA (adtler#169,
// adtler#232).
//
// The lookup is best effort and returns "" when the group cannot be read. The
// object itself was read successfully, and one denied or failed second request
// is no reason to withhold it from the caller. A cancelled or expired
// context is the exception: the caller gave up, so it gets that error instead
// of a success with a package that was never looked up.
func (c *httpClient) packageOfFunctionGroup(ctx context.Context, groupURI string) (string, error) {
	group, err := c.GetObjectInfo(ctx, groupURI)
	if err != nil {
		return "", ctx.Err()
	}
	return group.PackageName, nil
}

// parseGenericObjectInfo extracts ObjectInfo from any ADT object XML response.
// All ADT object types share adtcore:name, adtcore:type, adtcore:description
// attributes on the root element and an <adtcore:packageRef> child element.
// The object document does not reliably repeat its own address, so the URI
// the caller fetched it from is reported as ObjectInfo.URI.
func parseGenericObjectInfo(data []byte, objectURI string) (*ObjectInfo, error) {
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
		URI:         objectURI,
		Name:        obj.Name,
		Type:        obj.Type,
		Description: obj.Description,
		PackageName: obj.PackageRef.Name,
	}, nil
}
