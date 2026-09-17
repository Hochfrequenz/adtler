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

// s4WinbackFirstTaskNumber is the first task number recording
// R3TR/DEVC/Z_WINBACK in s4SingleRequestXML, s4ObjectAtBothLevelsXML, and
// s4ObjectDivergentPositionAndTaskXML — referenced repeatedly across the
// dedup/attribution tests below.
const s4WinbackFirstTaskNumber = "S4UK904439"

// newFixtureClient returns a TestClient whose transportrequests/<transport>
// endpoint always answers with body, regardless of which transport number is
// requested — mirroring the ECC worklist bug where a GET for one transport
// number returns the whole worklist body (see eccWorklistXML's doc comment).
// It returns adt.TestClient (rather than plain adt.Client) so callers that
// also need to read back the cached RemoveObjectSupport state (see
// transport_removeobject_support_test.go) can use the same helper instead of
// a near-duplicate one; TestClient embeds Client, so this serves every
// caller that only needs the public surface too.
func newFixtureClient(t *testing.T, body string) adt.TestClient {
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
	return adt.NewClientForTest(cfg)
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

// TestGetTransportObjects_S4SingleRequest_BindsAllObjectsWrapper verifies
// that request-level objects wrapped in <tm:all_objects> are no longer
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

// TestGetTransportObjects_S4RequestPresentButEmpty_ReturnsEmptySliceNilError
// pins the present-but-empty case directly: s4RequestNoObjectsXML's request
// (S4UK904476) IS the addressed request — it is present, per
// absentTransportError's contract — it simply holds no abap_object anywhere.
// That must come back as an empty slice and a nil error, never as
// absentTransportError; absent and empty are deliberately different outcomes
// (see absentTransportError's doc comment), and until this test existed the
// distinction was only ever exercised as a side effect of a capability test
// (TestRemoveObjectSupport_S4RequestNoObjects_Supported), not asserted on
// directly here.
func TestGetTransportObjects_S4RequestPresentButEmpty_ReturnsEmptySliceNilError(t *testing.T) {
	client := newFixtureClient(t, s4RequestNoObjectsXML)

	objs, err := client.GetTransportObjects(context.Background(), "S4UK904476")
	if err != nil {
		t.Fatalf("unexpected error for a present-but-empty request: %v", err)
	}
	if len(objs) != 0 {
		t.Errorf("got %+v, want an empty slice", objs)
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
	if objs[0].Task != s4WinbackFirstTaskNumber {
		t.Errorf("got Task %q, want %s (upgraded from the task-level duplicate)", objs[0].Task, s4WinbackFirstTaskNumber)
	}
}

// TestGetTransportObjects_S4ObjectAtBothLevels_DedupesToOneWithTaskAndFirstPosition
// verifies the acceptance criterion for s4ObjectAtBothLevelsXML (which
// records the same object three times: bare under <tm:request>, wrapped in
// <tm:all_objects>, and under <tm:task>): the result holds exactly one entry
// and carries the task number. It also asserts Position == "000001", but
// every occurrence in this fixture shares that same position, so that
// assertion cannot by itself distinguish "kept the first-seen position" from
// "the last occurrence happened to carry the same value" —
// TestGetTransportObjects_DivergentPositionAndTask_KeepsFirstSeenPosition
// below is the test that actually discriminates between those two.
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
	if got.Task != s4WinbackFirstTaskNumber {
		t.Errorf("got Task %q, want %s", got.Task, s4WinbackFirstTaskNumber)
	}
	if got.Position != "000001" {
		t.Errorf("got Position %q, want the first-seen position 000001", got.Position)
	}
}

// TestGetTransportObjects_DivergentPositionAndTask_KeepsFirstSeenPosition uses
// s4ObjectDivergentPositionAndTaskXML, where the duplicated object carries a
// different tm:position at every occurrence (000001 bare-request, 000002
// under the first task, 000003 under the second task). Unlike
// s4ObjectAtBothLevelsXML (every occurrence there shares the same position,
// so it cannot tell "kept first-seen" apart from "last occurrence overwrote
// the whole entry"), this fixture actually discriminates: only keeping the
// first-seen Position produces 000001 here.
func TestGetTransportObjects_DivergentPositionAndTask_KeepsFirstSeenPosition(t *testing.T) {
	client := newFixtureClient(t, s4ObjectDivergentPositionAndTaskXML)

	objs, err := client.GetTransportObjects(context.Background(), "S4UK904438")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want exactly 1 (deduped across all three occurrences): %+v", len(objs), objs)
	}
	if got := objs[0].Position; got != "000001" {
		t.Errorf("got Position %q, want the first-seen (bare, request-level) position 000001, not a later occurrence's 000002/000003", got)
	}
}

