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
	"sync"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config/v2"
)

const (
	deleteTestNumber = "AAAK900123"
	deleteTestOwner  = "USERA"
	deleteTestDesc   = "Example request"

	// The order in which deleteTransportServer saw the calls.
	callRead   = "read"
	callQuery  = "query"
	callDelete = "delete"
)

// deleteTestRequestXML is a transportorganizer.v1 body for one modifiable
// workbench request with two tasks, in the shape both releases answer with.
func deleteTestRequestXML(number string) string {
	return `<?xml version="1.0" encoding="utf-8"?>` +
		`<tm:root xmlns:tm="http://www.sap.com/cts/adt/tm">` +
		`<tm:request tm:number="` + number + `" tm:parent="" tm:owner="USERA" tm:desc="` + deleteTestDesc + `" tm:type="K" tm:status="D" tm:lastchanged_timestamp="20261105123456">` +
		`<tm:task tm:number="AAAK900124" tm:parent="` + number + `" tm:owner="USERA" tm:desc="Example task one" tm:type="Development/Correction" tm:status="D"/>` +
		`<tm:task tm:number="AAAK900125" tm:parent="` + number + `" tm:owner="USERB" tm:desc="Example task two" tm:type="Unclassified" tm:status="R"/>` +
		`</tm:request></tm:root>`
}

// deleteTestCreationRows is the E070CREATE answer for deleteTestNumber.
var deleteTestCreationRows = [][]string{{deleteTestNumber, "USERA", "20261104", "101112"}}

var deleteTestCreationColumns = []string{"TRKORR", "CRE_USER", "CRE_DATE", "CRE_TIME"}

// deleteTransportServer plays the three endpoints DeleteTransport talks to and
// records what it was asked, in order. A zero status means 200.
type deleteTransportServer struct {
	readStatus    int
	readBody      string
	previewStatus int
	previewRows   [][]string
	deleteStatus  int

	mu    sync.Mutex
	calls []string
	sql   []string
}

func (s *deleteTransportServer) client(t *testing.T) adt.Client {
	t.Helper()
	status := func(code int) int {
		if code == 0 {
			return http.StatusOK
		}
		return code
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "tok")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/sap/bc/adt/datapreview/freestyle":
			body, _ := io.ReadAll(r.Body)
			s.calls = append(s.calls, callQuery)
			s.sql = append(s.sql, string(body))
			w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
			w.WriteHeader(status(s.previewStatus))
			if status(s.previewStatus) == http.StatusOK {
				_, _ = w.Write([]byte(dataPreviewXML(deleteTestCreationColumns, s.previewRows)))
			}
		case strings.HasPrefix(r.URL.Path, "/sap/bc/adt/cts/transportrequests/") && r.Method == http.MethodGet:
			s.calls = append(s.calls, callRead)
			w.Header().Set("Content-Type", "application/vnd.sap.adt.transportorganizer.v1+xml")
			w.WriteHeader(status(s.readStatus))
			_, _ = w.Write([]byte(s.readBody))
		case strings.HasPrefix(r.URL.Path, "/sap/bc/adt/cts/transportrequests/") && r.Method == http.MethodDelete:
			s.calls = append(s.calls, callDelete)
			w.WriteHeader(status(s.deleteStatus))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})
}

func (s *deleteTransportServer) assertCalls(t *testing.T, want ...string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !reflect.DeepEqual(s.calls, want) {
		t.Errorf("calls: got %v, want %v", s.calls, want)
	}
}

func TestDeleteTransport_ReturnsDetailsRead_BeforeTheDelete(t *testing.T) {
	s := &deleteTransportServer{readBody: deleteTestRequestXML(deleteTestNumber), previewRows: deleteTestCreationRows}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.assertCalls(t, callRead, callQuery, callDelete)
	if got.DetailsErr != nil {
		t.Errorf("DetailsErr: got %v, want nil", got.DetailsErr)
	}
	if got.Number != deleteTestNumber || got.Owner != deleteTestOwner || got.Description != deleteTestDesc {
		t.Errorf("identity: got %q %q %q", got.Number, got.Owner, got.Description)
	}
	if got.Type != adt.TransportTypeWorkbench {
		t.Errorf("Type: got %q, want %q", got.Type, adt.TransportTypeWorkbench)
	}
	if got.State != adt.TransportStateModifiable || got.RawStatus != adt.TransportStatusModifiable {
		t.Errorf("State: got %v / %q", got.State, got.RawStatus)
	}
	if want := time.Date(2026, 11, 5, 12, 34, 56, 0, time.UTC); !got.LastChanged.Equal(want) {
		t.Errorf("LastChanged: got %v, want %v", got.LastChanged, want)
	}
	if want := time.Date(2026, 11, 4, 10, 11, 12, 0, time.UTC); !got.Created.Equal(want) {
		t.Errorf("Created: got %v, want %v", got.Created, want)
	}
	if got.CreatedBy != "USERA" {
		t.Errorf("CreatedBy: got %q, want USERA", got.CreatedBy)
	}
	wantTasks := []adt.TransportRequest{
		{Number: "AAAK900124", Owner: "USERA", Description: "Example task one", State: adt.TransportStateModifiable, RawStatus: "D"},
		{Number: "AAAK900125", Owner: "USERB", Description: "Example task two", State: adt.TransportStateReleased, RawStatus: "R"},
	}
	if !reflect.DeepEqual(got.Tasks, wantTasks) {
		t.Errorf("Tasks: got %+v, want %+v", got.Tasks, wantTasks)
	}
	if len(s.sql) != 1 || !strings.Contains(s.sql[0], "FROM E070CREATE") || !strings.Contains(s.sql[0], "TRKORR = '"+deleteTestNumber+"'") {
		t.Errorf("SQL: got %v", s.sql)
	}
}

