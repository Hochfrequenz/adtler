package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"

	"github.com/Hochfrequenz/adtler/adt"
)

// captureCreatePackageBody runs CreatePackage against a stub server and
// returns the request body it sent.
func captureCreatePackageBody(t *testing.T, languageVersion string) string {
	t.Helper()
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
	if err := client.CreatePackage(context.Background(), "$ZEXAMPLE", "Example", "USERA", "LOCAL", "", languageVersion, ""); err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	return gotBody
}

// adtler#254: an ABAP Cloud package is requested with the domain value of
// ABAP_LANGUAGE_VERSION in pak:attributes, as SAP reports it on read.
func TestCreatePackage_SendsLanguageVersion(t *testing.T) {
	body := captureCreatePackageBody(t, adt.LanguageVersionCloudDevelopment)
	if !strings.Contains(body, `pak:languageVersion="5"`) {
		t.Errorf("body lacks pak:languageVersion=\"5\": %s", body)
	}
}

// Without a language version the request stays exactly as before adtler#254,
// so SAP keeps choosing its own default.
func TestCreatePackage_EmptyLanguageVersionOmitsAttribute(t *testing.T) {
	body := captureCreatePackageBody(t, "")
	if strings.Contains(body, "languageVersion") {
		t.Errorf("body names a language version although none was asked for: %s", body)
	}
}

func serveObjectDocument(t *testing.T, doc string) adt.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(doc))
	}))
	t.Cleanup(srv.Close)
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
}

// Object documents carry the language version as a name on their root element
// (measured on S/4HANA, SAP_BASIS 816).
func TestGetObjectInfo_ReportsABAPLanguageVersion(t *testing.T) {
	client := serveObjectDocument(t, `<class:abapClass xmlns:class="http://www.sap.com/adt/oo/classes" xmlns:adtcore="http://www.sap.com/adt/core"`+
		` adtcore:name="ZCL_EXAMPLE" adtcore:type="CLAS/OC" adtcore:abapLanguageVersion="cloudDevelopment">`+
		`<adtcore:packageRef adtcore:name="ZEXAMPLE"/></class:abapClass>`)

	info, err := client.GetObjectInfo(context.Background(), "/sap/bc/adt/oo/classes/zcl_example")
	if err != nil {
		t.Fatalf("GetObjectInfo: %v", err)
	}
	if info.ABAPLanguageVersion != "cloudDevelopment" {
		t.Errorf("ABAPLanguageVersion = %q, want cloudDevelopment", info.ABAPLanguageVersion)
	}
	if info.PackageLanguageVersion != "" {
		t.Errorf("PackageLanguageVersion = %q for a class, want empty", info.PackageLanguageVersion)
	}
}

// Package documents carry no adtcore:abapLanguageVersion; theirs is the domain
// value in pak:attributes.
func TestGetObjectInfo_ReportsPackageLanguageVersion(t *testing.T) {
	client := serveObjectDocument(t, `<pak:package xmlns:pak="http://www.sap.com/adt/packages" xmlns:adtcore="http://www.sap.com/adt/core"`+
		` adtcore:name="ZEXAMPLE" adtcore:type="DEVC/K">`+
		`<pak:attributes pak:packageType="development" pak:languageVersion="5"/></pak:package>`)

	info, err := client.GetObjectInfo(context.Background(), "/sap/bc/adt/packages/zexample")
	if err != nil {
		t.Fatalf("GetObjectInfo: %v", err)
	}
	if info.PackageLanguageVersion != adt.LanguageVersionCloudDevelopment {
		t.Errorf("PackageLanguageVersion = %q, want %q", info.PackageLanguageVersion, adt.LanguageVersionCloudDevelopment)
	}
}
