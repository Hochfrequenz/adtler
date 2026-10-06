package adt_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const childVarsResp = `<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>` +
	`<VARIABLES><STPDA_ADT_VARIABLE><ID>LS_ROW</ID><NAME>LS_ROW</NAME><META_TYPE>structure</META_TYPE><VALUE>Structure</VALUE><TABLE_LINES>0</TABLE_LINES><READ_ONLY>X</READ_ONLY></STPDA_ADT_VARIABLE>` +
	`<STPDA_ADT_VARIABLE><ID>LT_ROWS</ID><NAME>LT_ROWS</NAME><META_TYPE>table</META_TYPE><VALUE>[5x3(16)]Standard Table</VALUE><TABLE_LINES>5</TABLE_LINES><IS_VALUE_INCOMPLETE/></STPDA_ADT_VARIABLE></VARIABLES>` +
	`<HIERARCHIES><STPDA_ADT_VARIABLE_HIERARCHY><PARENT_ID>@ROOT</PARENT_ID><CHILD_ID>@LOCALS</CHILD_ID><CHILD_NAME>Locals</CHILD_NAME></STPDA_ADT_VARIABLE_HIERARCHY>` +
	`<STPDA_ADT_VARIABLE_HIERARCHY><PARENT_ID>ME</PARENT_ID><CHILD_ID/></STPDA_ADT_VARIABLE_HIERARCHY></HIERARCHIES>` +
	`</DATA></asx:values></asx:abap>`

const varsResp = `<?xml version="1.0" encoding="utf-8"?><asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>` +
	`<STPDA_ADT_VARIABLE><ID>LV_A</ID><NAME>LV_A</NAME><META_TYPE>simple</META_TYPE><VALUE>1</VALUE><IS_VALUE_INCOMPLETE>X</IS_VALUE_INCOMPLETE></STPDA_ADT_VARIABLE>` +
	`<STPDA_ADT_VARIABLE><ID>LV_B</ID><NAME>LV_B</NAME><META_TYPE>simple</META_TYPE><VALUE>2</VALUE><READ_ONLY>X</READ_ONLY></STPDA_ADT_VARIABLE>` +
	`</DATA></asx:values></asx:abap>`

type asxCapture struct{ ct, accept, stateful, body string }

