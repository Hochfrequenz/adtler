package adt_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
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
		`"branch":"refs/heads/main","offline":true,"deserialized_at":"2026-10-06T07:42:34Z","deserialized_by":"DEVUSER"},` +
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
		Branch: "refs/heads/main", Offline: true, DeserializedAt: "2026-10-06T07:42:34Z", DeserializedBy: "DEVUSER",
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

type abapGitOp struct {
	name string
	call func(adt.Client) error
}

// abapGitOps lists every companion operation, for tests that must hold on all of them.
func abapGitOps() []abapGitOp {
	ctx := context.Background()
	return []abapGitOp{
		{"list", func(c adt.Client) error { _, err := c.ListAbapGitRepos(ctx); return err }},
		{"pull", func(c adt.Client) error {
			_, err := c.PullAbapGitRepo(ctx, adt.AbapGitPullRequest{Repo: "r"})
			return err
		}},
		{"push", func(c adt.Client) error {
			_, err := c.PushAbapGitRepo(ctx, adt.AbapGitPushRequest{
				Repo: "r", Objects: []adt.AbapGitObjectRef{{ObjType: "PROG", ObjName: "ZEXAMPLE"}}, Message: "msg",
			})
			return err
		}},
	}
}

func TestAbapGitSync_NotInstalled(t *testing.T) {
	for _, op := range abapGitOps() {
		t.Run(op.name, func(t *testing.T) {
			client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(contentTypeHdr, xmlMIME)
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(adtNotFoundXML))
			})
			err := op.call(client)
			if !errors.Is(err, adt.ErrAbapGitSyncNotInstalled) {
				t.Fatalf("got %v, want ErrAbapGitSyncNotInstalled", err)
			}
			var adtErr *adt.ADTError
			if errors.As(err, &adtErr) {
				t.Errorf("not-installed must not surface as *ADTError, got %+v", adtErr)
			}
		})
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
		{adt.AbapGitErrBadRequest, 400},
	}
	for _, tc := range cases {
		for _, op := range abapGitOps() {
			t.Run(op.name+"/"+tc.code, func(t *testing.T) {
				client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set(contentTypeHdr, jsonMIME)
					w.WriteHeader(tc.status)
					_, _ = w.Write([]byte(`{"code":"` + tc.code + `","message":"m","details":["d1"]}`))
				})
				err := op.call(client)
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

// postRecorder captures method, path, headers and decoded JSON body of the last non-CSRF request.
type postRecorder struct {
	method, path, contentType, accept string
	body                              map[string]any
	calls                             int
}

func recordingServer(t *testing.T, rec *postRecorder, response string) adt.Client {
	t.Helper()
	return abapGitSyncServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.calls++
		rec.method, rec.path = r.Method, r.URL.Path
		rec.contentType, rec.accept = r.Header.Get(contentTypeHdr), r.Header.Get("Accept")
		raw, _ := io.ReadAll(r.Body)
		rec.body = nil
		_ = json.Unmarshal(raw, &rec.body)
		w.Header().Set(contentTypeHdr, jsonMIME)
		_, _ = w.Write([]byte(response))
	})
}

func (rec *postRecorder) assertPost(t *testing.T, wantPath string) {
	t.Helper()
	if rec.method != http.MethodPost {
		t.Errorf("method: got %q, want POST", rec.method)
	}
	if rec.path != wantPath {
		t.Errorf("path: got %q, want %q", rec.path, wantPath)
	}
	if rec.contentType != jsonMIME || rec.accept != jsonMIME {
		t.Errorf("Content-Type/Accept: got %q/%q, want %q", rec.contentType, rec.accept, jsonMIME)
	}
}

const pulledBody = `{"status":"pulled","repo":{"name":"n","url":"u","package":"P"},` +
	`"log":[{"type":"I","text":"ok"},{"type":"W","text":"warn","obj_type":"CLAS","obj_name":"ZCL_EXAMPLE"}]}`

func jsonMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad test JSON: %v", err)
	}
	return m
}

