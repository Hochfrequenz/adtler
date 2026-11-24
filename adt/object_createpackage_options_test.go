package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"

	"github.com/Hochfrequenz/adtler/adt"
)

const (
	examplePackage    = "ZEXAMPLE"
	exampleSuperPkg   = "ZEXAMPLE_STRUKTUR"
	exampleLayer      = "ZLAY"
	recordChangesAttr = `pak:recordChanges="true"`
)

// createPackageBody runs CreatePackage against a stub server and returns the
// request body it sent ("" when it sent none) and the error.
func createPackageBody(t *testing.T, name, layer string, opts ...adt.PackageOption) (string, error) {
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
	err := client.CreatePackage(context.Background(), name, "Example", "USERA", "HOME", layer, "", "AAAK900001", opts...)
	return gotBody, err
}

// adtler#291: SAP refuses a package with a transport layer unless change
// recording is switched on (400 "Change recording must be activated").
func TestCreatePackage_TransportablePackageRecordsChanges(t *testing.T) {
	body, err := createPackageBody(t, examplePackage, exampleLayer)
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if !strings.Contains(body, recordChangesAttr) {
		t.Errorf("body lacks %s: %s", recordChangesAttr, body)
	}
}

// A local package has no change recording; the request stays as before.
func TestCreatePackage_LocalPackageDoesNotRecordChanges(t *testing.T) {
	body, err := createPackageBody(t, "$ZEXAMPLE", "")
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if strings.Contains(body, "recordChanges") {
		t.Errorf("body names recordChanges for a local package: %s", body)
	}
}

func TestCreatePackage_DefaultsToDevelopmentWithoutSuperPackage(t *testing.T) {
	body, err := createPackageBody(t, examplePackage, exampleLayer)
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if !strings.Contains(body, `pak:packageType="development"`) {
		t.Errorf("body lacks packageType development: %s", body)
	}
	if strings.Contains(body, "<pak:superPackage adtcore:name") {
		t.Errorf("body names a super package although none was asked for: %s", body)
	}
}

// adtler#291: a new top-level transportable package has to be a structure
// package, and a development package then goes below it.
func TestCreatePackage_SendsPackageTypeAndSuperPackage(t *testing.T) {
	body, err := createPackageBody(t, examplePackage, exampleLayer,
		adt.WithPackageType(adt.PackageTypeStructure))
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if !strings.Contains(body, `pak:packageType="structure"`) {
		t.Errorf("body lacks packageType structure: %s", body)
	}

	body, err = createPackageBody(t, examplePackage, exampleLayer,
		adt.WithPackageType(adt.PackageTypeDevelopment), adt.WithSuperPackage("zexample_struktur"))
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if !strings.Contains(body, `<pak:superPackage adtcore:name="`+exampleSuperPkg+`"`) {
		t.Errorf("body lacks the super package: %s", body)
	}
	if !strings.Contains(body, recordChangesAttr) {
		t.Errorf("body lacks %s: %s", recordChangesAttr, body)
	}
}

// A package type outside SAP's PACKAGE_TYPE is refused before any request.
func TestCreatePackage_RejectsUnknownPackageType(t *testing.T) {
	body, err := createPackageBody(t, examplePackage, exampleLayer, adt.WithPackageType("bogus"))
	if err == nil {
		t.Fatal("CreatePackage accepted an unknown package type")
	}
	if body != "" {
		t.Errorf("CreatePackage sent a request before refusing the type: %s", body)
	}
}
