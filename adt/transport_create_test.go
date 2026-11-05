package adt_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// discoveryWithTransportRequests is the S/4 shape: the transport organizer
// collection advertises its media type. ECC lists the collection without
// any app:accept.
const discoveryWithTransportRequests = `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/cts/transportrequests">
      <app:accept>application/vnd.sap.adt.transportorganizer.v1+xml</app:accept>
    </app:collection>
  </app:workspace>
</app:service>`

const discoveryWithoutTransportRequests = `<?xml version="1.0"?>
<app:service xmlns:app="http://www.w3.org/2007/app">
  <app:workspace>
    <app:collection href="/sap/bc/adt/cts/transportrequests"/>
  </app:workspace>
</app:service>`

// createdRequestResponse is S/4's 201 body, trimmed to what CreateTransport
// reads (captured 2026-11-05, SAP_BASIS 816).
const createdRequestResponse = `<?xml version="1.0" encoding="utf-8"?>` +
	`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">` +
	`<tm:request tm:number="AAAK900777" tm:parent="" tm:desc="d" tm:type="T" tm:target="DUM"><tm:long_desc/></tm:request>` +
	`</tm:root>`

// typedCreateServer serves discovery and records every other request.
type typedCreateServer struct {
	mu    sync.Mutex
	paths []string
	body  string
}

func (s *typedCreateServer) start(t *testing.T, discovery string) adt.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(discovery))
			return
		}
		data, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.paths = append(s.paths, r.Method+" "+r.URL.Path)
		s.body = string(data)
		s.mu.Unlock()
		if r.URL.Path != transportRequestsEndpoint {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.transportorganizer.v1+xml")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(createdRequestResponse))
	}))
	t.Cleanup(srv.Close)
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "usera", Password: "P", Client: "100"})
}

// TestCreateTransport_RefusesIgnoredInputs: every input SAP would drop or
// refuse is an error before any request goes out (#137).
func TestCreateTransport_RefusesIgnoredInputs(t *testing.T) {
	tests := []struct {
		name string
		opts adt.CreateTransportOptions
		want string
	}{
		{"workbench without package", adt.CreateTransportOptions{Description: "d"}, "needs a package"},
		{"workbench with blank package", adt.CreateTransportOptions{Description: "d", Package: "   "}, "needs a package"},
		{"workbench with target", adt.CreateTransportOptions{Type: adt.TransportTypeWorkbench, Description: "d", Package: "ZPKG", Target: "DUM"}, "leave Target empty"},
		{"customizing with package", adt.CreateTransportOptions{Type: adt.TransportTypeCustomizing, Description: "d", Package: "ZPKG"}, "leave Package empty"},
		{"copies with package", adt.CreateTransportOptions{Type: adt.TransportTypeCopies, Description: "d", Package: "ZPKG", Target: "DUM"}, "leave Package empty"},
		{"unknown type", adt.CreateTransportOptions{Type: "X", Description: "d"}, "unsupported request type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s typedCreateServer
			client := s.start(t, discoveryWithTransportRequests)
			nr, err := client.CreateTransport(context.Background(), tt.opts)
			if err == nil {
				t.Fatalf("want an error, got request %q", nr)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error should contain %q, got %v", tt.want, err)
			}
			if len(s.paths) != 0 {
				t.Errorf("sent %v, want no request", s.paths)
			}
		})
	}
}

// TestCreateTransport_TypedNotSupported: without the transport organizer
// collection in discovery (ECC), a customizing request or a transport of
// copies is refused with ErrorNotSupported and nothing is sent.
func TestCreateTransport_TypedNotSupported(t *testing.T) {
	for _, typ := range []adt.TransportType{adt.TransportTypeCustomizing, adt.TransportTypeCopies} {
		t.Run(string(typ), func(t *testing.T) {
			var s typedCreateServer
			client := s.start(t, discoveryWithoutTransportRequests)
			_, err := client.CreateTransport(context.Background(), adt.CreateTransportOptions{Type: typ, Description: "d"})
			if got := adt.ClassifyError(err); got != adt.ErrorNotSupported {
				t.Fatalf("ClassifyError: got %v, want ErrorNotSupported (err %v)", got, err)
			}
			if len(s.paths) != 0 {
				t.Errorf("sent %v, want no request", s.paths)
			}
		})
	}
}

// TestCreateTransport_Copies: a transport of copies goes to the transport
// organizer with type and upper-cased target, and no task (SAP creates none).
func TestCreateTransport_Copies(t *testing.T) {
	var s typedCreateServer
	client := s.start(t, discoveryWithTransportRequests)

	nr, err := client.CreateTransport(context.Background(), adt.CreateTransportOptions{
		Type: "t", Description: "copy & paste", Target: " dum ",
	})
	if err != nil {
		t.Fatalf("CreateTransport: %v", err)
	}
	if nr != "AAAK900777" {
		t.Errorf("number: got %q, want AAAK900777", nr)
	}
	if len(s.paths) != 1 || s.paths[0] != "POST "+transportRequestsEndpoint {
		t.Fatalf("requests: got %v, want one POST to %s", s.paths, transportRequestsEndpoint)
	}
	for _, want := range []string{`tm:type="T"`, `tm:target="DUM"`, `tm:desc="copy &amp; paste"`, `xmlns:tm="http://www.sap.com/cts/adt/tm"`} {
		if !strings.Contains(s.body, want) {
			t.Errorf("body missing %s:\n%s", want, s.body)
		}
	}
	for _, unwanted := range []string{"tm:task", "tm:useraction", "tm:number"} {
		if strings.Contains(s.body, unwanted) {
			t.Errorf("body must not contain %s:\n%s", unwanted, s.body)
		}
	}
}

// TestCreateTransport_Customizing: a customizing request gets one task owned
// by the logon user, like SE09; without a target the attribute is omitted
// and SAP applies the client's default.
func TestCreateTransport_Customizing(t *testing.T) {
	var s typedCreateServer
	client := s.start(t, discoveryWithTransportRequests)

	if _, err := client.CreateTransport(context.Background(), adt.CreateTransportOptions{
		Type: adt.TransportTypeCustomizing, Description: "cust",
	}); err != nil {
		t.Fatalf("CreateTransport: %v", err)
	}
	for _, want := range []string{`tm:type="W"`, `<tm:task tm:owner="USERA"></tm:task>`} {
		if !strings.Contains(s.body, want) {
			t.Errorf("body missing %s:\n%s", want, s.body)
		}
	}
	if strings.Contains(s.body, "tm:target") {
		t.Errorf("body must omit an empty target:\n%s", s.body)
	}
}
