package adt

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ClassRunResult holds the console output produced by executing a global ABAP
// class via the classrun endpoint ("Run as ABAP Application").
type ClassRunResult struct {
	ClassName     string `json:"class_name"`
	ConsoleOutput string `json:"console_output"`
}

// ClassRunClient executes global ABAP classes that implement IF_OO_ADT_CLASSRUN.
type ClassRunClient interface {
	// RunClass executes the global class className via the ADT classrun
	// endpoint and returns whatever the class writes to the console handler
	// (out->write(...)). It does not validate that the class exists, is
	// active, or implements IF_OO_ADT_CLASSRUN — the caller pre-checks that.
	RunClass(ctx context.Context, className string) (*ClassRunResult, error)
}

// RunClass POSTs to /sap/bc/adt/oo/classrun/{name} (name lower-cased) with an
// empty body and Accept: text/plain, and returns the class's console output.
//
// The classrun runs on an ISOLATED FRESH session (see freshSession): on S/4 the
// caller's session that performed create -> set source -> activate cannot
// generate the class's runtime load and the handler soft-fails with
// "does not implement ...main...", but a fresh session generates the load and
// returns real output (issue #106, defect 1). classrun is stateless — it only
// executes the class; any locking or commit the class performs is the class's
// own concern — so an isolated session is consistent with its contract and
// leaves the caller's session and locks untouched.
//
// Namespace slashes in className are percent-encoded automatically by
// doMutate → encodeNamespacePath (triggered by the "//" that results from
// appending "/na2/foo" to the base).
func (c *httpClient) RunClass(ctx context.Context, className string) (*ClassRunResult, error) {
	fresh := c.freshSession()
	uri := "/sap/bc/adt/oo/classrun/" + strings.ToLower(className)
	resp, err := fresh.doMutate(ctx, http.MethodPost, uri, nil,
		map[string]string{"Accept": contentTypeTextPlain})
	if err != nil {
		return nil, fmt.Errorf("RunClass: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("RunClass reading body: %w", err)
	}
	return &ClassRunResult{ClassName: className, ConsoleOutput: string(body)}, nil
}
