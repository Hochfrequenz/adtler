package adt

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

// The transport organizer's user actions that fill a request from another
// request or from a package. They are values of IF_CTS_ADT_TM_CONSTANTS
// (co_sap_add_objects_from_req, co_sap_add_objects_frm_package) on
// SAP_BASIS 816; SAP_BASIS 750 has neither.
const (
	userActionAddObjectFromRequest  = "addobjectfromrequest"
	userActionAddObjectsFromPackage = "addobjectsfrompackage"
	userActionAddObject             = "addobject"
)

// addObjectsFromPackageRelation is the atom relation a transport request
// advertises when the system implements addobjectsfrompackage (ABAP
// Platform 2105 and later). The handler emits it only for a request that is
// not released. It is the capability signal for both methods in this file,
// because addobjectfromrequest has no relation of its own.
const addObjectsFromPackageRelation = "http://www.sap.com/cts/relations/addobjectsfrompackage"

// nsTM is the transport organizer's XML namespace.
const nsTM = "http://www.sap.com/cts/adt/tm"

const transportOrganizerType = "application/vnd.sap.adt.transportorganizer.v1+xml"

// CopyTransportObjects copies the object entries of sourceRequest into
// targetRequest, as SE09's "include objects" does. The usual target is a
// transport of copies (category T).
//
// SAP's handler calls TR_COPY_COMM, which copies only the entries recorded
// directly under the number it is given, and records a CORR MERG entry for
// it in the target. Until a request is released its objects sit in its
// tasks, so for an unreleased source this copies the request and each of
// its tasks. A released source already carries its tasks' objects at
// request level and is copied as one. Both sourceRequest and targetRequest
// are meant to be requests; a task has not been tried as either.
//
// The copies are separate PUTs. If one fails, the error names its source
// and the target keeps what the earlier ones copied.
//
// withDocumentation copies the source's documentation as well, which is
// SAP's default. SAP's own query parameter for this is named
// "withdocumentation" but is passed to TR_COPY_COMM's
// wi_without_documentation, so this method sends it only when
// withDocumentation is false.
//
// A 200 from SAP does not prove that anything was copied: the handler also
// answers 200 when it could not lock the target. Read the result back with
// GetTransportObjects where that matters.
//
// The call is refused locally with ErrorNotSupported (ADTError type
// ExceptionTypeAddObjectsUnsupported), and no write is sent, when
// targetRequest does not advertise the addobjectsfrompackage relation —
// on a system older than ABAP Platform 2105, or for a released target.
// On an older system the transport organizer does not reject an unknown
// user action: it treats every PUT as a change-owner request (see #125),
// which fails without a target user and changes nothing, with an error
// about the owner. The PUT goes out only when the system says it
// understands the action, so the caller gets an accurate answer instead.
func (c *httpClient) CopyTransportObjects(ctx context.Context, sourceRequest, targetRequest string, withDocumentation bool) error {
	sourceRequest = strings.ToUpper(strings.TrimSpace(sourceRequest))
	targetRequest = strings.ToUpper(strings.TrimSpace(targetRequest))
	if err := validateTransportNumbers(sourceRequest, targetRequest); err != nil {
		return fmt.Errorf("CopyTransportObjects: %w", err)
	}
	if sourceRequest == targetRequest {
		return fmt.Errorf("CopyTransportObjects: source and target are the same request %s", targetRequest)
	}
	if err := c.ensureAddObjectsSupported(ctx, targetRequest); err != nil {
		return fmt.Errorf("CopyTransportObjects: %w", err)
	}

	sources, err := c.copySources(ctx, sourceRequest)
	if err != nil {
		return fmt.Errorf("CopyTransportObjects: %w", err)
	}

	query := ""
	if !withDocumentation {
		query = "withdocumentation=X"
	}
	for _, from := range sources {
		body, err := xml.Marshal(adtxml.TMRoot{
			NSTM:       nsTM,
			UserAction: userActionAddObjectFromRequest,
			Number:     from,
		})
		if err != nil {
			return fmt.Errorf("CopyTransportObjects: marshal request: %w", err)
		}
		if err := c.putTransportUserAction(ctx, targetRequest, query, body); err != nil {
			return fmt.Errorf("CopyTransportObjects: copy %s into %s: %w", from, targetRequest, err)
		}
	}
	return nil
}

// copySources returns the numbers whose entries make up sourceRequest: the
// request itself, followed by its tasks unless it is released.
func (c *httpClient) copySources(ctx context.Context, sourceRequest string) ([]string, error) {
	data, err := c.readTransportXML(ctx, sourceRequest, transportOrganizerType)
	if err != nil {
		return nil, fmt.Errorf("read source %s: %w", sourceRequest, err)
	}
	info, err := parseTransportInfo(data, sourceRequest)
	if err != nil {
		return nil, fmt.Errorf("read source %s: %w", sourceRequest, err)
	}
	sources := []string{sourceRequest}
	// The tasks of a modifiable request still hold the objects, so they are
	// copied too. A request whose release is in progress (or whose state is
	// unknown) is treated the same way: its tasks are already released and
	// their objects merged into the request, so copying them again is
	// redundant but harmless.
	if info.State == TransportStateReleased {
		return sources, nil
	}
	tasks, err := parseTransportTaskNumbers(data, sourceRequest)
	if err != nil {
		return nil, fmt.Errorf("read tasks of %s: %w", sourceRequest, err)
	}
	return append(sources, tasks...), nil
}

