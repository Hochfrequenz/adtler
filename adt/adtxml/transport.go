package adtxml

import "encoding/xml"

// TransportRoot is the XML response from GET /sap/bc/adt/cts/transportrequests.
// Real SAP structure: root > workbench > modifiable|released > request
//
//	root > customizing > modifiable|released > request
type TransportRoot struct {
	XMLName     xml.Name       `xml:"root"`
	Workbench   TransportGroup `xml:"workbench"`
	Customizing TransportGroup `xml:"customizing"`
}

// TransportGroup holds modifiable and released transport requests.
type TransportGroup struct {
	Modifiable TransportBucket `xml:"modifiable"`
	Released   TransportBucket `xml:"released"`
}

// TransportBucket holds a list of transport requests.
type TransportBucket struct {
	Requests []TransportRequest `xml:"request"`
}

// TransportRequest is a single transport request in the transport list XML response.
type TransportRequest struct {
	Number      string `xml:"number,attr"`
	Owner       string `xml:"owner,attr"`
	Description string `xml:"desc,attr"`
	Status      string `xml:"status,attr"`
}

// TransportComponent is the XML body for adding an object to a transport.
type TransportComponent struct {
	XMLName   xml.Name `xml:"adtcore:objectReference"`
	NSCore    string   `xml:"xmlns:adtcore,attr"`
	ObjectURI string   `xml:"adtcore:uri,attr"`
}

// TMRoot is the XML body for transport organizer actions (removeobject, etc.).
// Namespace: http://www.sap.com/cts/adt/tm
type TMRoot struct {
	XMLName    xml.Name `xml:"tm:root"`
	NSTM       string   `xml:"xmlns:tm,attr"`
	UserAction string   `xml:"tm:useraction,attr"`
	// Number and Request are omitted when empty. S/4 (SAP_BASIS 816) answers
	// 400 ExceptionInvalidData "Check of condition failed" to a body that
	// carries an empty tm:number: addobjectsfrompackage sends no root number,
	// and addobjectfromrequest no <tm:request> (measured 2026-11-04, #267).
	Number  string     `xml:"tm:number,attr,omitempty"`
	Request *TMRequest `xml:"tm:request,omitempty"`
}

// TMRequest wraps objects in a transport organizer request body.
type TMRequest struct {
	Number  string         `xml:"tm:number,attr,omitempty"`
	Objects []TMAbapObject `xml:"tm:abap_object"`
}

// TMAbapObject identifies an object in a transport request.
type TMAbapObject struct {
	PgmID    string `xml:"tm:pgmid,attr"`
	Type     string `xml:"tm:type,attr"`
	Name     string `xml:"tm:name,attr"`
	WBType   string `xml:"tm:wbtype,attr,omitempty"`
	Position string `xml:"tm:position,attr,omitempty"`
}
