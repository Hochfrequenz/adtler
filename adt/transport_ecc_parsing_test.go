package adt_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"
)

// Object names shared by eccWorklistXML's two requests, referenced repeatedly
// across the tests below.
const (
	eccOrderRequestObjName = "/HFQ/ORDER_REQUEST"
	eccLockReproObjName    = "ZCL_LOCKREPRO_2"
)

// newFixtureClient returns a client whose transportrequests/<transport>
// endpoint always answers with body, regardless of which transport number is
// requested — mirroring the ECC worklist bug where a GET for one transport
// number returns the whole worklist body (see eccWorklistXML's doc comment).
func newFixtureClient(t *testing.T, body string) adt.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/sap/bc/adt/cts/transportrequests/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	cfg := sapmcpconfig.SAPSystem{Host: srv.URL, User: "U", Password: "P", Client: "100"}
	return adt.NewClient(cfg)
}

// TestGetTransportObjects_ECCWorklist_FiltersByNumber pins the filter rule:
// asking for each of the two request numbers in eccWorklistXML returns only
// that request's objects, and the two results differ.
func TestGetTransportObjects_ECCWorklist_FiltersByNumber(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	objs178, err := client.GetTransportObjects(context.Background(), "HFQK900178")
	if err != nil {
		t.Fatalf("HFQK900178: unexpected error: %v", err)
	}
	if len(objs178) != 1 || objs178[0].Name != eccOrderRequestObjName {
		t.Fatalf("HFQK900178: got %+v, want single /HFQ/ORDER_REQUEST object", objs178)
	}

	objs952, err := client.GetTransportObjects(context.Background(), "HFQK902952")
	if err != nil {
		t.Fatalf("HFQK902952: unexpected error: %v", err)
	}
	if len(objs952) != 1 || objs952[0].Name != eccLockReproObjName {
		t.Fatalf("HFQK902952: got %+v, want single ZCL_LOCKREPRO_2 object", objs952)
	}
}

// TestGetTransportObjects_ECCWorklist_LowercaseNumberMatches verifies the
// comparison is case-insensitive.
func TestGetTransportObjects_ECCWorklist_LowercaseNumberMatches(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	objs, err := client.GetTransportObjects(context.Background(), "hfqk900178")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 || objs[0].Name != eccOrderRequestObjName {
		t.Fatalf("got %+v, want single /HFQ/ORDER_REQUEST object", objs)
	}
}

// TestGetTransportObjects_ECCWorklist_AbsentNumberErrors verifies a number
// absent from the worklist body returns the documented error, not an empty
// slice.
func TestGetTransportObjects_ECCWorklist_AbsentNumberErrors(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	_, err := client.GetTransportObjects(context.Background(), "HFQK999999")
	if err == nil {
		t.Fatal("expected error for a transport absent from the worklist body")
	}
	if !strings.Contains(err.Error(), "HFQK999999") ||
		!strings.Contains(err.Error(), "transport-organizer worklist") ||
		!strings.Contains(err.Error(), "released requests cannot be read this way") {
		t.Errorf("error text missing expected content: %v", err)
	}
}

// TestGetTransportObjects_ECCWorklistEmptyNumber_UnnumberedRequestNeverMatches
// verifies the blanked-number request in eccWorklistEmptyNumberXML contributes
// no objects to any result and does not make the "present" check succeed for
// itself.
func TestGetTransportObjects_ECCWorklistEmptyNumber_UnnumberedRequestNeverMatches(t *testing.T) {
	client := newFixtureClient(t, eccWorklistEmptyNumberXML)

	// The other, still-numbered request must be unaffected and must not pick
	// up the blanked request's object.
	objs178, err := client.GetTransportObjects(context.Background(), "HFQK900178")
	if err != nil {
		t.Fatalf("HFQK900178: unexpected error: %v", err)
	}
	if len(objs178) != 1 || objs178[0].Name != eccOrderRequestObjName {
		t.Fatalf("HFQK900178: got %+v, want only its own object", objs178)
	}
	for _, o := range objs178 {
		if o.Name == eccLockReproObjName {
			t.Error("HFQK900178: must not contain the blanked request's object")
		}
	}

	// Asking for the now-blanked number itself must be treated as absent, not
	// present-with-objects: the empty tm:number attribute must never satisfy
	// a lookup, including a lookup for "" itself.
	_, err = client.GetTransportObjects(context.Background(), "HFQK902952")
	if err == nil {
		t.Fatal("expected error: HFQK902952's number was blanked in this fixture")
	}
}

// TestGetTransportObjects_ECCCustomizing_ReturnsCustomizingGroupObjects
// verifies a request under <tm:customizing> is not dropped the way it would
// be if only <tm:workbench> were bound.
func TestGetTransportObjects_ECCCustomizing_ReturnsCustomizingGroupObjects(t *testing.T) {
	client := newFixtureClient(t, eccCustomizingXML)

	objs, err := client.GetTransportObjects(context.Background(), "HFQK902952")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 || objs[0].Name != eccLockReproObjName {
		t.Fatalf("got %+v, want single ZCL_LOCKREPRO_2 object from the customizing group", objs)
	}
}

