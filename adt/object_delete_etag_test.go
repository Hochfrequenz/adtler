package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

const (
	// deletePackageURI is how a caller addresses a local package: the dollar
	// sign is URL-encoded, as in adtler#150.
	deletePackageURI = "/sap/bc/adt/packages/%24zexample"
	// deleteReadETag is what a GET of the package returns. The ETag the server
	// compares a DELETE against differs in the version digits that precede the
	// media type (001 against 000), as measured on SAP S/4HANA (adtler#150), so
	// no ETag read from a GET can ever match.
	deleteReadETag = "20260101000000001text/html/AAAA"
	// deletePreconditionFailed is the ADT exception for a refused If-Match.
	deletePreconditionFailed = `<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionPreconditionFailed"/><message lang="EN">Client ETag does not match the object ETag in the server</message></exc:exception>`
	deletePackageXML         = `<pak:package xmlns:pak="http://www.sap.com/adt/packages" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="$ZEXAMPLE" adtcore:type="DEVC/K"/>`
)

// deleteMock mimics what adtler#150 measured on SAP S/4HANA: a DELETE that
// carries an If-Match header taken from a GET is refused with 412, because the
// server compares it against an ETag that differs in the version digits. A
// DELETE without If-Match goes through.
type deleteMock struct {
	*httptest.Server
	mu        sync.Mutex
	gets      int
	deletes   []deleteRequest // every DELETE, in order
	removed   bool
	refuseAll bool // the server refuses a DELETE without If-Match as well
}

type deleteRequest struct {
	sentIfMatch bool
	ifMatch     string
	corrNr      string
}

func newDeleteMock() *deleteMock {
	m := &deleteMock{}
	m.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			m.gets++
			w.Header().Set("ETag", deleteReadETag)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(deletePackageXML))
		case r.Method == http.MethodDelete:
			values := r.Header.Values("If-Match")
			m.deletes = append(m.deletes, deleteRequest{sentIfMatch: len(values) > 0, ifMatch: r.Header.Get("If-Match"), corrNr: r.URL.Query().Get("corrNr")})
			if len(values) > 0 || m.refuseAll {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(deletePreconditionFailed))
				return
			}
			m.removed = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return m
}

func (m *deleteMock) client() adt.Client {
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: m.URL, User: "U", Password: "P", Client: "100"})
}

// TestDeleteObject_Package_RetriesWithoutPreconditionOnETagMismatch is the
// regression test for adtler#150: deleting a package answered 412 because no
// ETag read from a GET matches the one the server compares a DELETE against.
// DeleteObject must try the ETag first, as before, and then delete without a
// precondition.
func TestDeleteObject_Package_RetriesWithoutPreconditionOnETagMismatch(t *testing.T) {
	srv := newDeleteMock()
	defer srv.Close()

	if err := srv.client().DeleteObject(context.Background(), deletePackageURI, "", ""); err != nil {
		t.Fatalf("DeleteObject of a package: %v", err)
	}
	if !srv.removed {
		t.Fatal("package was not deleted")
	}
	if len(srv.deletes) != 2 {
		t.Fatalf("DELETE requests: got %d, want 2 (refused once, then accepted)", len(srv.deletes))
	}
	if !srv.deletes[0].sentIfMatch || srv.deletes[0].ifMatch != deleteReadETag {
		t.Errorf("first DELETE must carry the ETag that was read, got %+v", srv.deletes[0])
	}
	if srv.deletes[1].sentIfMatch {
		t.Errorf("second DELETE must carry no If-Match, got %+v", srv.deletes[1])
	}
	if srv.gets != 1 {
		t.Errorf("ETag reads: got %d, want 1: the retry needs no second read", srv.gets)
	}
}

