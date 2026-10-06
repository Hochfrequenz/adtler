package adtxml_test

import (
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt/adtxml"
)

func TestChildVariablesRequest_EscapesAndWraps(t *testing.T) {
	b, err := adtxml.ChildVariablesRequest([]string{"@LOCALS", "LO->ATTR", `{O:19*\PROGRAM=Z\CLASS=LCL}-MV`})
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{
		`<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0">`,
		`<PARENT_ID>@LOCALS</PARENT_ID>`,
		`<PARENT_ID>LO-&gt;ATTR</PARENT_ID>`,
		`<PARENT_ID>{O:19*\PROGRAM=Z\CLASS=LCL}-MV</PARENT_ID>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
}

const childVarsResp = `<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>` +
	`<VARIABLES><STPDA_ADT_VARIABLE><ID>LS_ROW</ID><NAME>LS_ROW</NAME><META_TYPE>structure</META_TYPE><VALUE>Structure</VALUE><TABLE_LINES>0</TABLE_LINES><READ_ONLY>X</READ_ONLY></STPDA_ADT_VARIABLE>` +
	`<STPDA_ADT_VARIABLE><ID>LT_ROWS</ID><NAME>LT_ROWS</NAME><META_TYPE>table</META_TYPE><VALUE>[5x3(16)]Standard Table</VALUE><TABLE_LINES>5</TABLE_LINES><IS_VALUE_INCOMPLETE/></STPDA_ADT_VARIABLE></VARIABLES>` +
	`<HIERARCHIES><STPDA_ADT_VARIABLE_HIERARCHY><PARENT_ID>@ROOT</PARENT_ID><CHILD_ID>@LOCALS</CHILD_ID><CHILD_NAME>Locals</CHILD_NAME></STPDA_ADT_VARIABLE_HIERARCHY>` +
	`<STPDA_ADT_VARIABLE_HIERARCHY><PARENT_ID>ME</PARENT_ID><CHILD_ID/></STPDA_ADT_VARIABLE_HIERARCHY></HIERARCHIES>` +
	`</DATA></asx:values></asx:abap>`

func TestParseChildVariables(t *testing.T) {
	vars, links, err := adtxml.ParseChildVariables([]byte(childVarsResp))
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 2 || vars[1].ID != "LT_ROWS" || vars[1].MetaType != "table" || vars[1].TableLines != 5 || vars[0].ReadOnly != "X" {
		t.Errorf("vars: %+v", vars)
	}
	if len(links) != 2 || links[0].ChildName != "Locals" || links[1].ChildID != "" {
		t.Errorf("links: %+v", links)
	}
}

func TestVariableDataRequestAndParse(t *testing.T) {
	b, err := adtxml.VariableDataRequest("LT_ROWS", 2, 2, []string{"TEXT"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `<table name="LT_ROWS" offset="2" length="2"><field path="TEXT"></field></table>`) &&
		!strings.Contains(string(b), `<table name="LT_ROWS" offset="2" length="2"><field path="TEXT"/></table>`) {
		t.Errorf("request: %s", b)
	}
	tbl, err := adtxml.ParseVariableData([]byte(`<dbg:data xmlns:dbg="http://www.sap.com/adt/debugger"><table name="LT_ROWS" totalLines="5">` +
		`<tableLine index="2"><field path="TEXT"><value>row 2</value></field></tableLine>` +
		`<tableLine index="3"><field path="TEXT"><value>row 3</value></field></tableLine></table></dbg:data>`))
	if err != nil {
		t.Fatal(err)
	}
	if tbl == nil || tbl.TotalLines != 5 || len(tbl.Lines) != 2 || tbl.Lines[1].Index != 3 || tbl.Lines[1].Fields[0].Value != "row 3" {
		t.Errorf("table: %+v", tbl)
	}
	empty, err := adtxml.ParseVariableData([]byte(`<dbg:data xmlns:dbg="http://www.sap.com/adt/debugger"/>`))
	if err != nil || empty != nil {
		t.Errorf("empty data: %v %v", empty, err)
	}
}
