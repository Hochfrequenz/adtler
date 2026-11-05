package adtxml

import "encoding/xml"

// ActivationRequest is the XML body for POST /sap/bc/adt/activation.
type ActivationRequest struct {
	XMLName xml.Name `xml:"adtcore:objectReferences"`
	NS      string   `xml:"xmlns:adtcore,attr"`
	Objects []ActivationObject
}

// ActivationObject is a single object reference in an activation request.
type ActivationObject struct {
	XMLName   xml.Name `xml:"adtcore:objectReference"`
	URI       string   `xml:"adtcore:uri,attr"`
	Type      string   `xml:"adtcore:type,attr,omitempty"`
	Name      string   `xml:"adtcore:name,attr,omitempty"`
	ParentURI string   `xml:"adtcore:parentUri,attr,omitempty"`
}

// ActivationMessages is the response from POST /sap/bc/adt/activation.
// SAP returns <chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist">
// with <msg> children when there are errors/warnings.
// Verified: 2026-03-24 against an SAP ERP 6.0 EHP8 system
type ActivationMessages struct {
	XMLName  xml.Name            `xml:"messages"`
	Messages []ActivationMessage `xml:"msg"`
}

// ActivationMessage is a single message in an activation response.
// Verified: 2026-03-24 against an SAP ERP 6.0 EHP8 system
type ActivationMessage struct {
	ObjDescr       string `xml:"objDescr,attr"`
	Type           string `xml:"type,attr"`
	Line           string `xml:"line,attr"`
	Href           string `xml:"href,attr"`
	ForceSupported string `xml:"forceSupported,attr"`
	ShortText      struct {
		Text string `xml:"txt"`
	} `xml:"shortText"`
}

// ActivationPreaudit is the pre-audit answer of POST /sap/bc/adt/activation
// with preauditRequested=true: instead of activating, SAP lists the inactive
// objects that belong to the request, each with the transport it is recorded
// in. The caller sends the listed objects back in a second activation
// request. Entries whose object is empty carry only a transport.
// Verified: 2026-11-05 against an SAP ERP 6.0 EHP8 system (SAP_BASIS 750)
type ActivationPreaudit struct {
	XMLName xml.Name                  `xml:"http://www.sap.com/abapxml/inactiveCtsObjects inactiveObjects"`
	Entries []ActivationPreauditEntry `xml:"http://www.sap.com/abapxml/inactiveCtsObjects entry"`
}

// ActivationPreauditEntry is one entry of an ActivationPreaudit.
type ActivationPreauditEntry struct {
	Object struct {
		Ref *ActivationPreauditRef `xml:"http://www.sap.com/abapxml/inactiveCtsObjects ref"`
	} `xml:"http://www.sap.com/abapxml/inactiveCtsObjects object"`
}

// ActivationPreauditRef is the object reference of an ActivationPreauditEntry.
type ActivationPreauditRef struct {
	URI       string `xml:"http://www.sap.com/adt/core uri,attr"`
	Type      string `xml:"http://www.sap.com/adt/core type,attr"`
	Name      string `xml:"http://www.sap.com/adt/core name,attr"`
	ParentURI string `xml:"http://www.sap.com/adt/core parentUri,attr"`
}
