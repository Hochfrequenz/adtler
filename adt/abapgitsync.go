package adt

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// abapGitSyncBasePath is the root of the abapGit sync companion's ADT endpoints.
const abapGitSyncBasePath = "/sap/bc/adt/abapgitsync"

// contentTypeJSON is the Accept / Content-Type of the companion endpoints.
const contentTypeJSON = "application/json"

// maxAbapGitSyncErrorBody caps how much of an error body is read.
const maxAbapGitSyncErrorBody = 1 << 20

// Status values of AbapGitPullResult and AbapGitPushResult.
const (
	AbapGitStatusPulled            = "pulled"
	AbapGitStatusNeedsConfirmation = "needs_confirmation"
	AbapGitStatusPushed            = "pushed"
	AbapGitStatusDryRun            = "dry_run"
	AbapGitStatusNothingToPush     = "nothing_to_push"
)

// Error codes the companion reports in AbapGitSyncError.Code.
const (
	AbapGitErrRepoNotFound        = "REPO_NOT_FOUND"
	AbapGitErrRepoAmbiguous       = "REPO_AMBIGUOUS"
	AbapGitErrObjectNotInRepo     = "OBJECT_NOT_IN_REPO"
	AbapGitErrCredentialsMissing  = "CREDENTIALS_MISSING"
	AbapGitErrCredentialsRejected = "CREDENTIALS_REJECTED"
	AbapGitErrTransportRequired   = "TRANSPORT_REQUIRED"
	AbapGitErrNoModifiableTask    = "NO_MODIFIABLE_TASK"
	AbapGitErrRequirementsNotMet  = "REQUIREMENTS_NOT_MET"
	AbapGitErrRemoteChanged       = "REMOTE_CHANGED"
	AbapGitErrGitError            = "GIT_ERROR"
	AbapGitErrInternal            = "INTERNAL"
)

// AbapGitRepo is one abapGit repository known to the companion.
type AbapGitRepo struct {
	Key            string `json:"key"`
	Name           string `json:"name"`
	URL            string `json:"url"`
	Package        string `json:"package"`
	Branch         string `json:"branch"`
	Offline        bool   `json:"offline"`
	DeserializedAt string `json:"deserialized_at"`
	DeserializedBy string `json:"deserialized_by"`
}

// AbapGitRepoList is the result of ListAbapGitRepos.
type AbapGitRepoList struct {
	Count int           `json:"count"`
	Repos []AbapGitRepo `json:"repos"`
}

// AbapGitObjectRef identifies one object in a push request.
type AbapGitObjectRef struct {
	ObjType string `json:"obj_type"`
	ObjName string `json:"obj_name"`
}

// AbapGitConfirmation confirms an overwrite or delete the companion asked for.
type AbapGitConfirmation struct {
	ObjType string `json:"obj_type"`
	ObjName string `json:"obj_name"`
	Action  string `json:"action"`
}

// AbapGitPullRequest is the body of a pull call.
type AbapGitPullRequest struct {
	Repo      string                `json:"repo"`
	Transport string                `json:"transport,omitempty"`
	Confirm   []AbapGitConfirmation `json:"confirm,omitempty"`
}

// AbapGitFileState is the state of one file of an object awaiting confirmation.
type AbapGitFileState struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	State    string `json:"state"`
}

// AbapGitConfirmationRequired describes an object the companion will only touch after confirmation.
type AbapGitConfirmationRequired struct {
	ObjType string             `json:"obj_type"`
	ObjName string             `json:"obj_name"`
	Action  string             `json:"action"`
	Text    string             `json:"text"`
	Files   []AbapGitFileState `json:"files"`
}

// AbapGitLogEntry is one line of the companion's pull log.
type AbapGitLogEntry struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	ObjType string `json:"obj_type,omitempty"`
	ObjName string `json:"obj_name,omitempty"`
}

// AbapGitRepoRef names the repository a result belongs to.
type AbapGitRepoRef struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Package string `json:"package"`
}

