package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	abapGitReposPath = "/sap/bc/adt/abapgitsync/repos"
	jsonMIME         = "application/json"
	xmlMIME          = "application/xml"
	contentTypeHdr   = "Content-Type"

	abapGitReposBody = `{"repos":[` +
		`{"key":"K1","name":"repo-one","url":"https://example.invalid/one.git","package":"ZPKG_ONE",` +
		`"branch":"refs/heads/main","offline":true,"deserialized_at":"20260101120000","deserialized_by":"DEVUSER"},` +
		`{"key":"K2","name":"repo-two","url":"https://example.invalid/two.git","package":"ZPKG_TWO",` +
		`"branch":"refs/heads/dev","offline":false,"deserialized_at":"","deserialized_by":""}]}`

	adtNotFoundXML = `<?xml version="1.0" encoding="utf-8"?>` +
		`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework">` +
		`<namespace id="com.sap.adt"/><type id="ExceptionResourceNotFound"/>` +
		`<message lang="EN">Resource not found</message></exc:exception>`
)

// abapGitSyncServer answers the CSRF preflight and passes every other request to h.
func abapGitSyncServer(t *testing.T, h http.HandlerFunc) adt.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == csrfEndpoint {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
}

func TestListAbapGitRepos_RequestShape(t *testing.T) {
	var gotMethod, gotPath, gotAccept string
	client := abapGitSyncServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAccept = r.Method, r.URL.Path, r.Header.Get("Accept")
		w.Header().Set(contentTypeHdr, jsonMIME)
		_, _ = w.Write([]byte(abapGitReposBody))
	})

	list, err := client.ListAbapGitRepos(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method: got %q, want GET", gotMethod)
	}
	if gotPath != abapGitReposPath {
		t.Errorf("path: got %q, want %q", gotPath, abapGitReposPath)
	}
	if gotAccept != jsonMIME {
		t.Errorf("Accept: got %q, want %q", gotAccept, jsonMIME)
	}
	if list.Count != 2 || len(list.Repos) != 2 {
		t.Fatalf("Count/len: got %d/%d, want 2/2", list.Count, len(list.Repos))
	}
	want := adt.AbapGitRepo{
		Key: "K1", Name: "repo-one", URL: "https://example.invalid/one.git", Package: "ZPKG_ONE",
		Branch: "refs/heads/main", Offline: true, DeserializedAt: "20260101120000", DeserializedBy: "DEVUSER",
	}
	if list.Repos[0] != want {
		t.Errorf("first repo: got %+v, want %+v", list.Repos[0], want)
	}
	if list.Repos[1].Offline {
		t.Errorf("second repo Offline: got true, want false")
	}
}

func TestListAbapGitRepos_EmptyList(t *testing.T) {
	client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHdr, jsonMIME)
		_, _ = w.Write([]byte(`{"repos": []}`))
	})
	list, err := client.ListAbapGitRepos(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if list.Count != 0 {
		t.Errorf("Count: got %d, want 0", list.Count)
	}
}

func TestListAbapGitRepos_NotInstalled(t *testing.T) {
	client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHdr, xmlMIME)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(adtNotFoundXML))
	})
	_, err := client.ListAbapGitRepos(context.Background())
	if !errors.Is(err, adt.ErrAbapGitSyncNotInstalled) {
		t.Fatalf("got %v, want ErrAbapGitSyncNotInstalled", err)
	}
	var adtErr *adt.ADTError
	if errors.As(err, &adtErr) {
		t.Errorf("not-installed must not surface as *ADTError, got %+v", adtErr)
	}
}

