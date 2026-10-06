package adtxml

import "encoding/xml"

// asXML request/response types for the debugger methods getChildVariables,
// getVariables and getVariableData (POST /sap/bc/adt/debugger?method=…).
// Verified live 2026-10-06 on SAP_BASIS 816 and 750 (adtler#201).

const nsASX = "http://www.sap.com/abapxml"

type asxHierarchyReq struct {
	ParentID string `xml:"PARENT_ID"`
}

type asxChildVarsReq struct {
	XMLName     xml.Name          `xml:"asx:abap"`
	NS          string            `xml:"xmlns:asx,attr"`
	Version     string            `xml:"version,attr"`
	Hierarchies []asxHierarchyReq `xml:"asx:values>DATA>HIERARCHIES>STPDA_ADT_VARIABLE_HIERARCHY"`
}

// ChildVariablesRequest builds the getChildVariables body. An empty slice
// requests @ROOT.
func ChildVariablesRequest(parentIDs []string) ([]byte, error) {
	if len(parentIDs) == 0 {
		parentIDs = []string{"@ROOT"}
	}
	req := asxChildVarsReq{NS: nsASX, Version: "1.0"}
	for _, id := range parentIDs {
		req.Hierarchies = append(req.Hierarchies, asxHierarchyReq{ParentID: id})
	}
	b, err := xml.Marshal(req)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}

type asxVarIDReq struct {
	ID string `xml:"ID"`
}

type asxVariablesReq struct {
	XMLName xml.Name      `xml:"asx:abap"`
	NS      string        `xml:"xmlns:asx,attr"`
	Version string        `xml:"version,attr"`
	Vars    []asxVarIDReq `xml:"asx:values>DATA>STPDA_ADT_VARIABLE"`
}

// VariablesRequest builds the getVariables body.
func VariablesRequest(ids []string) ([]byte, error) {
	req := asxVariablesReq{NS: nsASX, Version: "1.0"}
	for _, id := range ids {
		req.Vars = append(req.Vars, asxVarIDReq{ID: id})
	}
	b, err := xml.Marshal(req)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), b...), nil
}

// ASXVariable is one STPDA_ADT_VARIABLE entry. Flags are 'X' or empty.
type ASXVariable struct {
	ID                string `xml:"ID"`
	Name              string `xml:"NAME"`
	DeclaredTypeName  string `xml:"DECLARED_TYPE_NAME"`
	ActualTypeName    string `xml:"ACTUAL_TYPE_NAME"`
	Kind              string `xml:"KIND"`
	InstantiationKind string `xml:"INSTANTIATION_KIND"`
	AccessKind        string `xml:"ACCESS_KIND"`
	ParameterKind     string `xml:"PARAMETER_KIND"`
	MetaType          string `xml:"META_TYPE"`
	Value             string `xml:"VALUE"`
	IsValueIncomplete string `xml:"IS_VALUE_INCOMPLETE"`
	ReadOnly          string `xml:"READ_ONLY"`
	TechnicalType     string `xml:"TECHNICAL_TYPE"`
	Length            int    `xml:"LENGTH"`
	TableLines        int    `xml:"TABLE_LINES"`
	IsException       string `xml:"IS_EXCEPTION"`
	InheritanceClass  string `xml:"INHERITANCE_CLASS"`
}

// ASXHierarchy is one parent/child link.
type ASXHierarchy struct {
	ParentID  string `xml:"PARENT_ID"`
	ChildID   string `xml:"CHILD_ID"`
	ChildName string `xml:"CHILD_NAME"`
}

type asxChildVarsResp struct {
	Vars  []ASXVariable  `xml:"values>DATA>VARIABLES>STPDA_ADT_VARIABLE"`
	Links []ASXHierarchy `xml:"values>DATA>HIERARCHIES>STPDA_ADT_VARIABLE_HIERARCHY"`
}

// ParseChildVariables parses a getChildVariables response.
func ParseChildVariables(data []byte) ([]ASXVariable, []ASXHierarchy, error) {
	var r asxChildVarsResp
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, nil, err
	}
	return r.Vars, r.Links, nil
}

type asxVariablesResp struct {
	Vars []ASXVariable `xml:"values>DATA>STPDA_ADT_VARIABLE"`
}

// ParseVariables parses a getVariables response. Unknown IDs are simply absent.
func ParseVariables(data []byte) ([]ASXVariable, error) {
	var r asxVariablesResp
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return r.Vars, nil
}

type dataFieldReq struct {
	Path string `xml:"path,attr"`
}

type dataTableReq struct {
	Name   string         `xml:"name,attr"`
	Offset int            `xml:"offset,attr"`
	Length int            `xml:"length,attr"`
	Fields []dataFieldReq `xml:"field"`
}

type dataRequest struct {
	XMLName xml.Name     `xml:"dbg:dataRequest"`
	NS      string       `xml:"xmlns:dbg,attr"`
	Table   dataTableReq `xml:"table"`
}

// VariableDataRequest builds the getVariableData body for ONE table (the
// server does not reset its field buffer between tables). offset is 1-based.
func VariableDataRequest(name string, offset, length int, fields []string) ([]byte, error) {
	req := dataRequest{NS: "http://www.sap.com/adt/debugger", Table: dataTableReq{Name: name, Offset: offset, Length: length}}
	for _, f := range fields {
		req.Table.Fields = append(req.Table.Fields, dataFieldReq{Path: f})
	}
	return xml.Marshal(req)
}

// DataField is one cell of a table line.
type DataField struct {
	Path  string `xml:"path,attr"`
	Value string `xml:"value"`
}

// DataLine is one table line; Index is 1-based.
type DataLine struct {
	Index  int         `xml:"index,attr"`
	Fields []DataField `xml:"field"`
}

// DataTable is one page of an internal table. TotalLines is 0 on SAP_BASIS 750.
type DataTable struct {
	Name       string     `xml:"name,attr"`
	TotalLines int        `xml:"totalLines,attr"`
	Lines      []DataLine `xml:"tableLine"`
}

type dataResp struct {
	Tables []DataTable `xml:"table"`
}

// ParseVariableData parses a getVariableData response; nil for an empty one.
func ParseVariableData(data []byte) (*DataTable, error) {
	var r dataResp
	if err := xml.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if len(r.Tables) == 0 {
		return nil, nil
	}
	return &r.Tables[0], nil
}
