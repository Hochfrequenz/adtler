package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

func TestBrowsePackage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path != "/sap/bc/adt/repository/nodestructure" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: got %q, want POST", r.Method)
		}
		accept := r.Header.Get("Accept")
		if accept != "application/vnd.sap.as+xml" {
			t.Errorf("Accept header: got %q", accept)
		}
		q := r.URL.Query()
		if q.Get("parent_name") != "STUN" {
			t.Errorf("parent_name: got %q", q.Get("parent_name"))
		}
		w.Header().Set("Content-Type", "application/vnd.sap.as+xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<asx:abap version="1.0" xmlns:asx="http://www.sap.com/abapxml">
  <asx:values>
    <DATA>
      <TREE_CONTENT>
        <SEU_ADT_REPOSITORY_OBJ_NODE>
          <OBJECT_TYPE>PROG/P</OBJECT_TYPE>
          <OBJECT_NAME>RSPARAM</OBJECT_NAME>
          <TECH_NAME>RSPARAM</TECH_NAME>
          <OBJECT_URI>/sap/bc/adt/programs/programs/RSPARAM</OBJECT_URI>
          <DESCRIPTION>Display SAP Profile Parameters</DESCRIPTION>
        </SEU_ADT_REPOSITORY_OBJ_NODE>
        <SEU_ADT_REPOSITORY_OBJ_NODE>
          <OBJECT_TYPE>DEVC/K</OBJECT_TYPE>
          <OBJECT_NAME>STUN_COMMON</OBJECT_NAME>
          <TECH_NAME>STUN_COMMON</TECH_NAME>
          <OBJECT_URI>/sap/bc/adt/packages/stun_common</OBJECT_URI>
          <DESCRIPTION>Common Monitoring</DESCRIPTION>
        </SEU_ADT_REPOSITORY_OBJ_NODE>
      </TREE_CONTENT>
    </DATA>
  </asx:values>
</asx:abap>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	results, err := client.BrowsePackage(context.Background(), "STUN")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Name != "RSPARAM" {
		t.Errorf("name[0]: got %q", results[0].Name)
	}
	if results[0].URI != "/sap/bc/adt/programs/programs/RSPARAM" {
		t.Errorf("uri[0]: got %q", results[0].URI)
	}
	if results[0].Type != progType {
		t.Errorf("type[0]: got %q", results[0].Type)
	}
	if results[0].Description != "Display SAP Profile Parameters" {
		t.Errorf("description[0]: got %q", results[0].Description)
	}
}

func TestGetObjectInfoProgram(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sap/bc/adt/programs/programs/RSPARAM" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "application/vnd.sap.adt.programs.programs") {
			t.Errorf("Accept header missing program type: %q", accept)
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.programs.programs.v2+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<program:abapProgram
  adtcore:name="RSPARAM" adtcore:type="PROG/P"
  adtcore:description="Display SAP Profile Parameters"
  xmlns:program="http://www.sap.com/adt/programs/programs"
  xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:uri="/sap/bc/adt/packages/stun" adtcore:name="STUN"/>
</program:abapProgram>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	info, err := client.GetObjectInfo(context.Background(), "/sap/bc/adt/programs/programs/RSPARAM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "RSPARAM" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.Type != progType {
		t.Errorf("type: got %q", info.Type)
	}
	if info.Description != "Display SAP Profile Parameters" {
		t.Errorf("description: got %q", info.Description)
	}
	if info.PackageName != "STUN" {
		t.Errorf("packageName: got %q", info.PackageName)
	}
}

func TestGetObjectInfoClass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sap/bc/adt/oo/classes/ZCL_EXAMPLE" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		accept := r.Header.Get("Accept")
		if !strings.Contains(accept, "application/vnd.sap.adt.oo.classes") {
			t.Errorf("Accept header missing class type: %q", accept)
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.oo.classes.v4+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<class:abapClass adtcore:name="ZCL_EXAMPLE" adtcore:type="CLAS/OC"
  adtcore:description="Example Class"
  xmlns:class="http://www.sap.com/adt/oo/classes"
  xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:uri="/sap/bc/adt/packages/ztest" adtcore:name="ZTEST"/>
</class:abapClass>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	info, err := client.GetObjectInfo(context.Background(), "/sap/bc/adt/oo/classes/ZCL_EXAMPLE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "ZCL_EXAMPLE" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.Type != "CLAS/OC" {
		t.Errorf("type: got %q", info.Type)
	}
	if info.PackageName != "ZTEST" {
		t.Errorf("packageName: got %q", info.PackageName)
	}
}

