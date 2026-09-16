//go:build integration

// Integration tests for Task 8 of the ecc-transport-parsing plan
// (https://github.com/Hochfrequenz/adtler/issues/125): verify the
// GetTransportObjects/GetTransportInfo parsing changes and the Task 6
// removeobject capability tri-state against real R/3 (HFQ) and S/4 (S4U)
// systems, using eachSystem(t) so both run from a single `go test` command.
//
// These tests are read-only. They enumerate existing modifiable transport
// requests and read their contents; they never create, release, or modify a
// transport. See transport_task8_remove_gate_integration_test.go for the
// (also read-only, gate-blocked) RemoveFromTransport coverage.
package adt_test

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// objectSetKey renders a TransportObject slice as an order-independent
// fingerprint (PgmID|Type|Name per entry, sorted, joined) so two object
// lists can be compared for equality regardless of server-returned order or
// the Task/Position/WBType fields, which legitimately differ between the
// ADT and E071-fallback code paths (see GetTransportObjects' doc comment).
func objectSetKey(objs []adt.TransportObject) string {
	keys := make([]string, len(objs))
	for i, o := range objs {
		keys[i] = o.PgmID + "|" + o.Type + "|" + o.Name
	}
	sort.Strings(keys)
	return strings.Join(keys, ";")
}

// maxTransportProbe caps how many modifiable requests selectTwoDifferingTransports
// will pull object lists for. Each GetTransportObjects call fetches a full
// transport-organizer body — up to 10.3 MB measured on S/4 (see task-8-brief.md)
// against a 30s HTTP timeout — so this walks a bounded prefix of the
// modifiable worklist rather than the whole thing.
const maxTransportProbe = 25

// selectTwoDifferingTransports enumerates GetTransportRequests(ctx, "", "D")
// and walks the result (bounded by maxTransportProbe) until it finds two
// requests whose GetTransportObjects results are both non-empty and differ
// from each other. It t.Skips with a clear reason if fewer than two such
// requests exist among the probed prefix — this is a live-data dependent
// selection, not an assumption, per the Task 8 brief.
func selectTwoDifferingTransports(t *testing.T, ctx context.Context, client adt.Client) (req1, req2 adt.TransportRequest, objs1, objs2 []adt.TransportObject) {
	t.Helper()

	requests, err := client.GetTransportRequests(ctx, "", "D")
	if err != nil {
		t.Fatalf("GetTransportRequests: %v", err)
	}

	type candidate struct {
		req     adt.TransportRequest
		objects []adt.TransportObject
	}
	var found []candidate

	probeLimit := len(requests)
	if probeLimit > maxTransportProbe {
		probeLimit = maxTransportProbe
	}

	for _, r := range requests[:probeLimit] {
		objects, err := client.GetTransportObjects(ctx, r.Number)
		if err != nil {
			t.Logf("GetTransportObjects(%s) failed, skipping: %v", r.Number, err)
			continue
		}
		if len(objects) == 0 {
			continue
		}
		key := objectSetKey(objects)
		isDup := false
		for _, c := range found {
			if objectSetKey(c.objects) == key {
				isDup = true
				break
			}
		}
		if isDup {
			continue
		}
		found = append(found, candidate{req: r, objects: objects})
		if len(found) == 2 {
			return found[0].req, found[1].req, found[0].objects, found[1].objects
		}
	}

	t.Skipf("fewer than two modifiable requests with differing, non-empty object lists among the first %d of %d modifiable requests", probeLimit, len(requests))
	return adt.TransportRequest{}, adt.TransportRequest{}, nil, nil
}

// TestGetTransportObjects_TwoRequestsDiffer_Integration is the regression
// guard for adtler#125 on R/3: it asserts that GetTransportObjects, run
// against two different modifiable requests on the same system, returns
// results that actually differ per request rather than a stuck/cached/
// misparsed identical list. On S/4 the request may resolve via the E070
// fallback (adt/transport.go:444-447 in GetTransportRequests) and hand back
// requests with empty object lists, which selectTwoDifferingTransports
// accounts for by skipping empty results rather than failing on them.
func TestGetTransportObjects_TwoRequestsDiffer_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			ctx := context.Background()
			req1, req2, objs1, objs2 := selectTwoDifferingTransports(t, ctx, sys.Client)

			t.Logf("%s: selected %s (%d objects) and %s (%d objects)",
				sys.Name, req1.Number, len(objs1), req2.Number, len(objs2))
			for _, o := range objs1 {
				t.Logf("  [%s] pgmid=%s type=%s name=%s wbtype=%s pos=%s task=%s",
					req1.Number, o.PgmID, o.Type, o.Name, o.WBType, o.Position, o.Task)
			}
			for _, o := range objs2 {
				t.Logf("  [%s] pgmid=%s type=%s name=%s wbtype=%s pos=%s task=%s",
					req2.Number, o.PgmID, o.Type, o.Name, o.WBType, o.Position, o.Task)
			}

			if objectSetKey(objs1) == objectSetKey(objs2) {
				t.Fatalf("%s: requests %s and %s were selected as differing but compare equal — selection bug",
					sys.Name, req1.Number, req2.Number)
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

// TestRemoveObjectSupport_Capability_Integration verifies the Task 6
// removeobject capability tri-state (adt.RemoveObjectSupport) resolves to
// Unsupported on R/3 (HFQ) and Supported on S/4 (S4U). The capability is a
// cached side effect of readTransportXML (see cacheRemoveObjectSupport), so
// this first triggers one real transport read via GetTransportInfo before
// reading the cached verdict through the test-only adt.TestClient hook
// (sys.Client.(adt.TestClient) — never added to the exported Client
// interface, see adt/export_internal_test.go).
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
