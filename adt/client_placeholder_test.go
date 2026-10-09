package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	placeholderUser     = "${env:SYS_USER}"
	placeholderPassword = "${env:SYS_PASSWORD}"
	plainUser           = "USERA"
	plainPassword       = "plain-secret"
	probeObjectURI      = "/sap/bc/adt/programs/programs/ztest"
)

// countingServer answers every request with 200 and counts how many arrived.
// The number of requests is the whole point of the placeholder tests: a
// placeholder credential must never reach SAP, not even once.
func countingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// placeholderOperations are the request paths that must refuse a placeholder:
// an ordinary read, a mutating request and Logout.
var placeholderOperations = []struct {
	name string
	call func(ctx context.Context, c adt.Client) error
}{
	{"read", func(ctx context.Context, c adt.Client) error {
		_, err := c.GetSource(ctx, probeObjectURI)
		return err
	}},
	{"mutate", func(ctx context.Context, c adt.Client) error {
		return c.UnlockObject(ctx, probeObjectURI, "handle")
	}},
	{"logout", func(ctx context.Context, c adt.Client) error {
		return c.Logout(ctx)
	}},
}

func TestUnresolvedPlaceholder_IsRefusedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		field    string
		user     string
		password string
		wantVar  string
	}{
		{"password", plainUser, placeholderPassword, placeholderPassword},
		{"user", placeholderUser, plainPassword, placeholderUser},
	}
	for _, tc := range cases {
		for _, op := range placeholderOperations {
			t.Run(tc.field+"/"+op.name, func(t *testing.T) {
				srv, requests := countingServer(t)
				client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: tc.user, Password: tc.password})

				err := op.call(context.Background(), client)

				if !errors.Is(err, adt.ErrUnresolvedPlaceholder) {
					t.Fatalf("error = %v, want one matching ErrUnresolvedPlaceholder", err)
				}
				if got := requests.Load(); got != 0 {
					t.Errorf("server received %d request(s), want 0", got)
				}
				msg := err.Error()
				if !strings.Contains(msg, tc.field) || !strings.Contains(msg, tc.wantVar) {
					t.Errorf("error %q should name the field %q and the placeholder %q", msg, tc.field, tc.wantVar)
				}
				for _, other := range []string{plainUser, plainPassword} {
					if strings.Contains(msg, other) {
						t.Errorf("error %q leaks the other credential %q", msg, other)
					}
				}
			})
		}
	}
}

func TestUnresolvedPlaceholder_IsRefusedOnTheFreshSessionPath(t *testing.T) {
	// RunClass sends its request on an isolated session cloned from the client;
	// the clone must refuse the placeholder as well.
	srv, requests := countingServer(t)
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: plainUser, Password: placeholderPassword})

	_, err := client.RunClass(context.Background(), "ZCL_EXAMPLE")

	if !errors.Is(err, adt.ErrUnresolvedPlaceholder) {
		t.Fatalf("error = %v, want one matching ErrUnresolvedPlaceholder", err)
	}
	if got := requests.Load(); got != 0 {
		t.Errorf("server received %d request(s), want 0", got)
	}
}

func TestUnresolvedPlaceholder_OtherValuesAreSentUnchanged(t *testing.T) {
	values := map[string]string{
		"contains a placeholder among other characters": "pre-${env:X}-post",
		"invalid variable name":                         "${env:not valid}",
		"variable name starting with a digit":           "${env:1ABC}",
		"missing prefix":                                "${SYS_PASSWORD}",
	}
	for name, password := range values {
		t.Run(name, func(t *testing.T) {
			srv, requests := countingServer(t)
			client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: plainUser, Password: password})

			// The test server's answer is irrelevant; only that the request left.
			_ = client.Logout(context.Background())

			if got := requests.Load(); got != 1 {
				t.Errorf("server received %d request(s), want 1", got)
			}
		})
	}
}

func TestUnresolvedPlaceholder_DoesNotApplyToOAuthClients(t *testing.T) {
	srv, requests := countingServer(t)
	client := adt.NewClientWithToken(
		sapmcpconfig.SAPSystem{Host: srv.URL, User: placeholderUser, Password: placeholderPassword},
		"access-token", nil)

	_ = client.Logout(context.Background())

	if got := requests.Load(); got != 1 {
		t.Errorf("server received %d request(s), want 1: a client with a token sends no Basic credentials", got)
	}
}
