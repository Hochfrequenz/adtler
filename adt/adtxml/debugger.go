package adtxml

import "encoding/xml"

// XML types for ABAP debugger ADT REST endpoints.

// BreakpointsRequest is the XML body for POST /sap/bc/adt/debugger/breakpoints.
// Verified: 2026-03-24 against an SAP ERP 6.0 EHP8 system.
// Derived from Simple Transformation TPDA_ADT_BREAKPOINTS_REQUEST.
//
// SyncMode is serialized as a syncMode attribute, which that transformation
// does not read: it takes the sync mode from a <syncScope mode="…"> child
// element and ignores unknown attributes (tt:extensible="deep"). Read on
// 2026-10-06 on SAP_BASIS 750 and 816, identical on both. The field is kept
// for compatibility with existing callers only; it has no effect on SAP.
type BreakpointsRequest struct {
	XMLName       xml.Name            `xml:"dbg:breakpoints"`
	NSDebug       string              `xml:"xmlns:dbg,attr"`
	NSCore        string              `xml:"xmlns:adtcore,attr"`
	Scope         string              `xml:"scope,attr,omitempty"`
	DebuggingMode string              `xml:"debuggingMode,attr,omitempty"`
	RequestUser   string              `xml:"requestUser,attr,omitempty"`
	TerminalID    string              `xml:"terminalId,attr,omitempty"`
	IdeID         string              `xml:"ideId,attr,omitempty"`
	SyncMode      string              `xml:"syncMode,attr,omitempty"`
	Breakpoints   []BreakpointRequest `xml:"breakpoint"`
}

// BreakpointRequest is a single breakpoint in a set-breakpoint request.
// Uses custom MarshalXML to produce adtcore:-prefixed attributes that
// Go's encoding/xml cannot generate from struct tags alone.
type BreakpointRequest struct {
	Kind     string // line, statement, etc.
	ClientID string // clientId, echoed back on the matching response entry
	URI      string // adtcore:uri with #start=line,col
	Type     string // adtcore:type e.g. PROG/P
	Name     string // adtcore:name e.g. ZREPORT
}

// MarshalXML writes a <breakpoint> element with adtcore:-prefixed attributes.
// ClientID, Type and Name are omitted when empty: the request transformation
// reads all three as optional (tt:cond), and the resource derives program,
// include and line from adtcore:uri alone.
func (b BreakpointRequest) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	start.Name = xml.Name{Local: "breakpoint"}
	start.Attr = []xml.Attr{{Name: xml.Name{Local: "kind"}, Value: b.Kind}}
	if b.ClientID != "" {
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "clientId"}, Value: b.ClientID})
	}
	start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "adtcore:uri"}, Value: b.URI})
	if b.Type != "" {
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "adtcore:type"}, Value: b.Type})
	}
	if b.Name != "" {
		start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: "adtcore:name"}, Value: b.Name})
	}
	e.EncodeToken(start)       //nolint:errcheck
	e.EncodeToken(start.End()) //nolint:errcheck
	return nil
}

// BreakpointsResponse is the XML response from POST /sap/bc/adt/debugger/breakpoints.
type BreakpointsResponse struct {
	XMLName     xml.Name             `xml:"breakpoints"`
	Breakpoints []BreakpointResponse `xml:"breakpoint"`
}

// BreakpointResponse is a single breakpoint in a response.
type BreakpointResponse struct {
	Kind           string `xml:"kind,attr"`
	ClientID       string `xml:"clientId,attr"`
	ID             string `xml:"id,attr"`
	ErrorKind      string `xml:"errorKind,attr"`
	ErrorMessage   string `xml:"errorMessage,attr"`
	URI            string `xml:"uri,attr"`
	Type           string `xml:"type,attr"`
	Name           string `xml:"name,attr"`
	NonAbapFlavour string `xml:"nonAbapFlavour,attr"`
}
