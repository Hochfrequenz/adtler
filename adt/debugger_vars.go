package adt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

const (
	ctChildVariables = "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.ChildVariables"
	ctVariables      = "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.Variables"
	acceptASXML      = "application/vnd.sap.as+xml"
)

// DebugVariable describes one variable of the debuggee.
type DebugVariable struct {
	ID, Name, MetaType, DeclaredType, ActualType, TechnicalType, Value string
	ValueIncomplete                                                    bool
	TableLines                                                         int
	Kind, AccessKind, InstantiationKind, ParameterKind                 string
	Length                                                             int
	ReadOnly, IsException                                              bool
	InheritanceClass                                                   string
}

// DebugVariableLink is a parent/child edge in the variable hierarchy.
type DebugVariableLink struct {
	ParentID, ChildID, ChildName string
}

// DebugChildVariables is the result of GetChildVariables.
type DebugChildVariables struct {
	Variables []DebugVariable
	Links     []DebugVariableLink
}

// DebugTableField is one field value of a table row.
type DebugTableField struct{ Path, Value string }

// DebugTableRow is one internal table row.
type DebugTableRow struct {
	Index  int
	Fields []DebugTableField
}

// DebugTablePage is one page of an internal table.
type DebugTablePage struct {
	Name               string
	TotalLines, Offset int
	Rows               []DebugTableRow
}

// ErrNotATable is returned by GetTableRows for a variable that is not an internal table.
var ErrNotATable = errors.New("variable is not an internal table")

