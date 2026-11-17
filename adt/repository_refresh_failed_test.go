package adt_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
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

// TestLookups_StopAfterAFailedTokenRefresh: lookups whose answer would
// otherwise silently be incomplete stop at the first failed refresh and return
// it (#298). The server answers 401 to everything after the preflight; each
// further request would mean one more refresh attempt.
func TestLookups_StopAfterAFailedTokenRefresh(t *testing.T) {
	for _, op := range []struct {
		name string
		call func(ctx context.Context, c adt.Client) error
	}{
		{"DDIC dependency chain", func(ctx context.Context, c adt.Client) error {
			_, err := c.GetObjectDependencies(ctx, "TABL", "ZTABLE", 0, 2)
			return err
		}},
		{"transport request list", func(ctx context.Context, c adt.Client) error {
			_, err := c.GetTransportRequests(ctx, adt.TransportListOptions{User: "USERA"})
			return err
		}},
		{"batch syntax check over several chunks", func(ctx context.Context, c adt.Client) error {
			uris := make([]string, batchCheckObjects)
			for i := range uris {
				uris[i] = probeObjectURI + string(rune('a'+i))
			}
			results := c.BatchSyntaxCheck(ctx, uris)
			if results[len(results)-1].Error == "" {
				return errors.New("last result carries no error")
			}
			return errors.New(results[len(results)-1].Error)
		}},
	} {
		t.Run(op.name, func(t *testing.T) {
			srv := newUnauthorizedServer(t, false)
			var refreshes int32
			c := adt.NewClientWithToken(newTestConfig(srv.URL), "t0", func(string) (string, error) {
				refreshes++
				return "", errRefreshRejected
			})
			if _, err := c.SystemFlavor(context.Background()); err != nil {
				t.Fatalf("establishing the session: %v", err)
			}
			srv.reject.Store(true)

			err := op.call(context.Background(), c)
			if err == nil || !strings.Contains(err.Error(), adt.ErrTokenRefreshFailed.Error()) {
				t.Fatalf("got %v, want the failed token refresh", err)
			}
			if refreshes != 1 {
				t.Errorf("refresh attempted %d times, want 1: the lookup went on after the first failure", refreshes)
			}
		})
	}
}