func asxVarsServer(t *testing.T, method, resp string, got *asxCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		if r.URL.Query().Get("method") == method {
			b, _ := io.ReadAll(r.Body)
			*got = asxCapture{r.Header.Get("Content-Type"), r.Header.Get("Accept"), r.Header.Get("X-sap-adt-sessiontype"), string(b)}
			w.Header().Set("Content-Type", "application/vnd.sap.as+xml")
			_, _ = w.Write([]byte(resp))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
}

func TestGetChildVariables(t *testing.T) {
	var got asxCapture
	srv := asxVarsServer(t, "getChildVariables", childVarsResp, &got)
	defer srv.Close()
	dbg := adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U")
	res, err := dbg.GetChildVariables(context.Background(), "@LOCALS")
	if err != nil {
		t.Fatal(err)
	}
	if got.stateful != wantSessionTypeStateful || got.accept != testAcceptASXML ||
		got.ct != "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.ChildVariables" ||
		!strings.Contains(got.body, "<PARENT_ID>@LOCALS</PARENT_ID>") {
		t.Errorf("request: %+v", got)
	}
	if len(res.Variables) != 2 || res.Variables[1].TableLines != 5 || !res.Variables[0].ReadOnly {
		t.Errorf("variables: %+v", res.Variables)
	}
	if len(res.Links) != 1 || res.Links[0].ChildID != "@LOCALS" {
		t.Errorf("links (empty CHILD_ID dropped): %+v", res.Links)
	}
}

func TestGetVariables(t *testing.T) {
	var got asxCapture
	srv := asxVarsServer(t, "getVariables", varsResp, &got)
	defer srv.Close()
	dbg := adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U")
	vars, err := dbg.GetVariables(context.Background(), "LV_A", "LV_B")
	if err != nil {
		t.Fatal(err)
	}
	if got.stateful != wantSessionTypeStateful || got.accept != testAcceptASXML ||
		got.ct != "application/vnd.sap.as+xml; charset=UTF-8; dataname=com.sap.adt.debugger.Variables" ||
		!strings.Contains(got.body, "LV_A") {
		t.Errorf("request: %+v", got)
	}
	if len(vars) != 2 || vars[0].ID != "LV_A" || !vars[0].ValueIncomplete || vars[0].ReadOnly ||
		vars[1].ID != "LV_B" || !vars[1].ReadOnly || vars[1].ValueIncomplete {
		t.Errorf("variables: %+v", vars)
	}
}

func TestGetTableRows(t *testing.T) {
	var dataCalls int
	var lastData string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			return
		}
		b, _ := io.ReadAll(r.Body)
		switch r.URL.Query().Get("method") {
		case "getVariables":
			var out strings.Builder
			out.WriteString(`<asx:abap xmlns:asx="http://www.sap.com/abapxml" version="1.0"><asx:values><DATA>`)
			if strings.Contains(string(b), "<ID>LT_ROWS</ID>") {
				out.WriteString(`<STPDA_ADT_VARIABLE><ID>LT_ROWS</ID><META_TYPE>table</META_TYPE><TABLE_LINES>5</TABLE_LINES></STPDA_ADT_VARIABLE>`)
			}
			if strings.Contains(string(b), "<ID>LS_ROW</ID>") {
				out.WriteString(`<STPDA_ADT_VARIABLE><ID>LS_ROW</ID><META_TYPE>structure</META_TYPE></STPDA_ADT_VARIABLE>`)
			}
			out.WriteString(`</DATA></asx:values></asx:abap>`)
			_, _ = w.Write([]byte(out.String()))
		case "getVariableData":
			dataCalls++
			lastData = string(b)
			_, _ = w.Write([]byte(`<dbg:data xmlns:dbg="http://www.sap.com/adt/debugger"><table name="LT_ROWS">` +
				`<tableLine index="4"><field path="TEXT"><value>r4</value></field></tableLine>` +
				`<tableLine index="5"><field path="TEXT"><value>r5</value></field></tableLine></table></dbg:data>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	dbg := adt.NewDebugSession(adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}), "U")
	ctx := context.Background()

	page, err := dbg.GetTableRows(ctx, "LT_ROWS", 4, 10, "TEXT")
	if err != nil || !strings.Contains(lastData, `offset="4" length="2"`) || page.TotalLines != 5 || len(page.Rows) != 2 || page.Rows[1].Fields[0].Value != "r5" {
		t.Errorf("clamp: page=%+v err=%v body=%s", page, err, lastData)
	}
	before := dataCalls
	page, err = dbg.GetTableRows(ctx, "LT_ROWS", 6, 3)
	if err != nil || len(page.Rows) != 0 || page.TotalLines != 5 || dataCalls != before {
		t.Errorf("beyond end: page=%+v err=%v calls=%d", page, err, dataCalls-before)
	}
	if _, err := dbg.GetTableRows(ctx, "LT_ROWS", 0, 3); err == nil || !strings.Contains(err.Error(), "offset") {
		t.Errorf("offset 0: %v", err)
	}
	if _, err := dbg.GetTableRows(ctx, "LT_ROWS", 1, 0); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("limit 0: %v", err)
	}
	if _, err := dbg.GetTableRows(ctx, "LS_ROW", 1, 3); !errors.Is(err, adt.ErrNotATable) {
		t.Errorf("structure: %v", err)
	}
	if _, err := dbg.GetTableRows(ctx, "NOPE", 1, 3); err == nil || !strings.Contains(err.Error(), "unknown variable") {
		t.Errorf("unknown: %v", err)
	}
}