// AbapGitPullResult is the result of a pull call.
type AbapGitPullResult struct {
	Status                string                        `json:"status"`
	Repo                  AbapGitRepoRef                `json:"repo"`
	ConfirmationsRequired []AbapGitConfirmationRequired `json:"confirmations_required"`
	Log                   []AbapGitLogEntry             `json:"log"`
}

// AbapGitPushRequest is the body of a push call.
type AbapGitPushRequest struct {
	Repo    string             `json:"repo"`
	Objects []AbapGitObjectRef `json:"objects"`
	Message string             `json:"message"`
	DryRun  bool               `json:"dry_run"`
}

// AbapGitAuthor is the commit author the companion used.
type AbapGitAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// AbapGitPushFile is one file of a push commit.
type AbapGitPushFile struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Action   string `json:"action"` // "add" | "rm"
}

// AbapGitPushResult is the result of a push call.
type AbapGitPushResult struct {
	Status string            `json:"status"`
	Commit string            `json:"commit,omitempty"`
	Author AbapGitAuthor     `json:"author"`
	Files  []AbapGitPushFile `json:"files"`
}

// AbapGitSyncError is an error answered by the companion itself.
// It deliberately does not wrap *ADTError.
type AbapGitSyncError struct {
	HTTPStatus int      `json:"-"`
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	Details    []string `json:"details,omitempty"`
}

// Error implements the error interface as "<CODE>: <message>".
func (e *AbapGitSyncError) Error() string {
	return e.Code + ": " + e.Message
}

// ErrAbapGitSyncNotInstalled is returned when ADT answers 404 without a companion error body.
var ErrAbapGitSyncNotInstalled = errors.New("abapGit sync companion is not installed on this system (Z_ABAPGIT_PULL_MCP_SHORTCUT)")

// ListAbapGitRepos lists the abapGit repositories known to the companion.
func (c *httpClient) ListAbapGitRepos(ctx context.Context) (*AbapGitRepoList, error) {
	ctx, cancel := withDefaultDeadline(ctx)
	defer cancel()
	resp, err := c.doReadLong(ctx, abapGitSyncBasePath+"/repos", map[string]string{"Accept": contentTypeJSON})
	if err != nil {
		return nil, fmt.Errorf("ListAbapGitRepos: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out AbapGitRepoList
	if err := decodeAbapGitSyncResponse("ListAbapGitRepos", resp, &out); err != nil {
		return nil, err
	}
	out.Count = len(out.Repos)
	return &out, nil
}

// PullAbapGitRepo is implemented in a later change.
func (c *httpClient) PullAbapGitRepo(context.Context, AbapGitPullRequest) (*AbapGitPullResult, error) {
	return nil, errors.New("not implemented")
}

// PushAbapGitRepo is implemented in a later change.
func (c *httpClient) PushAbapGitRepo(context.Context, AbapGitPushRequest) (*AbapGitPushResult, error) {
	return nil, errors.New("not implemented")
}

// decodeAbapGitSyncResponse decodes a companion response into out, or returns
// the mapped error. op is used as error prefix.
//
// For status >= 400 a JSON body carrying a non-empty "code" becomes an
// *AbapGitSyncError (wrapped with op, so errors.As finds it). Otherwise a 404
// means the companion is not installed, and anything else is parsed as a
// regular ADT error.
func decodeAbapGitSyncResponse(op string, resp *http.Response, out any) error {
	if resp.StatusCode < http.StatusBadRequest {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("%s: decoding response: %w", op, err)
		}
		return nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxAbapGitSyncErrorBody))
	var syncErr AbapGitSyncError
	if json.Unmarshal(body, &syncErr) == nil && syncErr.Code != "" {
		syncErr.HTTPStatus = resp.StatusCode
		return fmt.Errorf("%s: %w", op, &syncErr)
	}
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s: %w", op, ErrAbapGitSyncNotInstalled)
	}
	return fmt.Errorf("%s: %w", op, parseADTError(resp.StatusCode, bytes.NewReader(body)))
}
