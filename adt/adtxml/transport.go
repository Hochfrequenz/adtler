package adtxml

import "encoding/xml"

// TransportRoot is the XML response from GET /sap/bc/adt/cts/transportrequests.
//
// SAP systems return one of two formats depending on the NetWeaver version:
//
// Legacy (older NW releases):
//
//	<root>
//	  <workbench><modifiable><request number="..." desc="..." .../></modifiable></workbench>
//	  <customizing><modifiable><request .../></modifiable></customizing>
//	</root>
//
// Modern (S/4HANA and current NW releases):
//
//	<root>
//	  <workbenchRequests><workbenchRequest number="..." shortDescription="..." .../></workbenchRequests>
//	  <customizingRequests><customizingRequest .../></customizingRequests>
//	</root>
//
// TransportRoot captures both shapes. Callers should merge all non-empty slices.
type TransportRoot struct {
	XMLName xml.Name `xml:"root"`

	// Legacy format (older NW).
	Workbench   TransportGroup `xml:"workbench"`
	Customizing TransportGroup `xml:"customizing"`

	// Modern format (S/4HANA / current NW).
	WorkbenchRequests   []ModernTransportRequest `xml:"workbenchRequests>workbenchRequest"`
	CustomizingRequests []ModernTransportRequest `xml:"customizingRequests>customizingRequest"`
}

// TransportGroup holds modifiable and released transport requests (legacy format).
type TransportGroup struct {
	Modifiable TransportBucket `xml:"modifiable"`
	Released   TransportBucket `xml:"released"`
}

// TransportBucket holds a list of transport requests (legacy format).
type TransportBucket struct {
	Requests []TransportRequest `xml:"request"`
}

// TransportRequest is a single transport request in the legacy XML format.
// The description attribute is "desc" in legacy responses.
type TransportRequest struct {
	Number      string `xml:"number,attr"`
	Owner       string `xml:"owner,attr"`
	Description string `xml:"desc,attr"`
	Status      string `xml:"status,attr"`
}

// ModernTransportRequest is a single transport request in the modern S/4HANA XML format.
// The description attribute is "shortDescription" instead of "desc".
type ModernTransportRequest struct {
	Number      string `xml:"number,attr"`
	Owner       string `xml:"owner,attr"`
	Description string `xml:"shortDescription,attr"`
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
	XMLName    xml.Name  `xml:"tm:root"`
	NSTM       string    `xml:"xmlns:tm,attr"`
	UserAction string    `xml:"tm:useraction,attr"`
	Number     string    `xml:"tm:number,attr"`
	Request    TMRequest `xml:"tm:request"`
}

// TMRequest wraps objects in a transport organizer request body.
type TMRequest struct {
	Number  string         `xml:"tm:number,attr"`
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
