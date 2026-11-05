//go:build integration

package adt_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestTransportErrorsDoNotNameTheHost_Integration is the live half of #167.
// A request that fails below HTTP must return an error whose text names
// neither the system's host name, nor its port, nor an address the name
// resolves to. Integration tests print such errors with %v, and their output
// is posted on public pull requests.
//
// Two failure paths are exercised against each configured system:
//
//   - deadline: a context that has already expired. net/http gives up
//     before dialling and reports the full request URL.
//   - refused: the real host name with a port no ADT service listens on. The
//     name is resolved by the real resolver, so the dial error carries a real
//     address of the system.
//
// Neither path sends a credential: the first never dials, the second never
// gets past TCP. On failure the test reports which kind of value leaked,
// never the value or the raw error text.
func TestTransportErrorsDoNotNameTheHost_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			u, err := url.Parse(sys.Config.Host)
			if err != nil || u.Hostname() == "" {
				t.Fatal("configured host is not a URL with a host name")
			}
			leaks := map[string]string{"host name": u.Hostname()}
			if p := u.Port(); p != "" {
				leaks["port"] = ":" + p
			}
			if addrs, err := net.DefaultResolver.LookupHost(context.Background(), u.Hostname()); err == nil {
				for i, a := range addrs {
					leaks[fmt.Sprintf("resolved address #%d", i+1)] = a
				}
			}

			t.Run("deadline", func(t *testing.T) {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer cancel()
				_, err := sys.Client.GetObjectInfo(ctx, "/sap/bc/adt/programs/programs/RSPARAM")
				assertTransportErrorClean(t, err, leaks)
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Error("want errors.Is(err, context.DeadlineExceeded) to survive the redaction")
				}
			})

			t.Run("refused", func(t *testing.T) {
				cfg := sys.Config
				refused := *u
				refused.Host = net.JoinHostPort(u.Hostname(), "1")
				cfg.Host = refused.String()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, err := adt.NewClient(cfg).GetObjectInfo(ctx, "/sap/bc/adt/programs/programs/RSPARAM")
				// The dial may be refused or time out behind a firewall; both
				// are transport errors and both must be clean.
				assertTransportErrorClean(t, err, leaks)
			})
		})
	}
}

// assertTransportErrorClean fails when err is nil, is an ADT error (SAP
// answered, so no transport failure was measured), or names any leak.
func assertTransportErrorClean(t *testing.T, err error, leaks map[string]string) {
	t.Helper()
	if err == nil {
		t.Fatal("want a transport error, got none")
	}
	var adtErr *adt.ADTError
	if errors.As(err, &adtErr) {
		t.Fatalf("want a transport error, SAP answered with status %d", adtErr.StatusCode)
	}
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Errorf("want a *url.Error in the chain, got %T", err)
	}
	text := strings.ToLower(err.Error())
	for kind, value := range leaks {
		if strings.Contains(text, strings.ToLower(value)) {
			t.Errorf("error text names the system's %s", kind)
		}
	}
}