func TestAbapGitSyncError_Mapping(t *testing.T) {
	cases := []struct {
		code   string
		status int
	}{
		{adt.AbapGitErrRepoNotFound, 404},
		{adt.AbapGitErrRepoAmbiguous, 409},
		{adt.AbapGitErrObjectNotInRepo, 422},
		{adt.AbapGitErrCredentialsMissing, 422},
		{adt.AbapGitErrCredentialsRejected, 422},
		{adt.AbapGitErrTransportRequired, 422},
		{adt.AbapGitErrNoModifiableTask, 422},
		{adt.AbapGitErrRequirementsNotMet, 422},
		{adt.AbapGitErrRemoteChanged, 409},
		{adt.AbapGitErrGitError, 424},
		{adt.AbapGitErrInternal, 500},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(contentTypeHdr, jsonMIME)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"code":"` + tc.code + `","message":"m","details":["d1"]}`))
			})
			_, err := client.ListAbapGitRepos(context.Background())
			var syncErr *adt.AbapGitSyncError
			if !errors.As(err, &syncErr) {
				t.Fatalf("got %v (%T), want *AbapGitSyncError", err, err)
			}
			if syncErr.Code != tc.code {
				t.Errorf("Code: got %q, want %q", syncErr.Code, tc.code)
			}
			if syncErr.HTTPStatus != tc.status {
				t.Errorf("HTTPStatus: got %d, want %d", syncErr.HTTPStatus, tc.status)
			}
			if len(syncErr.Details) != 1 || syncErr.Details[0] != "d1" {
				t.Errorf("Details: got %v, want [d1]", syncErr.Details)
			}
			if !strings.Contains(err.Error(), tc.code+": m") {
				t.Errorf("Error(): got %q, want it to contain %q", err.Error(), tc.code+": m")
			}
			var adtErr *adt.ADTError
			if errors.As(err, &adtErr) {
				t.Errorf("companion error must not be an *ADTError")
			}
		})
	}
}

func TestAbapGitSync_404WithCompanionJSONIsNotSentinel(t *testing.T) {
	client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHdr, jsonMIME)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"REPO_NOT_FOUND","message":"no such repo"}`))
	})
	_, err := client.ListAbapGitRepos(context.Background())
	if errors.Is(err, adt.ErrAbapGitSyncNotInstalled) {
		t.Fatalf("companion 404 must not map to ErrAbapGitSyncNotInstalled: %v", err)
	}
	var syncErr *adt.AbapGitSyncError
	if !errors.As(err, &syncErr) || syncErr.Code != adt.AbapGitErrRepoNotFound {
		t.Errorf("got %v, want *AbapGitSyncError REPO_NOT_FOUND", err)
	}
}

func TestAbapGitSync_Non404WithoutJSON(t *testing.T) {
	client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHdr, xmlMIME)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Replace(adtNotFoundXML, "Resource not found", "Boom", 1)))
	})
	_, err := client.ListAbapGitRepos(context.Background())
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("got %v (%T), want *ADTError", err, err)
	}
	if adtErr.StatusCode != http.StatusInternalServerError || adtErr.Message != "Boom" {
		t.Errorf("got status %d message %q, want 500 / Boom", adtErr.StatusCode, adtErr.Message)
	}
	var syncErr *adt.AbapGitSyncError
	if errors.As(err, &syncErr) {
		t.Errorf("XML error must not be an *AbapGitSyncError")
	}
}

func TestListAbapGitRepos_BadJSON(t *testing.T) {
	client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHdr, jsonMIME)
		_, _ = w.Write([]byte(`not json`))
	})
	_, err := client.ListAbapGitRepos(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ListAbapGitRepos: decoding response") {
		t.Errorf("got %v, want a decoding error prefixed with the operation", err)
	}
}

func TestAbapGitSync_404WithJSONWithoutCodeIsNotInstalled(t *testing.T) {
	client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHdr, jsonMIME)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"gateway says no"}`))
	})
	_, err := client.ListAbapGitRepos(context.Background())
	if !errors.Is(err, adt.ErrAbapGitSyncNotInstalled) {
		t.Fatalf("got %v, want ErrAbapGitSyncNotInstalled", err)
	}
}