func TestPullAbapGitRepo_RequestShape(t *testing.T) {
	var rec postRecorder
	client := recordingServer(t, &rec, pulledBody)
	_, err := client.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{
		Repo: "r", Transport: "<request>",
		Confirm: []adt.AbapGitConfirmation{{ObjType: "CLAS", ObjName: "ZCL_EXAMPLE", Action: "overwrite"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec.assertPost(t, "/sap/bc/adt/abapgitsync/pull")
	want := jsonMap(t, `{"repo":"r","transport":"<request>","confirm":`+
		`[{"obj_type":"CLAS","obj_name":"ZCL_EXAMPLE","action":"overwrite"}]}`)
	if !reflect.DeepEqual(rec.body, want) {
		t.Errorf("body: got %v, want %v", rec.body, want)
	}
}

func TestPullAbapGitRepo_OmitsEmptyOptionalMembers(t *testing.T) {
	var rec postRecorder
	client := recordingServer(t, &rec, pulledBody)
	if _, err := client.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{Repo: "r"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, k := range []string{"transport", "confirm"} {
		if _, ok := rec.body[k]; ok {
			t.Errorf("body must not contain %q: %v", k, rec.body)
		}
	}
}

func TestPullAbapGitRepo_StatusPulled(t *testing.T) {
	var rec postRecorder
	client := recordingServer(t, &rec, pulledBody)
	res, err := client.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{Repo: "r"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != adt.AbapGitStatusPulled || res.Repo != (adt.AbapGitRepoRef{Name: "n", URL: "u", Package: "P"}) {
		t.Errorf("status/repo: got %q %+v", res.Status, res.Repo)
	}
	wantLog := []adt.AbapGitLogEntry{
		{Type: "I", Text: "ok"},
		{Type: "W", Text: "warn", ObjType: "CLAS", ObjName: "ZCL_EXAMPLE"},
	}
	if !reflect.DeepEqual(res.Log, wantLog) {
		t.Errorf("log: got %+v, want %+v", res.Log, wantLog)
	}
}

func TestPullAbapGitRepo_StatusNeedsConfirmation(t *testing.T) {
	var rec postRecorder
	client := recordingServer(t, &rec, `{"status":"needs_confirmation","repo":{"name":"n","url":"u","package":"P"},`+
		`"confirmations_required":[{"obj_type":"CLAS","obj_name":"ZCL_EXAMPLE","action":"overwrite","text":"t",`+
		`"files":[{"path":"/src/","filename":"zcl_example.clas.abap","state":"M_"},`+
		`{"path":"/src/","filename":"zcl_example.clas.xml","state":"_D"}]}]}`)
	res, err := client.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{Repo: "r"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != adt.AbapGitStatusNeedsConfirmation || len(res.ConfirmationsRequired) != 1 {
		t.Fatalf("got %+v", res)
	}
	c := res.ConfirmationsRequired[0]
	if c.ObjType != "CLAS" || c.ObjName != "ZCL_EXAMPLE" || c.Action != "overwrite" || c.Text != "t" {
		t.Errorf("confirmation: got %+v", c)
	}
	if len(c.Files) != 2 || c.Files[0].State != "M_" || c.Files[1].State != "_D" ||
		c.Files[0].Filename != "zcl_example.clas.abap" || c.Files[0].Path != "/src/" {
		t.Errorf("files: got %+v", c.Files)
	}
}

func TestPullAbapGitRepo_MissingOptionalArrays(t *testing.T) {
	var rec postRecorder
	client := recordingServer(t, &rec, `{"status":"pulled","repo":{"name":"n","url":"u","package":"P"}}`)
	res, err := client.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{Repo: "r"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Log) != 0 || len(res.ConfirmationsRequired) != 0 {
		t.Errorf("got %+v, want empty slices", res)
	}
}

func TestPushAbapGitRepo_RequestShape(t *testing.T) {
	var rec postRecorder
	client := recordingServer(t, &rec, `{"status":"dry_run"}`)
	req := adt.AbapGitPushRequest{
		Repo: "r", Objects: []adt.AbapGitObjectRef{{ObjType: "PROG", ObjName: "ZEXAMPLE"}}, Message: "msg", DryRun: true,
	}
	if _, err := client.PushAbapGitRepo(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	rec.assertPost(t, "/sap/bc/adt/abapgitsync/push")
	want := jsonMap(t, `{"repo":"r","objects":[{"obj_type":"PROG","obj_name":"ZEXAMPLE"}],`+
		`"message":"msg","dry_run":true}`)
	if !reflect.DeepEqual(rec.body, want) {
		t.Errorf("body: got %v, want %v", rec.body, want)
	}

	req.DryRun = false
	if _, err := client.PushAbapGitRepo(context.Background(), req); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v, ok := rec.body["dry_run"]; !ok || v != false {
		t.Errorf("dry_run must be sent explicitly as false, body: %v", rec.body)
	}
}

func TestPushAbapGitRepo_Statuses(t *testing.T) {
	cases := []struct {
		name, body, status, commit string
		files                      int
	}{
		{"pushed", `{"status":"pushed","commit":"abc123","author":{"name":"A","email":"a@example.invalid"},` +
			`"files":[{"path":"/src/","filename":"zexample.prog.abap","action":"add"}]}`,
			adt.AbapGitStatusPushed, "abc123", 1},
		{"dry_run", `{"status":"dry_run","files":[{"path":"/src/","filename":"x.prog.abap","action":"rm"}]}`,
			adt.AbapGitStatusDryRun, "", 1},
		{"nothing_to_push", `{"status":"nothing_to_push","files":[]}`, adt.AbapGitStatusNothingToPush, "", 0},
	}
	req := adt.AbapGitPushRequest{
		Repo: "r", Objects: []adt.AbapGitObjectRef{{ObjType: "PROG", ObjName: "ZEXAMPLE"}}, Message: "msg",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec postRecorder
			client := recordingServer(t, &rec, tc.body)
			res, err := client.PushAbapGitRepo(context.Background(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Status != tc.status || res.Commit != tc.commit || len(res.Files) != tc.files {
				t.Errorf("got %+v, want status %q commit %q files %d", res, tc.status, tc.commit, tc.files)
			}
			if tc.name == "pushed" &&
				(res.Author != (adt.AbapGitAuthor{Name: "A", Email: "a@example.invalid"}) || res.Files[0].Action != "add") {
				t.Errorf("author/files: got %+v", res)
			}
		})
	}
}

func TestAbapGitSync_ClientSideValidation(t *testing.T) {
	obj := []adt.AbapGitObjectRef{{ObjType: "PROG", ObjName: "ZEXAMPLE"}}
	cases := []struct {
		name string
		call func(adt.Client) error
	}{
		{"pull without repo", func(c adt.Client) error {
			_, err := c.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{})
			return err
		}},
		{"push without repo", func(c adt.Client) error {
			_, err := c.PushAbapGitRepo(context.Background(), adt.AbapGitPushRequest{Objects: obj, Message: "m"})
			return err
		}},
		{"push without objects", func(c adt.Client) error {
			_, err := c.PushAbapGitRepo(context.Background(), adt.AbapGitPushRequest{Repo: "r", Message: "m"})
			return err
		}},
		{"push without message", func(c adt.Client) error {
			_, err := c.PushAbapGitRepo(context.Background(), adt.AbapGitPushRequest{Repo: "r", Objects: obj})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec postRecorder
			client := recordingServer(t, &rec, `{}`)
			if err := tc.call(client); err == nil {
				t.Fatal("want a validation error, got nil")
			}
			if rec.calls != 0 {
				t.Errorf("server saw %d calls, want 0", rec.calls)
			}
		})
	}
}

func TestAbapGitSyncError_DetailsTypeMismatchStillSyncError(t *testing.T) {
	client := abapGitSyncServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(contentTypeHdr, jsonMIME)
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`{"code":"REMOTE_CHANGED","message":"m","details":[{"x":1}]}`))
	})
	_, err := client.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{Repo: "r"})
	var syncErr *adt.AbapGitSyncError
	if !errors.As(err, &syncErr) {
		t.Fatalf("got %v (%T), want *AbapGitSyncError", err, err)
	}
	if syncErr.Code != adt.AbapGitErrRemoteChanged {
		t.Errorf("Code: got %q, want %q", syncErr.Code, adt.AbapGitErrRemoteChanged)
	}
}
