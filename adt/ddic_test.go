package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// emptyDataPreviewXML is a minimal-but-valid datapreview response with zero
// columns, mirroring what /sap/bc/adt/datapreview/freestyle returns when the
// SELECT yields no rows. Adapted from adtxml/datapreview_test.go's empty
// fixture.
const emptyDataPreviewXML = `<?xml version="1.0" encoding="utf-8"?>
<dataPreview:tableData xmlns:dataPreview="http://www.sap.com/adt/dataPreview">
  <dataPreview:totalRows>0</dataPreview:totalRows>
  <dataPreview:isHanaAnalyticalView>false</dataPreview:isHanaAnalyticalView>
  <dataPreview:executedQueryString>SELECT FIELDNAME FROM DD03L WHERE TABNAME = 'DOES_NOT_EXIST'</dataPreview:executedQueryString>
  <dataPreview:queryExecutionTime>0.001</dataPreview:queryExecutionTime>
</dataPreview:tableData>`

// TestGetTableFields_NotFound regression-tests adtler#10:
// Before the fix, GetTableFields returned (nil, nil) for a non-existent
// table because the loop never appended anything and the function returned
// the zero-valued slice with no error. Callers couldn't distinguish "table
// missing" from "table exists but is somehow empty" (which is impossible
// for a real DDIC table). After the fix, the function returns a typed
// *TableNotFoundError that callers can check with errors.As.
func TestGetTableFields_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/sap/bc/adt/datapreview/freestyle") {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(emptyDataPreviewXML))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	fields, err := client.GetTableFields(context.Background(), "DOES_NOT_EXIST")
	if err == nil {
		t.Fatalf("expected error for missing table, got fields=%v", fields)
	}
	if fields != nil {
		t.Errorf("fields should be nil on error, got %v", fields)
	}

	var notFound *adt.TableNotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected *TableNotFoundError, got %T: %v", err, err)
	}
	if notFound.TableName != "DOES_NOT_EXIST" {
		t.Errorf("TableName: got %q", notFound.TableName)
	}
	if !strings.Contains(notFound.Error(), "DOES_NOT_EXIST") {
		t.Errorf("error should mention table name, got: %v", notFound)
	}
}

// TestGetTableFields_EmptyName checks the existing input-validation guard
// is unchanged by the not-found refactor.
func TestGetTableFields_EmptyName(t *testing.T) {
	cfg := sapmcpconfig.SAPSystem{Host: "http://localhost", User: "U", Password: "P"}
	client := adt.NewClient(cfg)

	_, err := client.GetTableFields(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty table name")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("error should mention 'must not be empty', got: %v", err)
	}

	// Whitespace-only also rejected (existing TrimSpace behavior).
	_, err = client.GetTableFields(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only table name")
	}
}