func TestGetObjectInfoVIT(t *testing.T) {
	tests := []struct {
		name      string
		uri       string
		wantType  string
		objectXML string
	}{
		{
			name:     "UIAC (UI annotation component)",
			uri:      "/sap/bc/adt/vit/wb/object_type/uiac/object_name/%2fHFQ%2fTC_EXT",
			wantType: "UIAC",
			objectXML: `<?xml version="1.0" encoding="utf-8"?>
<wb:objectProperties adtcore:name="/HFQ/TC_EXT" adtcore:type="UIAC"
  adtcore:description="HFQ UI Annotation Component"
  xmlns:wb="http://www.sap.com/adt/vit/wb"
  xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:name="/HFQ/MAIN"/>
</wb:objectProperties>`,
		},
		{
			name:     "UIAD (UI annotation definition)",
			uri:      "/sap/bc/adt/vit/wb/object_type/uiad/object_name/%2fHFQ%2f95A365FBC361529D",
			wantType: "UIAD",
			objectXML: `<?xml version="1.0" encoding="utf-8"?>
<wb:objectProperties adtcore:name="/HFQ/95A365FBC361529D" adtcore:type="UIAD"
  adtcore:description="HFQ UI Annotation Definition"
  xmlns:wb="http://www.sap.com/adt/vit/wb"
  xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:name="/HFQ/MAIN"/>
</wb:objectProperties>`,
		},
		{
			name:     "ADVC (advclrp URI subtype)",
			uri:      "/sap/bc/adt/vit/wb/object_type/advclrp/object_name/%2fHFQ%2fIWGBLTCDZFMCAZLC5IN3LHTJZY",
			wantType: "ADVC",
			objectXML: `<?xml version="1.0" encoding="utf-8"?>
<wb:objectProperties adtcore:name="/HFQ/IWGBLTCDZFMCAZLC5IN3LHTJZY" adtcore:type="ADVC"
  adtcore:description="HFQ ADVC Object"
  xmlns:wb="http://www.sap.com/adt/vit/wb"
  xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:name="/HFQ/MAIN"/>
</wb:objectProperties>`,
		},
		{
			name:     "WDCC (web Dynpro component configuration)",
			uri:      "/sap/bc/adt/vit/wb/object_type/wdcc/object_name/%2fHFQ%2fBB7F8A229259B8B8C03A1EEC4C307",
			wantType: "WDCC",
			objectXML: `<?xml version="1.0" encoding="utf-8"?>
<wb:objectProperties adtcore:name="/HFQ/BB7F8A229259B8B8C03A1EEC4C307" adtcore:type="WDCC"
  adtcore:description="HFQ Web Dynpro Component Configuration"
  xmlns:wb="http://www.sap.com/adt/vit/wb"
  xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:name="/HFQ/MAIN"/>
</wb:objectProperties>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objectXML := tt.objectXML
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == csrfEndpoint {
					w.Header().Set("X-CSRF-Token", "token")
					w.WriteHeader(http.StatusOK)
					return
				}
				accept := r.Header.Get("Accept")
				if !strings.Contains(accept, "application/vnd.sap.adt.basic.object.properties+xml") {
					w.WriteHeader(http.StatusNotAcceptable)
					return
				}
				w.Header().Set("Content-Type", "application/vnd.sap.adt.basic.object.properties+xml")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(objectXML))
			}))
			defer srv.Close()

			cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
			client := adt.NewClient(cfg)

			info, err := client.GetObjectInfo(context.Background(), tt.uri)
			if err != nil {
				t.Fatalf("GetObjectInfo(%s) unexpected error: %v", tt.uri, err)
			}
			if info.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", info.Type, tt.wantType)
			}
			if info.Name == "" {
				t.Error("Name is empty")
			}
			if info.PackageName == "" {
				t.Error("PackageName is empty")
			}
		})
	}
}

func TestGetObjectInfoInterface(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sap/bc/adt/oo/interfaces/ZIF_EXAMPLE" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.oo.interfaces.v5+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<intf:abapInterface adtcore:name="ZIF_EXAMPLE" adtcore:type="INTF/OI"
  adtcore:description="Example Interface"
  xmlns:intf="http://www.sap.com/adt/oo/interfaces"
  xmlns:adtcore="http://www.sap.com/adt/core">
  <adtcore:packageRef adtcore:uri="/sap/bc/adt/packages/ztest" adtcore:name="ZTEST"/>
</intf:abapInterface>`))
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	info, err := client.GetObjectInfo(context.Background(), "/sap/bc/adt/oo/interfaces/ZIF_EXAMPLE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Name != "ZIF_EXAMPLE" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.Type != "INTF/OI" {
		t.Errorf("type: got %q", info.Type)
	}
}