// postASX POSTs an asXML body to /sap/bc/adt/debugger?method=<method> on the
// stateful debug session and returns the response body.
func (d *DebugSession) postASX(ctx context.Context, method, contentType string, body []byte) ([]byte, error) {
	resp, err := d.client.doMutate(ctx, http.MethodPost, "/sap/bc/adt/debugger?method="+method,
		bytes.NewReader(body), map[string]string{
			"Content-Type":          contentType,
			"Accept":                acceptASXML,
			"X-sap-adt-sessiontype": "stateful",
		})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

func toDebugVariable(v adtxml.ASXVariable) DebugVariable {
	return DebugVariable{
		ID: v.ID, Name: v.Name, MetaType: v.MetaType,
		DeclaredType: v.DeclaredTypeName, ActualType: v.ActualTypeName, TechnicalType: v.TechnicalType,
		Value: v.Value, ValueIncomplete: v.IsValueIncomplete == "X", TableLines: v.TableLines,
		Kind: v.Kind, AccessKind: v.AccessKind, InstantiationKind: v.InstantiationKind, ParameterKind: v.ParameterKind,
		Length: v.Length, ReadOnly: v.ReadOnly == "X", IsException: v.IsException == "X",
		InheritanceClass: v.InheritanceClass,
	}
}

// GetChildVariables expands "@ROOT", "@LOCALS", "@PARAMETERS", "@GLOBALS",
// "@SYSTEM" or any variable ID (structure → components, object reference →
// attributes, "REF->*" → dereferenced value, "ITAB[n]" → row). Internal tables
// have no children here; use GetTableRows. With no parentIDs it requests
// "@ROOT" explicitly. VALUE keeps ABAP padding.
func (d *DebugSession) GetChildVariables(ctx context.Context, parentIDs ...string) (*DebugChildVariables, error) {
	body, err := adtxml.ChildVariablesRequest(parentIDs)
	if err != nil {
		return nil, fmt.Errorf("GetChildVariables marshal: %w", err)
	}
	data, err := d.postASX(ctx, "getChildVariables", ctChildVariables, body)
	if err != nil {
		return nil, err
	}
	vars, links, err := adtxml.ParseChildVariables(data)
	if err != nil {
		return nil, fmt.Errorf("GetChildVariables unmarshal: %w", err)
	}
	out := &DebugChildVariables{}
	for _, v := range vars {
		out.Variables = append(out.Variables, toDebugVariable(v))
	}
	for _, l := range links {
		if l.ChildID == "" {
			continue
		}
		out.Links = append(out.Links, DebugVariableLink{ParentID: l.ParentID, ChildID: l.ChildID, ChildName: l.ChildName})
	}
	return out, nil
}

// GetVariables returns metadata for ids ("A-B", "OBJ->ATTR", "ITAB[n]", "ITAB[]",
// "REF->COMP"). IDs the server does not know are omitted — match by ID.
func (d *DebugSession) GetVariables(ctx context.Context, ids ...string) ([]DebugVariable, error) {
	body, err := adtxml.VariablesRequest(ids)
	if err != nil {
		return nil, fmt.Errorf("GetVariables marshal: %w", err)
	}
	data, err := d.postASX(ctx, "getVariables", ctVariables, body)
	if err != nil {
		return nil, err
	}
	vars, err := adtxml.ParseVariables(data)
	if err != nil {
		return nil, fmt.Errorf("GetVariables unmarshal: %w", err)
	}
	out := make([]DebugVariable, 0, len(vars))
	for _, v := range vars {
		out = append(out, toDebugVariable(v))
	}
	return out, nil
}

// GetTableRows reads one page of internal table name. offset is 1-based; limit
// is clamped to the row count (SAP_BASIS 750 fails when a page runs past the
// end). An offset beyond the last row returns an empty page without calling
// SAP. One table per request (the server keeps its field buffer between tables).
func (d *DebugSession) GetTableRows(ctx context.Context, name string, offset, limit int, fields ...string) (*DebugTablePage, error) {
	if offset < 1 {
		return nil, fmt.Errorf("GetTableRows: offset must be >= 1 (1-based), got %d", offset)
	}
	if limit < 1 {
		return nil, fmt.Errorf("GetTableRows: limit must be >= 1, got %d", limit)
	}
	vars, err := d.GetVariables(ctx, name)
	if err != nil {
		return nil, err
	}
	meta := findTableMeta(vars, name)
	if meta == nil {
		return nil, fmt.Errorf("GetTableRows: unknown variable %q", name)
	}
	if meta.MetaType != "table" {
		return nil, fmt.Errorf("GetTableRows %q (%s): %w", name, meta.MetaType, ErrNotATable)
	}
	page := &DebugTablePage{Name: name, TotalLines: meta.TableLines, Offset: offset}
	if offset > meta.TableLines {
		return page, nil
	}
	if remaining := meta.TableLines - offset + 1; limit > remaining {
		limit = remaining
	}
	body, err := adtxml.VariableDataRequest(name, offset, limit, fields)
	if err != nil {
		return nil, fmt.Errorf("GetTableRows marshal: %w", err)
	}
	resp, err := d.client.doMutate(ctx, http.MethodPost, "/sap/bc/adt/debugger?method=getVariableData",
		bytes.NewReader(body), map[string]string{
			"Content-Type":          contentTypeXML,
			"Accept":                "application/xml",
			"X-sap-adt-sessiontype": "stateful",
		})
	if err != nil {
		return nil, fmt.Errorf("GetTableRows: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if err := checkResponse(resp); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("GetTableRows read: %w", err)
	}
	tbl, err := adtxml.ParseVariableData(data)
	if err != nil {
		return nil, fmt.Errorf("GetTableRows unmarshal: %w", err)
	}
	if tbl == nil {
		return page, nil
	}
	for _, l := range tbl.Lines {
		row := DebugTableRow{Index: l.Index}
		for _, f := range l.Fields {
			row.Fields = append(row.Fields, DebugTableField{Path: f.Path, Value: f.Value})
		}
		page.Rows = append(page.Rows, row)
	}
	return page, nil
}

// findTableMeta picks the variable answering a single-ID getVariables request.
// SAP may echo another ID form (instance form for object attributes, other
// case), so: exact ID, else the only entry returned, else a case-insensitive ID.
func findTableMeta(vars []DebugVariable, name string) *DebugVariable {
	for i := range vars {
		if vars[i].ID == name {
			return &vars[i]
		}
	}
	if len(vars) == 1 {
		return &vars[0]
	}
	for i := range vars {
		if strings.EqualFold(vars[i].ID, name) {
			return &vars[i]
		}
	}
	return nil
}
