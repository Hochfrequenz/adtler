package adt_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

// #298: when the package lookup of GetObjectInfo gets a 401 and the OAuth
// token cannot be refreshed, the credential is dead. The caller must get that
// error, not the object with an empty package.

var errRefreshRejected = errors.New("refresh token rejected")

func failingRefreshClient(host string) adt.Client {
	return adt.NewClientWithToken(sapmcpconfig.SAPSystem{Host: host, Client: "100"}, "t0",
		func(string) (string, error) { return "", errRefreshRejected })
}

func assertRefreshFailureReported(t *testing.T, info *adt.ObjectInfo, err error) {
	t.Helper()
	if !errors.Is(err, adt.ErrTokenRefreshFailed) || !errors.Is(err, errRefreshRejected) {
		t.Fatalf("GetObjectInfo: got info %v, error %v, want the failed token refresh", info, err)
	}
	if adt.ClassifyError(err) != adt.ErrorUnauthorized {
		t.Errorf("ClassifyError: got %v, want ErrorUnauthorized", adt.ClassifyError(err))
	}
}

func TestGetObjectInfo_HTTPService_FailedTokenRefreshIsReported(t *testing.T) {
	srv := newHTTPSvcPackageServer(httpSvcXML, http.StatusUnauthorized)
	defer srv.Close()
	info, err := failingRefreshClient(srv.URL).GetObjectInfo(context.Background(), httpSvcURI)
	assertRefreshFailureReported(t, info, err)
}

func TestGetObjectInfo_FunctionModule_FailedTokenRefreshIsReported(t *testing.T) {
	var fmoduleGets, groupGets int32
	srv := packageLookupServer(fmoduleXMLWithoutPackage, http.StatusUnauthorized, &fmoduleGets, &groupGets)
	defer srv.Close()
	info, err := failingRefreshClient(srv.URL).GetObjectInfo(context.Background(), fmoduleURI)
	assertRefreshFailureReported(t, info, err)
}