// TestGetTransportObjects_S4SingleRequest_BindsAllObjectsWrapper verifies the
// a0 fix: request-level objects wrapped in <tm:all_objects> are no longer
// dropped, and dedup still collapses the identical object also present under
// <tm:task> into a single entry.
func TestGetTransportObjects_S4SingleRequest_BindsAllObjectsWrapper(t *testing.T) {
	client := newFixtureClient(t, s4SingleRequestXML)

	objs, err := client.GetTransportObjects(context.Background(), "S4UK904438")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want 1 (deduped): %+v", len(objs), objs)
	}
	if objs[0].Name != "Z_WINBACK" || objs[0].Type != "DEVC" || objs[0].PgmID != "R3TR" {
		t.Errorf("got %+v, want R3TR/DEVC/Z_WINBACK", objs[0])
	}
}

// TestGetTransportObjects_S4SingleRequest_WrongNumberIsAbsent verifies a
// Format 1 body whose request number differs from the requested transport is
// treated as absent, identically to the worklist branch.
func TestGetTransportObjects_S4SingleRequest_WrongNumberIsAbsent(t *testing.T) {
	client := newFixtureClient(t, s4SingleRequestXML)

	_, err := client.GetTransportObjects(context.Background(), "S4UK000000")
	if err == nil {
		t.Fatal("expected error: fixture body is for S4UK904438, not S4UK000000")
	}
}

// TestGetTransportTasks_ECCWorklist_FiltersByNumber aligns
// parseTransportTaskNumbers onto the same filter rule as
// parseTransportObjectsXML.
func TestGetTransportTasks_ECCWorklist_FiltersByNumber(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	tasks178, err := client.GetTransportTasks(context.Background(), "HFQK900178")
	if err != nil {
		t.Fatalf("HFQK900178: unexpected error: %v", err)
	}
	if len(tasks178) != 1 || tasks178[0] != "HFQK900635" {
		t.Fatalf("HFQK900178: got %v, want [HFQK900635]", tasks178)
	}

	tasks952, err := client.GetTransportTasks(context.Background(), "HFQK902952")
	if err != nil {
		t.Fatalf("HFQK902952: unexpected error: %v", err)
	}
	if len(tasks952) != 1 || tasks952[0] != "HFQK902953" {
		t.Fatalf("HFQK902952: got %v, want [HFQK902953]", tasks952)
	}
}

// TestGetTransportTasks_ECCWorklist_AbsentNumberErrors mirrors the objects
// test: the three parsers on this body must agree on the absent/empty
// distinction.
func TestGetTransportTasks_ECCWorklist_AbsentNumberErrors(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	_, err := client.GetTransportTasks(context.Background(), "HFQK999999")
	if err == nil {
		t.Fatal("expected error for a transport absent from the worklist body")
	}
}

// TestGetTransportObjects_ECCWorklist_AttributesTaskNumber verifies Task 3's
// acceptance criterion for eccWorklistXML: each object's Task carries the
// number of the task that recorded it, surviving Task 2's per-request
// filtering.
func TestGetTransportObjects_ECCWorklist_AttributesTaskNumber(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	objs178, err := client.GetTransportObjects(context.Background(), "HFQK900178")
	if err != nil {
		t.Fatalf("HFQK900178: unexpected error: %v", err)
	}
	if len(objs178) != 1 || objs178[0].Task != "HFQK900635" {
		t.Fatalf("HFQK900178: got %+v, want single object with Task HFQK900635", objs178)
	}

	objs952, err := client.GetTransportObjects(context.Background(), "HFQK902952")
	if err != nil {
		t.Fatalf("HFQK902952: unexpected error: %v", err)
	}
	if len(objs952) != 1 || objs952[0].Task != "HFQK902953" {
		t.Fatalf("HFQK902952: got %+v, want single object with Task HFQK902953", objs952)
	}
}

// TestGetTransportObjects_S4SingleRequest_DedupUpgradesTask verifies that the
// object recorded both under <tm:all_objects> at request level (no task) and
// again under <tm:task> (same object) is deduped into a single entry that
// carries the task's number — not left with an empty Task, which is what a
// first-wins-only implementation of the dedup map would produce.
func TestGetTransportObjects_S4SingleRequest_DedupUpgradesTask(t *testing.T) {
	client := newFixtureClient(t, s4SingleRequestXML)

	objs, err := client.GetTransportObjects(context.Background(), "S4UK904438")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want 1 (deduped): %+v", len(objs), objs)
	}
	if objs[0].Task != "S4UK904439" {
		t.Errorf("got Task %q, want S4UK904439 (upgraded from the task-level duplicate)", objs[0].Task)
	}
}

// TestGetTransportObjects_S4ObjectAtBothLevels_DedupesToOneWithTaskAndFirstPosition
// verifies the acceptance criterion for s4ObjectAtBothLevelsXML (which records
// the same object three times: bare under <tm:request>, wrapped in
// <tm:all_objects>, and under <tm:task>): the result holds exactly one entry,
// it carries the task number, and it keeps the position of the first-seen
// (bare, request-level) occurrence rather than any later one.
func TestGetTransportObjects_S4ObjectAtBothLevels_DedupesToOneWithTaskAndFirstPosition(t *testing.T) {
	client := newFixtureClient(t, s4ObjectAtBothLevelsXML)

	objs, err := client.GetTransportObjects(context.Background(), "S4UK904438")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want exactly 1 (deduped across all three occurrences): %+v", len(objs), objs)
	}
	got := objs[0]
	if got.Name != "Z_WINBACK" || got.Type != "DEVC" || got.PgmID != "R3TR" {
		t.Errorf("got %+v, want R3TR/DEVC/Z_WINBACK", got)
	}
	if got.Task != "S4UK904439" {
		t.Errorf("got Task %q, want S4UK904439", got.Task)
	}
	if got.Position != "000001" {
		t.Errorf("got Position %q, want the first-seen position 000001", got.Position)
	}
}
