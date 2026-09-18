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

// multiColumnDataPreview builds a one-row datapreview response over the given
// column/value pairs, in the order given.
func multiColumnDataPreview(cols [][2]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n" +
		`<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">` + "\n" +
		`  <dataPreview:totalRows>1</dataPreview:totalRows>`)
	for _, c := range cols {
		b.WriteString("\n  <dataPreview:columns>\n" +
			`    <dataPreview:metadata dataPreview:name="` + c[0] + `" dataPreview:type="C" dataPreview:keyAttribute="false" dataPreview:colType="" dataPreview:isKeyFigure="false"/>` + "\n" +
			`    <dataPreview:dataSet><dataPreview:data>` + c[1] + `</dataPreview:data></dataPreview:dataSet>` + "\n" +
			"  </dataPreview:columns>")
	}
	b.WriteString("\n</dataPreview:tableData>")
	return b.String()
}

// TestGetObjectDependencies_UIAD_MultipleTargets covers a Web Dynpro app entry,
// which references both a Web Dynpro application and a transaction, so both are
// reported. The response deliberately lists its columns in an order matching
// neither the SELECT list nor uiadTargetColumns: the data-preview endpoint may
// reorder columns, so the implementation has to address them by name.
// Positional indexing passes a same-order fixture and fails this one.
func TestGetObjectDependencies_UIAD_MultipleTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
		_, _ = w.Write([]byte(multiColumnDataPreview([][2]string{
			{"UI5_APP_ID", ""},
			{"APP_TYPE", "W"},
			{"WD_APPL_ID", "WDA_EXAMPLE"},
			{"TCODE", "SE38"},
			{"WCF_TARGET_ID", ""},
		})))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	c := adt.NewClient(cfg)
	res, err := c.GetObjectDependencies(context.Background(), "UIAD", "APPID00000000000000000000000001", 200, 3)
	if err != nil {
		t.Fatalf("GetObjectDependencies: %v", err)
	}
	if res.Count != 2 {
		t.Fatalf("count: got %d, want 2 (%+v)", res.Count, res.Dependencies)
	}
	if res.Dependencies[0].Name != "SE38" || res.Dependencies[0].UseType != adt.UseTypeTransaction {
		t.Errorf("dep[0]: got %+v, want {SE38 TRANSACTION}", res.Dependencies[0])
	}
	if res.Dependencies[1].Name != "WDA_EXAMPLE" || res.Dependencies[1].UseType != adt.UseTypeWebDynproApp {
		t.Errorf("dep[1]: got %+v, want {WDA_EXAMPLE WEB_DYNPRO_APP}", res.Dependencies[1])
	}
}

// TestGetObjectDependencies_UIAD_NoTarget covers a URL app entry: it exists but
// launches no repository object, reported as an empty list plus a warning
// rather than as an error.
func TestGetObjectDependencies_UIAD_NoTarget(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
		_, _ = w.Write([]byte(multiColumnDataPreview([][2]string{
			{"APP_TYPE", "R"},
			{"TCODE", ""},
			{"WD_APPL_ID", ""},
			{"WCF_TARGET_ID", ""},
			{"UI5_APP_ID", ""},
		})))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	c := adt.NewClient(cfg)
	res, err := c.GetObjectDependencies(context.Background(), "UIAD", "APPID00000000000000000000000001", 200, 3)
	if err != nil {
		t.Fatalf("GetObjectDependencies: %v", err)
	}
	if res.Count != 0 {
		t.Fatalf("count: got %d, want 0 (%+v)", res.Count, res.Dependencies)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no launch target") {
		t.Errorf("warnings: got %v, want one mentioning \"no launch target\"", res.Warnings)
	}
}

// TestGetObjectDependencies_UIAD_NoEntry covers an app id with no row at all in
// SUI_TM_MM_APP (as opposed to TestGetObjectDependencies_UIAD_NoTarget, where the
// row exists but every target column is empty): reported as an empty list plus
// a warning that the app id is unknown, rather than as an error.
func TestGetObjectDependencies_UIAD_NoEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
		// Zero values means zero rows: the query ran but matched nothing.
		_, _ = w.Write([]byte(oneColumnDataPreview("APP_TYPE")))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	c := adt.NewClient(cfg)
	res, err := c.GetObjectDependencies(context.Background(), "UIAD", "APPID00000000000000000000000099", 200, 3)
	if err != nil {
		t.Fatalf("GetObjectDependencies: %v", err)
	}
	if res.Count != 0 {
		t.Fatalf("count: got %d, want 0 (%+v)", res.Count, res.Dependencies)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no SUI_TM_MM_APP entry") {
		t.Errorf("warnings: got %v, want one mentioning \"no SUI_TM_MM_APP entry\"", res.Warnings)
	}
}