func TestDeleteTransport_NoE070CreateRow_LeavesCreatedZero(t *testing.T) {
	s := &deleteTransportServer{readBody: deleteTestRequestXML(deleteTestNumber)}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.assertCalls(t, callRead, callQuery, callDelete)
	if got.DetailsErr != nil {
		t.Errorf("DetailsErr: got %v, want nil", got.DetailsErr)
	}
	if !got.Created.IsZero() || got.CreatedBy != "" {
		t.Errorf("Created: got %v by %q, want zero and empty", got.Created, got.CreatedBy)
	}
	if got.LastChanged.IsZero() {
		t.Error("LastChanged: got zero, want the ADT timestamp")
	}
}

func TestDeleteTransport_ADTReadFails_StillDeletes(t *testing.T) {
	s := &deleteTransportServer{readStatus: http.StatusInternalServerError, previewRows: deleteTestCreationRows}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.assertCalls(t, callRead, callQuery, callDelete)
	if got.DetailsErr == nil {
		t.Error("DetailsErr: got nil, want the failed read")
	}
	if got.Number != deleteTestNumber || got.Owner != "" {
		t.Errorf("got Number %q Owner %q, want the number only", got.Number, got.Owner)
	}
	if got.Created.IsZero() || got.CreatedBy != "USERA" {
		t.Errorf("Created: got %v by %q, want the E070CREATE row", got.Created, got.CreatedBy)
	}
	// A JSON consumer must not lose the reason: the error value itself is not marshalled.
	out, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"details_error"`) {
		t.Errorf("JSON lacks details_error: %s", out)
	}
}

func TestDeleteTransport_RequestAbsentFromAnswer_StillDeletes(t *testing.T) {
	// The normal ECC case for a released request: the answer is a worklist
	// that does not hold it.
	s := &deleteTransportServer{readBody: deleteTestRequestXML("AAAK909999"), previewRows: deleteTestCreationRows}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.assertCalls(t, callRead, callQuery, callDelete)
	if got.DetailsErr == nil {
		t.Error("DetailsErr: got nil, want the absent request")
	}
	if got.Owner != "" || got.CreatedBy != "USERA" {
		t.Errorf("got Owner %q CreatedBy %q, want no ADT fields and the E070CREATE row", got.Owner, got.CreatedBy)
	}
}

func TestDeleteTransport_DataPreviewFails_StillDeletes(t *testing.T) {
	s := &deleteTransportServer{readBody: deleteTestRequestXML(deleteTestNumber), previewStatus: http.StatusForbidden}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The client repeats a 403 once after fetching a new CSRF token, hence two queries.
	s.assertCalls(t, callRead, callQuery, callQuery, callDelete)
	if got.DetailsErr == nil {
		t.Error("DetailsErr: got nil, want the failed query")
	}
	if got.Owner != deleteTestOwner || len(got.Tasks) != 2 {
		t.Errorf("ADT fields: got Owner %q and %d tasks", got.Owner, len(got.Tasks))
	}
}

func TestDeleteTransport_RejectedCredentialOnRead_SendsNothingElse(t *testing.T) {
	s := &deleteTransportServer{readStatus: http.StatusUnauthorized, previewRows: deleteTestCreationRows}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	assertDeleteUnauthorized(t, got, err)
	s.assertCalls(t, callRead)
}

func TestDeleteTransport_RejectedCredentialOnQuery_DoesNotDelete(t *testing.T) {
	s := &deleteTransportServer{readBody: deleteTestRequestXML(deleteTestNumber), previewStatus: http.StatusUnauthorized}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	assertDeleteUnauthorized(t, got, err)
	s.assertCalls(t, callRead, callQuery)
}

func assertDeleteUnauthorized(t *testing.T, got *adt.DeletedTransport, err error) {
	t.Helper()
	if got != nil {
		t.Errorf("result: got %+v, want nil", got)
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("error: got %v, want an *ADTError with status 401", err)
	}
}

func TestDeleteTransport_DeleteFails_ReturnsError(t *testing.T) {
	s := &deleteTransportServer{readBody: deleteTestRequestXML(deleteTestNumber), previewRows: deleteTestCreationRows, deleteStatus: http.StatusInternalServerError}
	got, err := s.client(t).DeleteTransport(context.Background(), deleteTestNumber)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got != nil {
		t.Errorf("result: got %+v, want nil", got)
	}
	s.assertCalls(t, callRead, callQuery, callDelete)
}

func TestDeleteTransport_UnsafeNumber_SendsNoSQL(t *testing.T) {
	unsafe := "AAAK9'00123"
	// The ADT read succeeds, so the only reason for DetailsErr is the number.
	s := &deleteTransportServer{readBody: deleteTestRequestXML(unsafe)}
	got, err := s.client(t).DeleteTransport(context.Background(), unsafe)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s.assertCalls(t, callRead, callDelete)
	if got.DetailsErr == nil {
		t.Error("DetailsErr: got nil, want the refused number")
	}
}

func TestDeleteTransport_LowerCaseNumber_IsUpperCasedInSQL(t *testing.T) {
	s := &deleteTransportServer{readBody: deleteTestRequestXML(deleteTestNumber), previewRows: deleteTestCreationRows}
	if _, err := s.client(t).DeleteTransport(context.Background(), strings.ToLower(deleteTestNumber)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.sql) != 1 || !strings.Contains(s.sql[0], "TRKORR = '"+deleteTestNumber+"'") {
		t.Errorf("SQL: got %v, want the upper-case number", s.sql)
	}
}
