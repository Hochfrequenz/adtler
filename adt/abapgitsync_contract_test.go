package adt_test

// Contract test between adtler and the SAP-side abapGit sync companion
// (repository Hochfrequenz/Z_ABAPGIT_PULL_MCP_SHORTCUT).
//
// The JSON strings below are copied from the companion's ABAP unit test class
// ltcl_json (src/zcl_abapgit_mcp_json.clas.testclasses.abap, ABAP string
// templates reassembled), and the member names come from its Simple
// Transformations (src/zabapgit_mcp_*.xslt.source.xml). The ABAP tests pin the
// companion to these shapes, this file pins adtler to the same shapes. If
// either side changes a member name, an optional member or a JSON type, the
// strings in both repositories must change together, otherwise one of the two
// test suites fails.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

const (
	// ltcl_json=>repos_use_contract_names
	contractReposJSON = `{"repos":[{"key":"000000000001","name":"example","url":"https://github.com/example/repo",` +
		`"package":"ZEXAMPLE","branch":"refs/heads/main","offline":false,` +
		`"deserialized_at":"2026-10-06T07:42:34Z","deserialized_by":"DEVUSER"}]}`

	// ltcl_json=>pull_result_with_files
	contractPullResultJSON = `{"status":"needs_confirmation","repo":{"name":"example","url":"u","package":"ZEXAMPLE"},` +
		`"confirmations_required":[{"obj_type":"CLAS","obj_name":"ZCL_EXAMPLE","action":"delete",` +
		`"text":"Delete local object","files":[{"path":"/src/","filename":"zcl_example.clas.abap","state":"MD"}]}],` +
		`"log":[]}`

	// ltcl_json=>push_result_commit_optional, first assertion: no commit member at all.
	contractPushNothingJSON = `{"status":"nothing_to_push","author":{"name":"n","email":"e"},"files":[]}`

	// ltcl_json=>push_result_commit_optional, second assertion only pins the fragment "commit":"0123abc".
	// The surrounding members follow zabapgit_mcp_push_res.xslt.source.xml (status, commit, author, files).
	contractPushPushedJSON = `{"status":"pushed","commit":"0123abc","author":{"name":"n","email":"e"},` +
		`"files":[{"path":"/src/","filename":"zexample.prog.abap","action":"add"}]}`

	// ltcl_json=>error_with_details
	contractErrorJSON = `{"code":"REMOTE_CHANGED","message":"pull first","details":["/src/zexample.prog.abap _M"]}`

	// ltcl_json=>pull_request_any_order and ltcl_json=>push_request_dry_run (requests the companion accepts)
	contractPullRequestJSON = `{"confirm":[{"action":"overwrite","obj_name":"ZCL_EXAMPLE","obj_type":"CLAS"}],` +
		`"transport":"<request>","repo":"example"}`
	contractPushRequestJSON = `{"repo":"example","objects":[{"obj_type":"PROG","obj_name":"ZEXAMPLE"}],` +
		`"message":"msg","dry_run":true}`
)

// Members the companion's request transformations accept
// (zabapgit_mcp_pull_req and zabapgit_mcp_push_req); anything else is BAD_REQUEST.
var (
	contractPullMembers    = []string{"repo", "transport", "confirm"}
	contractConfirmMembers = []string{"obj_type", "obj_name", "action"}
	contractPushMembers    = []string{"repo", "objects", "message", "dry_run"}
	contractObjectMembers  = []string{"obj_type", "obj_name"}
)

