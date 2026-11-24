package adt_test

import (
	"context"
	"encoding/xml"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

const (
	addTask    = "AAAK900301"
	addPgmID   = "R3TR"
	addObjType = "PROG"
	addObjName = "ZTEST"
)

// addTaskXML is a modifiable S/4 task: SAP_BASIS 816 advertises addobject on
// every request and task that is not released.
var addTaskXML = copiesRequestXML(addTask, "D", "", "modify", "addobject", "removeobject")

func assertAddObjectUnsupported(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a not-supported error, got nil")
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
	}
	if adtErr.Type != adt.ExceptionTypeAddObjectUnsupported {
		t.Errorf("Type = %q, want %q", adtErr.Type, adt.ExceptionTypeAddObjectUnsupported)
	}
	if kind := adt.ClassifyError(err); kind != adt.ErrorNotSupported {
		t.Errorf("ClassifyError = %v, want %v", kind, adt.ErrorNotSupported)
	}
}

// TestAddToTransport_SendsAddObjectUserAction pins the request S/4 accepts
// (#154): a PUT on the task itself, the addobject user action, the E071 key
// in tm:request/tm:abap_object, and the vendor Accept type. The fake server
// answers 400 "Accept header missing" without Accept, as S/4 does after it
// has already added the entry, so dropping the header fails this test.
func TestAddToTransport_SendsAddObjectUserAction(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{addTask: addTaskXML}, http.StatusOK)

	if err := copiesClient(srv).AddToTransport(context.Background(), "r3tr", "prog", addObjName, addTask); err != nil {
		t.Fatalf("AddToTransport: %v", err)
	}
	got := puts()
	if len(got) != 1 {
		t.Fatalf("got %d PUTs, want 1", len(got))
	}
	p := got[0]
	if want := "/sap/bc/adt/cts/transportrequests/" + addTask; p.path != want {
		t.Errorf("path = %q, want %q", p.path, want)
	}
	if p.accept != copiesTMType || p.contentType != copiesTMType {
		t.Errorf("Accept = %q, Content-Type = %q, want both %q", p.accept, p.contentType, copiesTMType)
	}

	var body struct {
		UserAction string `xml:"useraction,attr"`
		Request    struct {
			Objects []struct {
				PgmID string `xml:"pgmid,attr"`
				Type  string `xml:"type,attr"`
				Name  string `xml:"name,attr"`
			} `xml:"abap_object"`
		} `xml:"request"`
	}
	if err := xml.Unmarshal([]byte(p.body), &body); err != nil {
		t.Fatalf("PUT body does not parse: %v\n%s", err, p.body)
	}
	if body.UserAction != "addobject" {
		t.Errorf("useraction = %q, want addobject", body.UserAction)
	}
	if len(body.Request.Objects) != 1 {
		t.Fatalf("got %d objects in body, want 1:\n%s", len(body.Request.Objects), p.body)
	}
	o := body.Request.Objects[0]
	if o.PgmID != addPgmID || o.Type != addObjType || o.Name != addObjName {
		t.Errorf("object = %s %s %s, want R3TR PROG ZTEST (pgmid and type upper-cased)", o.PgmID, o.Type, o.Name)
	}
}

func TestAddToTransport_SAPErrorSurfaces(t *testing.T) {
	srv, _ := copiesServer(t, map[string]string{addTask: addTaskXML}, http.StatusBadRequest)

	if err := copiesClient(srv).AddToTransport(context.Background(), addPgmID, addObjType, addObjName, addTask); err == nil {
		t.Fatal("expected the SAP rejection to surface as an error")
	}
}

// TestAddToTransport_ECCSendsNoPUT: SAP_BASIS 750 has no add action, and its
// transport organizer runs change owner for every PUT, so nothing may be sent.
func TestAddToTransport_ECCSendsNoPUT(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{addTask: eccWorklistXML}, http.StatusOK)

	err := copiesClient(srv).AddToTransport(context.Background(), addPgmID, addObjType, addObjName, addTask)
	assertAddObjectUnsupported(t, err)
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent to a system without addobject", n)
	}
}

// assertRequestReleased checks the refusal of a released (or release-in-
// progress) target: a request-specific error, not ErrorNotSupported, and
// without the SE09 hint that only applies to a system without the action.
func assertRequestReleased(t *testing.T, err error, number string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a released-request error, got nil")
	}
	var adtErr *adt.ADTError
	if !errors.As(err, &adtErr) {
		t.Fatalf("expected *adt.ADTError, got %T: %v", err, err)
	}
	if adtErr.Type != adt.ExceptionTypeRequestReleased {
		t.Errorf("Type = %q, want %q", adtErr.Type, adt.ExceptionTypeRequestReleased)
	}
	if kind := adt.ClassifyError(err); kind != adt.ErrorBadRequest {
		t.Errorf("ClassifyError = %v, want %v", kind, adt.ErrorBadRequest)
	}
	if strings.Contains(err.Error(), "SE09") {
		t.Errorf("message suggests SE09 for a released request: %v", err)
	}
	if !strings.Contains(err.Error(), number) {
		t.Errorf("message does not name the request %s: %v", number, err)
	}
}

func TestAddToTransport_ReleasedRequestSendsNoPUT(t *testing.T) {
	// N is released with import protection; O is a release in progress.
	for _, status := range []string{"R", "N", "O"} {
		t.Run("status_"+status, func(t *testing.T) {
			srv, puts := copiesServer(t, map[string]string{
				addTask: copiesRequestXML(addTask, status, "", "adturi"),
			}, http.StatusOK)

			err := copiesClient(srv).AddToTransport(context.Background(), addPgmID, addObjType, addObjName, addTask)
			assertRequestReleased(t, err, addTask)
			if n := len(puts()); n != 0 {
				t.Fatalf("%d PUTs sent without the addobject relation", n)
			}
		})
	}
}

// TestAddToTransport_ModifiableWithoutRelationIsNotSupported pins that a
// modifiable request lacking the relation keeps ErrorNotSupported.
func TestAddToTransport_ModifiableWithoutRelationIsNotSupported(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{
		addTask: copiesRequestXML(addTask, "D", "", "modify"),
	}, http.StatusOK)

	err := copiesClient(srv).AddToTransport(context.Background(), addPgmID, addObjType, addObjName, addTask)
	assertAddObjectUnsupported(t, err)
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent without the addobject relation", n)
	}
}

// TestAddToTransport_UnreadableRequestSendsNoPUT: the gate fails closed.
func TestAddToTransport_UnreadableRequestSendsNoPUT(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{}, http.StatusOK)

	if err := copiesClient(srv).AddToTransport(context.Background(), addPgmID, addObjType, addObjName, addTask); err == nil {
		t.Fatal("expected an error for a request that cannot be read")
	}
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent after a failed capability read", n)
	}
}

func TestAddToTransport_RejectsInvalidArguments(t *testing.T) {
	srv, puts := copiesServer(t, map[string]string{addTask: addTaskXML}, http.StatusOK)
	c := copiesClient(srv)
	for name, args := range map[string][4]string{
		"empty pgmid":   {"", addObjType, addObjName, addTask},
		"empty type":    {addPgmID, "", addObjName, addTask},
		"empty name":    {addPgmID, addObjType, " ", addTask},
		"empty request": {addPgmID, addObjType, addObjName, ""},
		"path in input": {addPgmID, addObjType, addObjName, addTask + "/x"},
	} {
		if err := c.AddToTransport(context.Background(), args[0], args[1], args[2], args[3]); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if n := len(puts()); n != 0 {
		t.Fatalf("%d PUTs sent for invalid arguments", n)
	}
}
