//go:build integration

// Integration tests for adtler#125
// (https://github.com/Hochfrequenz/adtler/issues/125): verify the
// GetTransportObjects/GetTransportInfo ECC-worklist-vs-S/4-single-request
// parsing changes and the removeobject capability tri-state
// (adt.RemoveObjectSupport) against the real ECC (SAP_BASIS 750) and S/4
// (SAP_BASIS 816) systems configured locally, using eachSystem(t) so both
// run from a single `go test` command. Which configured system name is which
// release is resolved at runtime from the local SAP config, not hardcoded
// here beyond the two `case` labels below that key the expected verdict.
//
// These tests are read-only. They enumerate existing modifiable transport
// requests and read their contents; they never create, release, or modify a
// transport. See transport_removeobject_gate_integration_test.go for the
// (also read-only, gate-blocked) RemoveFromTransport coverage.
package adt_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// maxTransportProbe caps how many modifiable requests selectTwoTaskOwnedTransports
// will pull object lists for. Each GetTransportObjects call fetches a full
// transport-organizer body — up to 10.3 MB measured on S/4 — against a 30s
// HTTP timeout, so this walks a bounded prefix of the modifiable worklist
// rather than the whole thing.
const maxTransportProbe = 25

type transportObjectsProbe struct {
	req     adt.TransportRequest
	objects []adt.TransportObject
}

func taskSet(tasks []string) map[string]bool {
	set := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		set[strings.ToUpper(task)] = true
	}
	return set
}

// selectTwoTaskOwnedTransports finds two modifiable requests whose object lists
// are non-empty and whose task-attributed objects all point back to that
// request's own tasks. That ownership check is the live oracle: if
// GetTransportObjects regresses to "return some other request's objects", at
// least one returned Task stops belonging to the request being probed.
func selectTwoTaskOwnedTransports(t *testing.T, ctx context.Context, client adt.Client) (probe1, probe2 transportObjectsProbe) {
	t.Helper()

	requests, err := client.GetTransportRequests(ctx, "", "D")
	if err != nil {
		t.Fatalf("GetTransportRequests: %v", err)
	}
	var probes []transportObjectsProbe

	probeLimit := len(requests)
	if probeLimit > maxTransportProbe {
		probeLimit = maxTransportProbe
	}

	for _, r := range requests[:probeLimit] {
		tasks, err := client.GetTransportTasks(ctx, r.Number)
		if err != nil {
			t.Logf("GetTransportTasks(%s) failed, skipping: %v", r.Number, err)
			continue
		}
		if len(tasks) == 0 {
			continue
		}
		taskNums := taskSet(tasks)

		objects, err := client.GetTransportObjects(ctx, r.Number)
		if err != nil {
			t.Logf("GetTransportObjects(%s) failed, skipping: %v", r.Number, err)
			continue
		}
		if len(objects) == 0 {
			continue
		}
		hasTaskObject := false
		for _, o := range objects {
			if o.Task == "" {
				continue
			}
			hasTaskObject = true
			if !taskNums[strings.ToUpper(o.Task)] {
				sortedTasks := append([]string(nil), tasks...)
				sort.Strings(sortedTasks)
				t.Fatalf("GetTransportObjects(%s) returned task %q on object %s/%s/%s, but GetTransportTasks only reports %s",
					r.Number, o.Task, o.PgmID, o.Type, o.Name, strings.Join(sortedTasks, ", "))
			}
		}
		if !hasTaskObject {
			continue
		}
		probes = append(probes, transportObjectsProbe{req: r, objects: objects})
		if len(probes) >= 2 {
			return probes[0], probes[1]
		}
	}

	if len(probes) < 2 {
		t.Skipf("fewer than two modifiable requests with non-empty object lists and task-attributed entries among the first %d of %d modifiable requests", probeLimit, len(requests))
	}
	return probes[0], probes[1]
}

