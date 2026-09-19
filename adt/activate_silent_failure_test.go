package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// activateWithInactiveCheckServer mocks an activation POST followed by a
// GetInactiveObjects read. activationBody is written verbatim as the
// activation response (empty string reproduces the ECC "always empty" case
// from Hochfrequenz/aibap.mcp#500 / adtler#144; a well-formed
// zero-<msg> body reproduces the shape used by TestActivateObjectSuccess).
//
// inactiveEntryURI, if non-empty, is embedded as a still-inactive object's
// ref/@uri; pass "" for a clean inactive-objects list. inactiveStatus, if
// non-zero, makes the inactive-objects read itself fail with that HTTP
// status instead of returning a list.
func activateWithInactiveCheckServer(t *testing.T, activationBody, inactiveEntryURI string, inactiveStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case activationPath:
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(activationBody))
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

// TestActivateObjects_StillInactiveAfterEmptyBody is the regression guard
// for Hochfrequenz/aibap.mcp#500: the activation POST returning 2xx with an
// empty body is not proof of activation. ActivateObjects must catch the
// silent no-op by re-checking GetInactiveObjects.
func TestActivateObjects_StillInactiveAfterEmptyBody(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"
	srv := activateWithInactiveCheckServer(t, "", objectURI+"/source/main", 0)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjects(context.Background(), []string{objectURI})
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

// TestActivateObjects_StillInactiveAfterWellFormedEmptyMessageBody covers the
// gap in an earlier version of this fix: a well-formed activation response
// with zero <msg> elements (the same shape TestActivateObjectSuccess uses)
// looks identical to a genuine success on the wire, but must still be
// verified — a system could return exactly this shape while silently not
// activating the object. Gating verification on "body was empty or
// unparseable" would skip this case entirely; ActivateObjects must verify
// on any apparent success, not just an inconclusive one.
func TestActivateObjects_StillInactiveAfterWellFormedEmptyMessageBody(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"
	wellFormedEmptyBody := `<?xml version="1.0" encoding="utf-8"?><chkl:messages xmlns:chkl="http://www.sap.com/abapxml/checklist"><chkl:properties checkExecuted="false" activationExecuted="false" generationExecuted="true"/></chkl:messages>`
	srv := activateWithInactiveCheckServer(t, wellFormedEmptyBody, objectURI+"/source/main", 0)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjects(context.Background(), []string{objectURI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Success {
		t.Fatal("expected Success=false: object is still listed as inactive despite a clean-looking activation response")
	}
}

// TestActivateObjects_EmptyBodyButActuallyActive guards against
// over-correcting #500: an empty activation body is the common case on this
// stack even for a genuinely successful activation (aibap.mcp#34).
// ActivateObjects must not report failure when GetInactiveObjects confirms
// the object is no longer inactive.
func TestActivateObjects_EmptyBodyButActuallyActive(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"
	srv := activateWithInactiveCheckServer(t, "", "", 0)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjects(context.Background(), []string{objectURI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected Success=true, got false with messages: %+v", result.Messages)
	}
}

// TestActivateObjects_InactiveObjectsReadFails mirrors
// ReleaseTransportVerified's optimistic fallback: if the post-activation
// verification read itself fails, assume the (unverified) result stands
// rather than turning a transport-layer hiccup into a false failure.
func TestActivateObjects_InactiveObjectsReadFails(t *testing.T) {
	const objectURI = "/sap/bc/adt/oo/classes/%2fabc%2fcl_example"
	srv := activateWithInactiveCheckServer(t, "", "", http.StatusInternalServerError)
	defer srv.Close()

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	client := adt.NewClient(cfg)

	result, err := client.ActivateObjects(context.Background(), []string{objectURI})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected optimistic Success=true when verification read fails, got false with messages: %+v", result.Messages)
	}
}

// TestActivateObjects_DoesNotVerifyOnExplicitError ensures a real,
// well-formed error response from the activation call itself is trusted as
// Success:false without needing (or performing) the extra verification
// round-trip.
func TestActivateObjects_DoesNotVerifyOnExplicitError(t *testing.T) {
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

	result, err := client.ActivateObjects(context.Background(), []string{"/sap/bc/adt/oo/classes/%2fabc%2fcl_example"})
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
