package adtxml

import "encoding/xml"

const nsCodeCompletion = "http://www.sap.com/adt/codecompletion"

// Completions is the XML response from the code completion endpoint.
// SAP uses the codecompletion namespace for both elements and attributes:
//
//	<codecompletion:completions xmlns:codecompletion="http://www.sap.com/adt/codecompletion">
//	  <codecompletion:completion codecompletion:text="METHOD" codecompletion:description="ABAP Keyword"/>
//	</codecompletion:completions>
type Completions struct {
	XMLName xml.Name     `xml:"http://www.sap.com/adt/codecompletion completions"`
	Items   []Completion `xml:"http://www.sap.com/adt/codecompletion completion"`
}

// Completion is a single code completion proposal in XML.
type Completion struct {
	Text        string `xml:"http://www.sap.com/adt/codecompletion text,attr"`
	Description string `xml:"http://www.sap.com/adt/codecompletion description,attr"`
}