// TestGetTransportInfo_ECCWorklist_SelectsMatchingRequest verifies
// parseTransportInfo's Format 2 (worklist) branch: over eccWorklistXML, the
// requested request's Number/Owner/Description/Status come back — not the
// worklist's first entry regardless of which number was asked for.
func TestGetTransportInfo_ECCWorklist_SelectsMatchingRequest(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	info, err := client.GetTransportInfo(context.Background(), "HFQK902952")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := &adt.TransportRequest{
		Number:      "HFQK902952",
		Owner:       "MEISKEJ",
		Description: "Lock reproducer probe (throwaway, delete after)",
		Status:      adt.TransportStatusModifiable,
	}
	if *info != *want {
		t.Errorf("got %+v, want %+v", *info, *want)
	}

	// The other request in the same worklist body must resolve to its own
	// data, not HFQK902952's.
	info178, err := client.GetTransportInfo(context.Background(), "HFQK900178")
	if err != nil {
		t.Fatalf("HFQK900178: unexpected error: %v", err)
	}
	if info178.Number != "HFQK900178" || info178.Owner != "KLEINK" {
		t.Errorf("HFQK900178: got %+v, want Number=HFQK900178 Owner=KLEINK", info178)
	}
}

// TestGetTransportInfo_ECCWorklist_AbsentNumberErrors mirrors the
// objects/tasks absent-number tests: a number absent from the worklist body
// must error with Task 2's absentTransportError, not return a zero-value
// TransportRequest.
func TestGetTransportInfo_ECCWorklist_AbsentNumberErrors(t *testing.T) {
	client := newFixtureClient(t, eccWorklistXML)

	_, err := client.GetTransportInfo(context.Background(), "HFQK999999")
	if err == nil {
		t.Fatal("expected error for a transport absent from the worklist body")
	}
	if !strings.Contains(err.Error(), "HFQK999999") ||
		!strings.Contains(err.Error(), "transport-organizer worklist") ||
		!strings.Contains(err.Error(), "released requests cannot be read this way") {
		t.Errorf("error text missing expected content: %v", err)
	}
}

// TestGetTransportInfo_S4SingleRequest_StillPasses verifies the Format 1
// (single-request) branch is unaffected by the switch to xmlTransportDoc.
func TestGetTransportInfo_S4SingleRequest_StillPasses(t *testing.T) {
	client := newFixtureClient(t, s4SingleRequestXML)

	info, err := client.GetTransportInfo(context.Background(), "S4UK904438")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Number != "S4UK904438" || info.Owner != "MANNN" || info.Status != adt.TransportStatusModifiable {
		t.Errorf("got %+v, want Number=S4UK904438 Owner=MANNN Status=D", info)
	}
}

// TestGetTransportInfo_S4SingleRequest_WrongNumberIsAbsent verifies a Format
// 1 body whose request number differs from the requested transport is
// treated as absent, identically to GetTransportObjects/GetTransportTasks.
func TestGetTransportInfo_S4SingleRequest_WrongNumberIsAbsent(t *testing.T) {
	client := newFixtureClient(t, s4SingleRequestXML)

	_, err := client.GetTransportInfo(context.Background(), "S4UK000000")
	if err == nil {
		t.Fatal("expected error: fixture body is for S4UK904438, not S4UK000000")
	}
}

// TestGetTransportObjects_DivergentPositionAndTask_KeepsFirstAttributedTask
// uses the same fixture to pin the other half of the upgrade rule: once an
// entry has been attributed to a task (S4UK904439, the first task to record
// the object), a second, different task recording the same object
// (S4UK904440) must not overwrite that attribution. No other fixture in this
// package attributes one object to two different tasks, so nothing else
// catches an unconditional "last task wins" simplification of the
// Task=="" upgrade guard.
func TestGetTransportObjects_DivergentPositionAndTask_KeepsFirstAttributedTask(t *testing.T) {
	client := newFixtureClient(t, s4ObjectDivergentPositionAndTaskXML)

	objs, err := client.GetTransportObjects(context.Background(), "S4UK904438")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d objects, want exactly 1 (deduped across all three occurrences): %+v", len(objs), objs)
	}
	if got := objs[0].Task; got != s4WinbackFirstTaskNumber {
		t.Errorf("got Task %q, want the first task to record the object (%s), not the second (S4UK904440)", got, s4WinbackFirstTaskNumber)
	}
}