// TestGetTransportObjects_TaskAttributionMatchesRequestTasks_Integration is the
// ECC regression guard for adtler#125 on live data: for two different
// modifiable requests, every task-attributed object GetTransportObjects returns
// must belong to one of that request's own tasks.
func TestGetTransportObjects_TaskAttributionMatchesRequestTasks_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			probe1, probe2 := selectTwoTaskOwnedTransports(t, ctx, sys.Client)

			t.Logf("%s: selected %s (%d objects) and %s (%d objects)",
				sys.Name, probe1.req.Number, len(probe1.objects), probe2.req.Number, len(probe2.objects))
			for _, o := range probe1.objects {
				t.Logf("  [%s] pgmid=%s type=%s name=%s wbtype=%s pos=%s task=%s",
					probe1.req.Number, o.PgmID, o.Type, o.Name, o.WBType, o.Position, o.Task)
			}
			for _, o := range probe2.objects {
				t.Logf("  [%s] pgmid=%s type=%s name=%s wbtype=%s pos=%s task=%s",
					probe2.req.Number, o.PgmID, o.Type, o.Name, o.WBType, o.Position, o.Task)
			}
		})
	}
}

// TestGetTransportInfo_ReturnsRequestedNumber_Integration is the regression
// guard for https://github.com/Hochfrequenz/aibap.mcp/issues/496:
// GetTransportInfo must succeed and echo back the number it was asked for
// on both the older-systems worklist shape (R/3) and the modern one (S/4).
func TestGetTransportInfo_ReturnsRequestedNumber_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()

			requests, err := sys.Client.GetTransportRequests(ctx, "", "D")
			if err != nil {
				t.Fatalf("GetTransportRequests: %v", err)
			}
			if len(requests) == 0 {
				t.Skipf("%s: no modifiable transport requests available to probe GetTransportInfo with", sys.Name)
			}
			number := requests[0].Number

			info, err := sys.Client.GetTransportInfo(ctx, number)
			if err != nil {
				t.Fatalf("GetTransportInfo(%s): %v", number, err)
			}
			t.Logf("%s: GetTransportInfo(%s) -> Number=%s Owner=%s Status=%s Description=%q",
				sys.Name, number, info.Number, info.Owner, info.Status, info.Description)

			if info.Number != number {
				t.Errorf("%s: GetTransportInfo(%s).Number = %q, want %q", sys.Name, number, info.Number, number)
			}
		})
	}
}

// TestRemoveObjectSupport_Capability_Integration verifies the removeobject
// capability tri-state (adt.RemoveObjectSupport) resolves to
// Unsupported on the ECC system and Supported on the S/4 system. The
// capability is a cached side effect of readTransportXML (see
// cacheRemoveObjectSupport), so this first triggers one real transport read
// via GetTransportInfo before reading the cached verdict through the
// test-only adt.TestClient hook (sys.Client.(adt.TestClient) — never added
// to the exported Client interface, see adt/export_internal_test.go). The
// `case` labels below select on the system name configured locally (the
// only way this test can tell which real system it is talking to); nothing
// else in this function should be read as naming a specific environment.
func TestRemoveObjectSupport_Capability_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()

			testClient, ok := sys.Client.(adt.TestClient)
			if !ok {
				t.Fatalf("%s: client %T does not implement adt.TestClient", sys.Name, sys.Client)
			}

			requests, err := sys.Client.GetTransportRequests(ctx, "", "D")
			if err != nil {
				t.Fatalf("GetTransportRequests: %v", err)
			}
			if len(requests) == 0 {
				t.Skipf("%s: no modifiable transport requests available to trigger a transport read", sys.Name)
			}
			if _, err := sys.Client.GetTransportInfo(ctx, requests[0].Number); err != nil {
				t.Fatalf("GetTransportInfo(%s): %v", requests[0].Number, err)
			}

			got := testClient.RemoveObjectSupportForTest()
			t.Logf("%s: RemoveObjectSupport = %v", sys.Name, got)

			var want adt.RemoveObjectSupport
			switch sys.Name {
			case "HFQ":
				want = adt.RemoveObjectSupportUnsupported
			case "S4U":
				want = adt.RemoveObjectSupportSupported
			default:
				t.Skipf("%s: no expected capability verdict recorded for this system name", sys.Name)
			}
			if got != want {
				t.Errorf("%s: RemoveObjectSupport = %v, want %v", sys.Name, got, want)
			}
		})
	}
}