// contractClient answers every companion call with status and body and records the request body.
func contractClient(t *testing.T, status int, body string) (adt.Client, *postRecorder) {
	t.Helper()
	rec := &postRecorder{}
	client := abapGitSyncServer(t, func(w http.ResponseWriter, r *http.Request) {
		rec.calls++
		rec.method, rec.path = r.Method, r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		rec.body = nil
		_ = json.Unmarshal(raw, &rec.body)
		w.Header().Set(contentTypeHdr, jsonMIME)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	return client, rec
}

func assertMembers(t *testing.T, where string, m map[string]any, allowed []string) {
	t.Helper()
	for k := range m {
		if !slices.Contains(allowed, k) {
			t.Errorf("%s: member %q is not accepted by the companion (allowed: %v)", where, k, allowed)
		}
	}
}

// assertNoNull fails for any JSON null below v; the companion's transformation rejects null.
func assertNoNull(t *testing.T, path string, v any) {
	t.Helper()
	switch x := v.(type) {
	case nil:
		t.Errorf("%s is JSON null, the companion rejects null", path)
	case map[string]any:
		for k, e := range x {
			assertNoNull(t, path+"."+k, e)
		}
	case []any:
		for _, e := range x {
			assertNoNull(t, path+"[]", e)
		}
	}
}

func objectList(t *testing.T, m map[string]any, key string) []map[string]any {
	t.Helper()
	arr, ok := m[key].([]any)
	if !ok {
		t.Fatalf("%q is not a JSON array: %#v", key, m[key])
	}
	out := make([]map[string]any, 0, len(arr))
	for _, e := range arr {
		o, ok := e.(map[string]any)
		if !ok {
			t.Fatalf("%q element is not a JSON object: %#v", key, e)
		}
		out = append(out, o)
	}
	return out
}

// strictDecode decodes s into v and fails on members v does not have.
func strictDecode(t *testing.T, s string, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("companion JSON does not fit adtler's struct: %v", err)
	}
}

func TestContract_ReposResponse(t *testing.T) {
	client, _ := contractClient(t, http.StatusOK, contractReposJSON)
	list, err := client.ListAbapGitRepos(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := adt.AbapGitRepoList{Count: 1, Repos: []adt.AbapGitRepo{{
		Key: "000000000001", Name: "example", URL: "https://github.com/example/repo", Package: "ZEXAMPLE",
		Branch: "refs/heads/main", Offline: false, DeserializedAt: "2026-10-06T07:42:34Z", DeserializedBy: "DEVUSER",
	}}}
	if !reflect.DeepEqual(*list, want) {
		t.Errorf("got %+v, want %+v", *list, want)
	}
}

func TestContract_PullResponse(t *testing.T) {
	client, _ := contractClient(t, http.StatusOK, contractPullResultJSON)
	res, err := client.PullAbapGitRepo(context.Background(), adt.AbapGitPullRequest{Repo: "example"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := adt.AbapGitPullResult{
		Status: adt.AbapGitStatusNeedsConfirmation,
		Repo:   adt.AbapGitRepoRef{Name: "example", URL: "u", Package: "ZEXAMPLE"},
		ConfirmationsRequired: []adt.AbapGitConfirmationRequired{{
			ObjType: "CLAS", ObjName: "ZCL_EXAMPLE", Action: "delete", Text: "Delete local object",
			Files: []adt.AbapGitFileState{{Path: "/src/", Filename: "zcl_example.clas.abap", State: "MD"}},
		}},
		Log: []adt.AbapGitLogEntry{},
	}
	if !reflect.DeepEqual(*res, want) {
		t.Errorf("got %+v, want %+v", *res, want)
	}
}

func contractPushRequest() adt.AbapGitPushRequest {
	return adt.AbapGitPushRequest{
		Repo: "example", Message: "msg", Objects: []adt.AbapGitObjectRef{{ObjType: "PROG", ObjName: "ZEXAMPLE"}},
	}
}

func TestContract_PushResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
		want adt.AbapGitPushResult
	}{
		{"commit member absent", contractPushNothingJSON, adt.AbapGitPushResult{
			Status: adt.AbapGitStatusNothingToPush, Author: adt.AbapGitAuthor{Name: "n", Email: "e"},
			Files: []adt.AbapGitPushFile{},
		}},
		{"commit present", contractPushPushedJSON, adt.AbapGitPushResult{
			Status: adt.AbapGitStatusPushed, Commit: "0123abc", Author: adt.AbapGitAuthor{Name: "n", Email: "e"},
			Files: []adt.AbapGitPushFile{{Path: "/src/", Filename: "zexample.prog.abap", Action: "add"}},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, _ := contractClient(t, http.StatusOK, tc.body)
			res, err := client.PushAbapGitRepo(context.Background(), contractPushRequest())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(*res, tc.want) {
				t.Errorf("got %+v, want %+v", *res, tc.want)
			}
		})
	}
}

func TestContract_ErrorWithDetails(t *testing.T) {
	client, _ := contractClient(t, http.StatusConflict, contractErrorJSON)
	_, err := client.PushAbapGitRepo(context.Background(), contractPushRequest())
	var syncErr *adt.AbapGitSyncError
	if !errors.As(err, &syncErr) {
		t.Fatalf("got %v, want *AbapGitSyncError", err)
	}
	want := adt.AbapGitSyncError{
		HTTPStatus: http.StatusConflict, Code: adt.AbapGitErrRemoteChanged, Message: "pull first",
		Details: []string{"/src/zexample.prog.abap _M"},
	}
	if !reflect.DeepEqual(*syncErr, want) {
		t.Errorf("got %+v, want %+v", *syncErr, want)
	}
}

func TestContract_PullRequestMembers(t *testing.T) {
	requests := map[string]adt.AbapGitPullRequest{
		"minimal": {Repo: "example"},
		"full": {Repo: "example", Transport: "<request>", Confirm: []adt.AbapGitConfirmation{
			{ObjType: "CLAS", ObjName: "ZCL_EXAMPLE", Action: "overwrite"}}},
	}
	for name, req := range requests {
		t.Run(name, func(t *testing.T) {
			client, rec := contractClient(t, http.StatusOK, contractPullResultJSON)
			if _, err := client.PullAbapGitRepo(context.Background(), req); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertMembers(t, "pull request", rec.body, contractPullMembers)
			assertNoNull(t, "pull request", rec.body)
			if rec.body["repo"] != "example" {
				t.Errorf("repo: got %#v, want \"example\"", rec.body["repo"])
			}
			if name == "full" {
				confirm := objectList(t, rec.body, "confirm")
				if len(confirm) != 1 {
					t.Fatalf("confirm: got %d entries, want 1", len(confirm))
				}
				assertMembers(t, "pull request confirm[]", confirm[0], contractConfirmMembers)
			}
		})
	}
}

func TestContract_PushRequestMembers(t *testing.T) {
	for _, dry := range []bool{true, false} {
		client, rec := contractClient(t, http.StatusOK, contractPushNothingJSON)
		req := contractPushRequest()
		req.DryRun = dry
		if _, err := client.PushAbapGitRepo(context.Background(), req); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertMembers(t, "push request", rec.body, contractPushMembers)
		assertNoNull(t, "push request", rec.body)
		objects := objectList(t, rec.body, "objects")
		if len(objects) != 1 {
			t.Fatalf("objects: got %d entries, want 1", len(objects))
		}
		assertMembers(t, "push request objects[]", objects[0], contractObjectMembers)
		if got, ok := rec.body["dry_run"].(bool); !ok || got != dry {
			t.Errorf("dry_run: got %#v, want JSON boolean %v", rec.body["dry_run"], dry)
		}
	}
}

// The companion's own sample requests must decode into adtler's request structs
// without an unknown member and with every value in the right field.
func TestContract_CompanionRequestsDecodeIntoAdtlerStructs(t *testing.T) {
	var pull adt.AbapGitPullRequest
	strictDecode(t, contractPullRequestJSON, &pull)
	wantPull := adt.AbapGitPullRequest{Repo: "example", Transport: "<request>", Confirm: []adt.AbapGitConfirmation{
		{ObjType: "CLAS", ObjName: "ZCL_EXAMPLE", Action: "overwrite"}}}
	if !reflect.DeepEqual(pull, wantPull) {
		t.Errorf("pull: got %+v, want %+v", pull, wantPull)
	}

	var push adt.AbapGitPushRequest
	strictDecode(t, contractPushRequestJSON, &push)
	wantPush := contractPushRequest()
	wantPush.DryRun = true
	if !reflect.DeepEqual(push, wantPush) {
		t.Errorf("push: got %+v, want %+v", push, wantPush)
	}
}
