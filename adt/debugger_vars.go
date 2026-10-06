package adt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"

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
// have no children here; use GetTableRows. With no parentIDs the server
// treats the request as "@ROOT". VALUE keeps ABAP padding.
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