// AddPackageToTransport adds the objects of packageName to targetRequest,
// and with includeSubpackages also those of its subpackages. The usual
// target is a transport of copies (category T).
//
// SAP's handler answers 200 with an empty body whatever happened, including
// when it could not lock the target, so a nil error does not prove that
// anything was added. Read the result back with GetTransportObjects where
// that matters.
//
// The capability gate is the same as CopyTransportObjects': without the
// addobjectsfrompackage relation on targetRequest, the call returns
// ErrorNotSupported and sends no write.
func (c *httpClient) AddPackageToTransport(ctx context.Context, packageName, targetRequest string, includeSubpackages bool) error {
	packageName = strings.ToUpper(strings.TrimSpace(packageName))
	targetRequest = strings.ToUpper(strings.TrimSpace(targetRequest))
	if packageName == "" {
		return fmt.Errorf("AddPackageToTransport: package name is empty")
	}
	if err := validateTransportNumbers(targetRequest); err != nil {
		return fmt.Errorf("AddPackageToTransport: %w", err)
	}
	if err := c.ensureAddObjectsSupported(ctx, targetRequest); err != nil {
		return fmt.Errorf("AddPackageToTransport: %w", err)
	}

	// The handler reads only the name of the first object and does nothing,
	// successfully, when there is none — so the object is always sent. It
	// does not look at pgmid or type; R3TR DEVC is what the entry is.
	body, err := xml.Marshal(adtxml.TMRoot{
		NSTM:       nsTM,
		UserAction: userActionAddObjectsFromPackage,
		Request: &adtxml.TMRequest{
			Objects: []adtxml.TMAbapObject{{PgmID: "R3TR", Type: "DEVC", Name: packageName}},
		},
	})
	if err != nil {
		return fmt.Errorf("AddPackageToTransport: marshal request: %w", err)
	}
	query := ""
	if includeSubpackages {
		query = "includesubpackageobjects=X"
	}
	if err := c.putTransportUserAction(ctx, targetRequest, query, body); err != nil {
		return fmt.Errorf("AddPackageToTransport: %w", err)
	}
	return nil
}

// putTransportUserAction sends a transport organizer user action to
// targetRequest.
func (c *httpClient) putTransportUserAction(ctx context.Context, targetRequest, query string, body []byte) error {
	path := "/sap/bc/adt/cts/transportrequests/" + url.PathEscape(targetRequest)
	if query != "" {
		path += "?" + query
	}
	resp, err := c.doMutate(ctx, http.MethodPut, path,
		strings.NewReader(xml.Header+string(body)),
		map[string]string{
			"Content-Type": transportOrganizerType,
			"Accept":       transportOrganizerType,
		},
	)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return checkResponse(resp)
}

// ensureAddObjectsSupported reads targetRequest and refuses the call unless
// the response advertises the addobjectsfrompackage relation.
//
// Unlike RemoveFromTransport's gate, this one fails closed: a target that
// could not be read, or that carries no such relation, gets no PUT. The
// relation is present on every modifiable request of a system that has the
// action, so a missing one means an older system or a target SAP would
// refuse anyway (released). This deliberately also refuses a system between
// ABAP Platform 1809 and 2105 that might implement addobjectfromrequest
// without advertising it: refusing there costs a missed feature, sending
// to a system without the action returns a misleading change-owner error.
func (c *httpClient) ensureAddObjectsSupported(ctx context.Context, targetRequest string) error {
	ok, err := c.advertisesRelation(ctx, targetRequest, addObjectsFromPackageRelation)
	if err != nil {
		return fmt.Errorf("read target %s: %w", targetRequest, err)
	}
	if ok {
		return nil
	}
	return &ADTError{
		Type: ExceptionTypeAddObjectsUnsupported,
		Message: fmt.Sprintf("transport request %s does not advertise adding objects from a request or package "+
			"(added in ABAP Platform 2105; never offered for a released request); include the objects in SE09 instead",
			targetRequest),
	}
}

// ensureAddObjectSupported is AddToTransport's gate, built like
// ensureAddObjectsSupported and failing closed for the same reason. S/4
// (SAP_BASIS 816) advertises the addobject relation on every request and
// task that is not released; SAP_BASIS 750 has no such action.
func (c *httpClient) ensureAddObjectSupported(ctx context.Context, transport string) error {
	ok, err := c.advertisesRelation(ctx, transport, addObjectRelation)
	if err != nil {
		return fmt.Errorf("read %s: %w", transport, err)
	}
	if ok {
		return nil
	}
	return &ADTError{
		Type: ExceptionTypeAddObjectUnsupported,
		Message: fmt.Sprintf("transport request %s does not advertise adding an object "+
			"(not offered by SAP_BASIS 750, nor for a released request); add the entry in SE09 instead",
			transport),
	}
}

// advertisesRelation reads a transport request or task and reports whether
// its response carries the atom relation rel anywhere, which includes the
// links of its tasks: SAP still decides on the write itself, so a relation
// found on a child only lets the request through to that decision. A body
// that does not parse advertises nothing. A read error, a 401 included, is
// returned unwrapped, so a gate built on it sends no write after a failed
// read.
func (c *httpClient) advertisesRelation(ctx context.Context, number, rel string) (bool, error) {
	data, err := c.readTransportXML(ctx, number, transportOrganizerType)
	if err != nil {
		return false, err
	}
	var root atomLinkNode
	rels := make(map[string]bool)
	if xml.Unmarshal(data, &root) == nil {
		root.collectRels(rels)
	}
	return rels[rel], nil
}

// validateTransportNumbers rejects empty numbers and numbers that would
// change the request path.
func validateTransportNumbers(numbers ...string) error {
	for _, n := range numbers {
		if n == "" {
			return fmt.Errorf("transport number is empty")
		}
		if strings.ContainsAny(n, "/?#% ") {
			return fmt.Errorf("invalid transport number %q", n)
		}
	}
	return nil
}
