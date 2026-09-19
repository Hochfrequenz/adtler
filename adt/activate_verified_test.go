package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// activateVerifiedServer mocks an activation POST that returns an empty body
// (the ECC behavior from Hochfrequenz/aibap.mcp#500 / adtler#144 — SAP
// answers 2xx with no message body regardless of whether activation actually
// happened) followed by a GetInactiveObjects read.
//
// inactiveEntry, if non-empty, is embedded as a still-inactive object whose
// ref/@uri equals objectURI; pass "" for a clean inactive-objects list.
func activateVerifiedServer(t *testing.T, objectURI, inactiveEntryURI string, inactiveStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case activationPath:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			// No body written at all — the ECC "always empty" case.
		case "/sap/bc/adt/activation/inactiveobjects":
			if inactiveStatus != 0 {
				w.WriteHeader(inactiveStatus)
				return
			}
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			if inactiveEntryURI == "" {
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"/>`))
				return
			}
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core">
  <entry>
    <object>
      <ref uri="` + inactiveEntryURI + `" type="CLAS/OC" name="/ABC/CL_EXAMPLE" packageName="/ABC/SOMEPKG"/>
    </object>
  </entry>
</ioc:inactiveObjects>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestActivateObjectsVerified_StillInactive is the regression guard for
// Hochfrequenz/aibap.mcp#500: ActivateObjects alone reports Success:true for
// any 2xx, even when the object never actually activated. Verified must
// catch this by re-checking GetInactiveObjects.
func TestActivateObjectsVerified_StillInactive(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"
	srv := activateVerifiedServer(t, objectURI, objectURI+"/source/main", 0)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjectsVerified(context.Background(), []string{objectURI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected Success=false: object is still listed as inactive after activation")
	}
	if len(result.Messages) == 0 {
		t.Fatal("expected a synthesized message explaining the still-inactive object")
	}
	if result.Messages[0].ObjectURI != objectURI {
		t.Errorf("message ObjectURI = %q, want %q", result.Messages[0].ObjectURI, objectURI)
	}
}

// TestActivateObjectsVerified_EmptyBodyButActuallyActive guards against
// over-correcting #500: an empty activation body is the common case on this
// stack even for a genuinely successful activation (aibap.mcp#34). Verified
// must not report failure when GetInactiveObjects confirms the object is no
// longer inactive.
func TestActivateObjectsVerified_EmptyBodyButActuallyActive(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"
	srv := activateVerifiedServer(t, objectURI, "", 0)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjectsVerified(context.Background(), []string{objectURI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected Success=true, got false with messages: %+v", result.Messages)
	}
}

// TestActivateObjectsVerified_InactiveObjectsReadFails mirrors
// ReleaseTransportVerified's optimistic fallback: if the post-activation
// verification read itself fails, assume the (unverified) result stands
// rather than turning a transport-layer hiccup into a false failure.
func TestActivateObjectsVerified_InactiveObjectsReadFails(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"
	srv := activateVerifiedServer(t, objectURI, "", http.StatusInternalServerError)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjectsVerified(context.Background(), []string{objectURI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected optimistic Success=true when verification read fails, got false with messages: %+v", result.Messages)
	}
}

// TestActivateObjectsVerified_DoesNotOverrideExplicitError ensures a real,
// well-formed error response from the activation call itself is trusted as
// Success:false without needing (or performing) the extra verification
// round-trip.
func TestActivateObjectsVerified_DoesNotOverrideExplicitError(t *testing.T) {
	calledInactive := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case activationPath:
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist">
  <msg objDescr="Class /ABC/CL_EXAMPLE" type="E" line="1"
       href="/sap/bc/adt/oo/classes/%2fabc%2fcl_example/source/main#start=5,0"
       forceSupported="true">
    <shortText><txt>Syntax error in line 5</txt></shortText>
  </msg>
</chkl:messages>`))
		case "/sap/bc/adt/activation/inactiveobjects":
			calledInactive = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?><ioc:inactiveObjects xmlns:ioc="http://www.sap.com/adt/core"/>`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjectsVerified(context.Background(), []string{"/sap/bc/adt/oo/classes/%2fabc%2fcl_example"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected Success=false from the explicit E message")
	}
	if calledInactive {
		t.Error("did not expect GetInactiveObjects to be called when the activation body already carried a definitive error")
	}
}
