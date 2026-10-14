package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// Issue #223: a rejected user name or password came back as
// "SAP ADT error 401: SAP returned an HTML error page (no parseable detail)"
// and classified as ErrorUnknown. These tests drive the real request path
// (GetSource → checkResponse → parseADTError) against an httptest server that
// answers every request, including the CSRF preflight, with the given status
// and body — as SAP does when the Basic-auth credentials are wrong.

const unauthorizedTestURI = "/sap/bc/adt/programs/programs/ZTEST"

// sapLogonErrorPage is an HTML page without the errorTextHeader/detailText
// layout, so the parser can extract no detail from it — the shape observed
// on both S/4HANA and SAP ERP 6.0 for a wrong password.
const sapLogonErrorPage = `<!DOCTYPE html>
<html><head><title>Logon Error Message</title></head>
<body><div id="logonError"><span>Anmeldung fehlgeschlagen</span></div></body></html>`

// sapLogonDetailPage uses the "Application Server Error" layout the parser
// does understand, so SAP's own text must survive unchanged.
const sapLogonDetailPage = `<!DOCTYPE html>
<html><body>
<p class="errorTextHeader">401 Unauthorized</p>
<p class="detailText">Logon failed for client 100.</p>
</body></html>`

func serveStatus(status int, contentType, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func getSourceADTErr(t *testing.T, srv *httptest.Server) *adt.ADTError {
	t.Helper()
	client := adt.NewClient(newTestConfig(srv.URL))
	_, err := client.GetSource(context.Background(), unauthorizedTestURI)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
	}
	return adtErr
}

func assertCredentialsMessage(t *testing.T, adtErr *adt.ADTError) {
	t.Helper()
	if adtErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401 (callers match on it)", adtErr.StatusCode)
	}
	if got := adt.ClassifyError(adtErr); got != adt.ErrorUnauthorized {
		t.Errorf("ClassifyError = %v, want %v", got, adt.ErrorUnauthorized)
	}
	msg := adtErr.Message
	if !strings.Contains(msg, "credentials") {
		t.Errorf("message should tell the user to check the credentials, got %q", msg)
	}
	if !strings.Contains(msg, "lock the user") {
		t.Errorf("message should warn that repeated logons can lock the user, got %q", msg)
	}
	// parseADTError does not know the auth mode, so the text must also fit
	// an OAuth client whose token was rejected.
	if !strings.Contains(msg, "OAuth token") {
		t.Errorf("message should also cover an OAuth token, got %q", msg)
	}
	// Error() already prefixes "SAP ADT error 401: "; the message must not
	// repeat the status code.
	if strings.Contains(msg, "401") {
		t.Errorf("message should not repeat the status code, got %q", msg)
	}
	if strings.Contains(msg, "HTML error page") {
		t.Errorf("message should not be the HTML placeholder, got %q", msg)
	}
}

func TestUnauthorized_HTMLWithoutDetail_NamesCredentials(t *testing.T) {
	srv := serveStatus(http.StatusUnauthorized, "text/html", sapLogonErrorPage)
	defer srv.Close()
	assertCredentialsMessage(t, getSourceADTErr(t, srv))
}

func TestUnauthorized_EmptyBody_NamesCredentials(t *testing.T) {
	srv := serveStatus(http.StatusUnauthorized, "", "")
	defer srv.Close()
	assertCredentialsMessage(t, getSourceADTErr(t, srv))
}

func TestUnauthorized_HTMLWithDetail_KeepsSAPText(t *testing.T) {
	srv := serveStatus(http.StatusUnauthorized, "text/html", sapLogonDetailPage)
	defer srv.Close()
	adtErr := getSourceADTErr(t, srv)
	if got := adt.ClassifyError(adtErr); got != adt.ErrorUnauthorized {
		t.Errorf("ClassifyError = %v, want %v", got, adt.ErrorUnauthorized)
	}
	if want := "401 Unauthorized — Logon failed for client 100."; adtErr.Message != want {
		t.Errorf("Message = %q, want SAP's own text %q", adtErr.Message, want)
	}
}

func TestUnauthorized_ExceptionEnvelope_KeepsSAPText(t *testing.T) {
	const body = `<?xml version="1.0"?>
<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">
  <namespace id="com.sap.adt"/>
  <type id="ExceptionSomethingUnmapped"/>
  <message lang="EN">Password logon no longer possible</message>
</exc:exception>`
	srv := serveStatus(http.StatusUnauthorized, "application/xml", body)
	defer srv.Close()
	adtErr := getSourceADTErr(t, srv)
	if got := adt.ClassifyError(adtErr); got != adt.ErrorUnauthorized {
		t.Errorf("ClassifyError = %v, want %v", got, adt.ErrorUnauthorized)
	}
	if want := "Password logon no longer possible"; adtErr.Message != want {
		t.Errorf("Message = %q, want SAP's own text %q", adtErr.Message, want)
	}
}

// A 403 with the same detail-less page is an authorization problem, not a
// logon problem: it stays ErrorForbidden and keeps the generic placeholder.
func TestForbidden_HTMLWithoutDetail_StaysForbidden(t *testing.T) {
	srv := serveStatus(http.StatusForbidden, "text/html", sapLogonErrorPage)
	defer srv.Close()
	adtErr := getSourceADTErr(t, srv)
	if got := adt.ClassifyError(adtErr); got != adt.ErrorForbidden {
		t.Errorf("ClassifyError = %v, want %v", got, adt.ErrorForbidden)
	}
	if strings.Contains(adtErr.Message, "credentials") {
		t.Errorf("a 403 must not be reported as a credential problem, got %q", adtErr.Message)
	}
}
