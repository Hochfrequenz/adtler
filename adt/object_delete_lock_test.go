package adt_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// deleteLockObjectURI and deleteLockHandle are shared by the lock-release
// tests below. Hoisted for goconst.
const (
	deleteLockObjectURI = programsEndpoint + "/ZLOCKED"
	deleteLockHandle    = "handle+with/special=chars"
	deleteLockEventDel  = "delete"
	// actionUnlock is the _action query value of the UNLOCK request.
	actionUnlock = "UNLOCK"
)

// lockAwareDeleteServer mimics the S/4HANA behaviour from adtler#187: while
// the caller's own lock is held, a DELETE is refused with 403
// ExceptionResourceNoAccess ("is currently editing"). The lock is released by
// the UNLOCK action. unlockCode lets a test make the UNLOCK itself fail.
type lockAwareDeleteServer struct {
	*httptest.Server
	mu         sync.Mutex
	locked     bool
	events     []string // "unlock:<handle>" and "delete", in arrival order
	deleted    bool
	unlockCode int
}

func newLockAwareDeleteServer(locked bool) *lockAwareDeleteServer {
	s := &lockAwareDeleteServer{locked: locked, unlockCode: http.StatusOK}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		switch {
		case r.URL.Path == csrfEndpoint:
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			w.Header().Set("ETag", "etag-1")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<program:abapProgram xmlns:program="http://www.sap.com/adt/programs/programs" xmlns:adtcore="http://www.sap.com/adt/core" adtcore:name="ZLOCKED" adtcore:type="PROG/P"/>`))
		case r.URL.Query().Get("_action") == actionUnlock:
			s.events = append(s.events, "unlock:"+r.URL.Query().Get("lockHandle"))
			if s.unlockCode != http.StatusOK {
				w.WriteHeader(s.unlockCode)
				_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionInvalidLockHandle"/><message lang="EN">Invalid lock handle</message></exc:exception>`))
				return
			}
			s.locked = false
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete:
			s.events = append(s.events, deleteLockEventDel)
			if s.locked {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<exc:exception xmlns:exc="http://www.sap.com/abapxml/types/communicationframework"><type id="ExceptionResourceNoAccess"/><message lang="EN">User USERA is currently editing ZLOCKED</message></exc:exception>`))
				return
			}
			s.deleted = true
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return s
}

func (s *lockAwareDeleteServer) client() adt.Client {
	return adt.NewClient(sapmcpconfig.SAPSystem{Host: s.URL, User: "U", Password: "P", Client: "100"})
}

// TestDeleteObject_ReleasesCallerLockBeforeDeleting is the regression test for
// adtler#187. The DELETE runs in a different SAP session than the caller's
// LockObject, so on S/4HANA the caller's own lock blocks it. A caller that
// passes its handle, as the signature invites, must therefore have the lock
// released first.
func TestDeleteObject_ReleasesCallerLockBeforeDeleting(t *testing.T) {
	srv := newLockAwareDeleteServer(true)
	defer srv.Close()

	if err := srv.client().DeleteObject(context.Background(), deleteLockObjectURI, deleteLockHandle, ""); err != nil {
		t.Fatalf("DeleteObject with a held lock: %v", err)
	}
	if !srv.deleted {
		t.Fatal("object was not deleted")
	}
	want := []string{"unlock:" + deleteLockHandle, deleteLockEventDel}
	if strings.Join(srv.events, ",") != strings.Join(want, ",") {
		t.Errorf("request order: got %v, want %v", srv.events, want)
	}
}

// With no handle there is nothing to release: DeleteObject must not send an
// UNLOCK request, which would be a pointless round trip and would fail on a
// server that never saw a lock.
func TestDeleteObject_WithoutLockHandleSendsNoUnlock(t *testing.T) {
	srv := newLockAwareDeleteServer(false)
	defer srv.Close()

	if err := srv.client().DeleteObject(context.Background(), deleteLockObjectURI, "", ""); err != nil {
		t.Fatalf("DeleteObject: %v", err)
	}
	if len(srv.events) != 1 || srv.events[0] != deleteLockEventDel {
		t.Errorf("request order: got %v, want only [delete]", srv.events)
	}
}

// A handle that is already released (the caller called UnlockObject and still
// passes the handle along, which several callers do) makes the UNLOCK fail.
// That must not block the delete: nothing is held, so the DELETE decides the
// outcome.
func TestDeleteObject_StaleLockHandleDoesNotBlockDelete(t *testing.T) {
	srv := newLockAwareDeleteServer(false)
	srv.unlockCode = http.StatusBadRequest
	defer srv.Close()

	if err := srv.client().DeleteObject(context.Background(), deleteLockObjectURI, deleteLockHandle, ""); err != nil {
		t.Fatalf("DeleteObject with a stale handle: %v", err)
	}
	if !srv.deleted {
		t.Fatal("object was not deleted")
	}
}

// If the UNLOCK failed and the DELETE then fails too, the caller must see the
// DELETE error unchanged (errors.As still finds the ADTError) and also learn
// that releasing the lock failed, because that is the likely cause when the
// lock is really still held.
func TestDeleteObject_FailedUnlockIsReportedWithTheDeleteError(t *testing.T) {
	srv := newLockAwareDeleteServer(true)
	srv.unlockCode = http.StatusBadRequest
	defer srv.Close()

	err := srv.client().DeleteObject(context.Background(), deleteLockObjectURI, deleteLockHandle, "")
	if err == nil {
		t.Fatal("expected an error: the lock could not be released, so the DELETE is refused")
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) || adtErr.Type != "ExceptionResourceNoAccess" {
		t.Errorf("want the DELETE's ADTError ExceptionResourceNoAccess reachable with errors.As, got: %v", err)
	}
	if !strings.Contains(err.Error(), "releas") || !strings.Contains(err.Error(), "Invalid lock handle") {
		t.Errorf("error should mention the failed lock release and its reason, got: %v", err)
	}
}
