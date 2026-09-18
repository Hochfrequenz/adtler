package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// oneColumnDataPreview builds a single-column datapreview response.
func oneColumnDataPreview(name string, values ...string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>
<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">
  <dataPreview:totalRows>` + strconv.Itoa(len(values)) + `</dataPreview:totalRows>
  <dataPreview:columns>
    <dataPreview:metadata dataPreview:name="` + name + `" dataPreview:type="C" dataPreview:description="" dataPreview:keyAttribute="true" dataPreview:colType="" dataPreview:isKeyFigure="false"/>
    <dataPreview:dataSet>`)
	for _, v := range values {
		b.WriteString("<dataPreview:data>" + v + "</dataPreview:data>")
	}
	b.WriteString(`</dataPreview:dataSet>
  </dataPreview:columns>
</dataPreview:tableData>`)
	return b.String()
}

// TestGetObjectDependencies_Prog exercises the PROG path end to end: the
// D010TAB master query returns one table name, which DD02L then classifies as a
// transparent table. The mock routes by inspecting the SQL in the request body.
func TestGetObjectDependencies_Prog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		sql := string(bodyBytes)
		w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
		switch {
		case strings.Contains(sql, "FROM D010TAB"):
			_, _ = w.Write([]byte(oneColumnDataPreview("TABNAME", "ZORDERS")))
		case strings.Contains(sql, "FROM DD02L"):
			// Two columns: TABNAME, TABCLASS.
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">
  <dataPreview:totalRows>1</dataPreview:totalRows>
  <dataPreview:columns>
    <dataPreview:metadata dataPreview:name="TABNAME" dataPreview:type="C" dataPreview:keyAttribute="true" dataPreview:colType="" dataPreview:isKeyFigure="false"/>
    <dataPreview:dataSet><dataPreview:data>ZORDERS</dataPreview:data></dataPreview:dataSet>
  </dataPreview:columns>
  <dataPreview:columns>
    <dataPreview:metadata dataPreview:name="TABCLASS" dataPreview:type="C" dataPreview:keyAttribute="false" dataPreview:colType="" dataPreview:isKeyFigure="false"/>
    <dataPreview:dataSet><dataPreview:data>TRANSP</dataPreview:data></dataPreview:dataSet>
  </dataPreview:columns>
</dataPreview:tableData>`))
		default:
			_, _ = w.Write([]byte(oneColumnDataPreview("X")))
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	res, err := client.GetObjectDependencies(context.Background(), "prog", "Z_MY_REPORT", 200, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ObjectType != "PROG" || res.ObjectName != "Z_MY_REPORT" {
		t.Errorf("header: got %+v", res)
	}
	if res.Count != 1 || len(res.Dependencies) != 1 {
		t.Fatalf("expected 1 dependency, got %d: %+v", res.Count, res.Dependencies)
	}
	dep := res.Dependencies[0]
	if dep.Name != "ZORDERS" || dep.UseType != adt.UseTypeTable {
		t.Errorf("dependency: got %+v, want {ZORDERS TABLE}", dep)
	}
}

// TestGetObjectDependencies_UIAC exercises the UIAC path: the catalog query
// returns two app ids, reported as UI_APP dependencies. SUI_TM_MM_CAT must
// never be queried — that table is absent on ECC systems.
func TestGetObjectDependencies_UIAC(t *testing.T) {
	var sawCatTable bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		bodyBytes, _ := io.ReadAll(r.Body)
		sql := string(bodyBytes)
		if strings.Contains(sql, "SUI_TM_MM_CAT") {
			sawCatTable = true
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
		if strings.Contains(sql, "FROM SUI_TM_MM_APP") {
			_, _ = w.Write([]byte(oneColumnDataPreview("APP_ID", "APPONE", "APPTWO")))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	c := adt.NewClient(cfg)
	res, err := c.GetObjectDependencies(context.Background(), "UIAC", "SAP_TC_EXAMPLE", 200, 3)
	if err != nil {
		t.Fatalf("GetObjectDependencies: %v", err)
	}
	if sawCatTable {
		t.Error("SUI_TM_MM_CAT was queried; it does not exist on ECC systems")
	}
	if res.Count != 2 {
		t.Fatalf("count: got %d, want 2", res.Count)
	}
	if res.Dependencies[0].Name != "APPONE" || res.Dependencies[0].UseType != adt.UseTypeUIApp {
		t.Errorf("dep[0]: got %+v, want {APPONE UI_APP}", res.Dependencies[0])
	}
	if res.Dependencies[1].Name != "APPTWO" {
		t.Errorf("dep[1] name: got %q, want APPTWO", res.Dependencies[1].Name)
	}
}
