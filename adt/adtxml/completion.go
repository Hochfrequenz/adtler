package adtxml

import "encoding/xml"

// Completions is the asXML response from the code completion endpoint
// (Accept: application/vnd.sap.as+xml). The SAP handler serializes
// the SCC_ADT_COMPLETION_RESULTS table as a raw asXML envelope:
//
//	<asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml">
//	  <asx:values>
//	    <DATA>
//	      <SCC_COMPLETION>
//	        <KIND>52</KIND>
//	        <IDENTIFIER>METHOD</IDENTIFIER>
//	        ...
//	      </SCC_COMPLETION>
//	    </DATA>
//	  </asx:values>
//	</asx:abap>
//
// asXML carries no description attribute — only the identifier and
// numeric metadata (kind, role, location, grade). The description is
// surfaced through a separate elementinfo endpoint, not here.
type Completions struct {
	XMLName xml.Name     `xml:"http://www.sap.com/abapxml abap"`
	Items   []Completion `xml:"values>DATA>SCC_COMPLETION"`
}

// Completion is a single code completion proposal in asXML form.
type Completion struct {
	Identifier string `xml:"IDENTIFIER"`
}