// The transport request that records the deletion stays on both DELETEs.
func TestDeleteObject_ETagMismatchRetryKeepsTheTransportRequest(t *testing.T) {
	const transport = "AAAK900001"
	srv := newDeleteMock()
	defer srv.Close()

	if err := srv.client().DeleteObject(context.Background(), deletePackageURI, "", transport); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if len(srv.deletes) != 2 {
		t.Fatalf("DELETE requests: got %d, want 2", len(srv.deletes))
	}
	for i, d := range srv.deletes {
		if d.corrNr != transport {
			t.Errorf("DELETE %d carried corrNr %q, want %q", i+1, d.corrNr, transport)
		}
	}
}

// The retry without a precondition is for packages only: the mismatch was
// measured for packages, and for any other object type a 412 may report a real
// concurrent change, which must reach the caller. One DELETE, with the ETag, and
// the 412 is returned.
func TestDeleteObject_ETagMismatchOfOtherTypesIsNotRetried(t *testing.T) {
	srv := newDeleteMock()
	defer srv.Close()

	err := srv.client().DeleteObject(context.Background(), programsEndpoint+"/ZEXAMPLE", "", "")
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.Type != "ExceptionPreconditionFailed" {
		t.Fatalf("want the original ExceptionPreconditionFailed, got: %v", err)
	}
	if srv.removed {
		t.Error("the program must not be deleted behind a refused ETag")
	}
	if len(srv.deletes) != 1 || !srv.deletes[0].sentIfMatch {
		t.Errorf("DELETE requests: got %+v, want exactly one, carrying the ETag", srv.deletes)
	}
}

// If SAP refuses the DELETE without a precondition as well, that refusal is
// the answer. The caller must see it as the ADTError it is, with no further
// retries.
func TestDeleteObject_Package_SecondRefusalIsReturned(t *testing.T) {
	srv := newDeleteMock()
	srv.refuseAll = true
	defer srv.Close()

	err := srv.client().DeleteObject(context.Background(), deletePackageURI, "", "")
	if err == nil {
		t.Fatal("expected an error: the server refuses every DELETE")
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.Type != "ExceptionPreconditionFailed" {
		t.Errorf("want an ADTError ExceptionPreconditionFailed, got: %v", err)
	}
	if len(srv.deletes) != 2 {
		t.Errorf("DELETE requests: got %d, want exactly 2 (no retry loop)", len(srv.deletes))
	}
}

// A refusal that is not an ETag mismatch, such as a missing authorization,
// must not trigger a precondition-free retry: dropping the precondition cannot
// help, and it must not be a way around a refusal.
func TestDeleteObject_OtherRefusalIsNotRetried(t *testing.T) {
	var deletes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			w.Header().Set("ETag", "etag-1")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(deletePackageXML))
		case r.Method == http.MethodDelete:
			deletes++
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionInvalidData"/><message lang="EN">Invalid input</message></exc:exception>`))
		}
	}))
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	err := client.DeleteObject(context.Background(), deletePackageURI, "", "")
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("want the original 400, got: %v", err)
	}
	if deletes != 1 {
		t.Errorf("DELETE requests: got %d, want 1", deletes)
	}
}

// An object that deletes on the first try must cost exactly one read and one
// DELETE, as before: the retry is for an ETag mismatch only.
func TestDeleteObject_AcceptedFirstTimeCostsOneReadAndOneDelete(t *testing.T) {
	var gets, deletes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			gets++
			w.Header().Set("ETag", "etag-1")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="ZONCE" adtcore:type="PROG/P"/>`))
		case r.Method == http.MethodDelete:
			deletes++
			if r.Header.Get("If-Match") != "etag-1" {
				t.Errorf("DELETE must carry the ETag that was read, got %q", r.Header.Get("If-Match"))
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	client := adt.NewClient(sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"})

	if err := client.DeleteObject(context.Background(), programsEndpoint+"/ZONCE", "", ""); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if gets != 1 || deletes != 1 {
		t.Errorf("requests: got %d reads and %d deletes, want 1 and 1", gets, deletes)
	}
}
