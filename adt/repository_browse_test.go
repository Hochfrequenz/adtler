package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// browsedPackage is the package the test browses, in the upper case ADT uses.
const browsedPackage = "ZPKG_EXAMPLE"

// browsePackageNodesXML is a nodestructure response with two objects of the
// browsed package, one of them a sub-package. The response carries no package
// name per object, which is why BrowsePackage has to supply it.
const browsePackageNodesXML = `<?xml version="1.0" encoding="utf-8"?>
<asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml">
  <asx:values>
    <DATA>
      <TREE_CONTENT>
        <SEU_ADT_REPOSITORY_OBJ_NODE>
          <OBJECT_TYPE>PROG/P</OBJECT_TYPE>
          <OBJECT_NAME>ZPROG_EXAMPLE</OBJECT_NAME>
          <OBJECT_URI>/sap/bc/adt/programs/programs/ZPROG_EXAMPLE</OBJECT_URI>
          <DESCRIPTION>Example program</DESCRIPTION>
        </SEU_ADT_REPOSITORY_OBJ_NODE>
        <SEU_ADT_REPOSITORY_OBJ_NODE>
          <OBJECT_TYPE>DEVC/K</OBJECT_TYPE>
          <OBJECT_NAME>ZPKG_EXAMPLE_SUB</OBJECT_NAME>
          <OBJECT_URI>/sap/bc/adt/packages/zpkg_example_sub</OBJECT_URI>
          <DESCRIPTION>Example sub-package</DESCRIPTION>
        </SEU_ADT_REPOSITORY_OBJ_NODE>
      </TREE_CONTENT>
    </DATA>
  </asx:values>
</asx:abap>`

// TestBrowsePackage_SetsPackageName covers adtler#152: every object a package
// browse returns lives in the browsed package by definition, yet PackageName
// came back empty for all of them, while GetObjectInfo on the same URIs named
// the package. A sub-package is listed in its parent, so its PackageName is the
// browsed package as well.
func TestBrowsePackage_SetsPackageName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(browsePackageNodesXML))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	// ADT reports package names in upper case (adtcore:packageRef), so a
	// caller who typed the name in lower case still gets the spelling
	// GetObjectInfo returns.
	for _, asked := range []string{browsedPackage, "zpkg_example"} {
		results, err := client.BrowsePackage(context.Background(), asked)
		if err != nil {
			t.Fatalf("BrowsePackage(%q): unexpected error: %v", asked, err)
		}
		if len(results) != 2 {
			t.Fatalf("BrowsePackage(%q): got %d results, want 2", asked, len(results))
		}
		for _, obj := range results {
			if obj.PackageName != browsedPackage {
				t.Errorf("BrowsePackage(%q): PackageName of %s = %q, want %q", asked, obj.Name, obj.PackageName, browsedPackage)
			}
		}
	}
}

// TestBrowsePackage_AsksForShortDescriptions covers the description half of
// adtler#152: SAP S/4HANA leaves the description out of a nodestructure
// response unless the request sets withShortDescriptions=true.
func TestBrowsePackage_AsksForShortDescriptions(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		asked = r.URL.Query().Get("withShortDescriptions")
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(browsePackageNodesXML))
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	results, err := client.BrowsePackage(context.Background(), browsedPackage)
	if err != nil {
		t.Fatalf("BrowsePackage: %v", err)
	}
	if asked != "true" {
		t.Errorf("withShortDescriptions = %q, want %q", asked, "true")
	}
	if len(results) != 2 || results[0].Description != "Example program" {
		t.Errorf("descriptions must reach the caller, got %+v", results)
	}
}
