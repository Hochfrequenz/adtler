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
// transport-organizer body — up to 10.3 MB measured on S/4 (see
// task-1-brief.md) against a 30s HTTP timeout — so this walks a bounded
// prefix of the modifiable worklist rather than the whole thing.
const maxTransportProbe = 25

// selectTwoDifferingTransports enumerates GetTransportRequests(ctx, "", "D")
// and walks the result (bounded by maxTransportProbe), grouping every
// non-empty GetTransportObjects result by its objectSetKey fingerprint.
//
// Two outcomes are NOT the same thing, and this function tells them apart
// rather than collapsing both into a skip:
//
//   - Sparse data: fewer than two distinct non-empty fingerprints turn up
//     among the probed prefix (e.g. only one modifiable request has any
//     objects at all, or the rest happened to be empty). This is a property
//     of the target system's current data, not a bug — t.Skip with a clear
//     reason.
//   - A broken filter: two or more DIFFERENT request numbers return the
//     identical non-empty fingerprint. This is the exact shape of the
//     adtler#125 regression — GetTransportObjects ignoring which transport
//     number it was asked for and returning the same body regardless — and
//     must never be reported as "ok" via a skip. t.Fatal, naming the
//     offending request numbers and the shared fingerprint.
//
// On success it returns two requests whose object sets are guaranteed to
// differ by construction (they are the first two distinct fingerprints
// found), so callers do not need to re-verify that themselves.
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
	// byKey groups every non-empty candidate probed so far by its
	// object-set fingerprint. A key claimed by two or more candidates is
	// the failure signature described above, not a "duplicate to skip".
	byKey := make(map[string][]candidate)
	var order []string // fingerprints in first-seen order

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
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}
		byKey[key] = append(byKey[key], candidate{req: r, objects: objects})

		// Stop once two DISTINCT fingerprints exist — that's all this test
		// needs. A duplicate landing on an already-seen key does not count
		// toward this and keeps the loop going (see the fatal check below,
		// which needs to see every duplicate, not just the first).
		if len(order) >= 2 {
			break
		}
	}

	for _, key := range order {
		group := byKey[key]
		if len(group) < 2 {
			continue
		}
		numbers := make([]string, len(group))
		for i, c := range group {
			numbers[i] = c.req.Number
		}
		t.Fatalf("GetTransportObjects returned the identical non-empty object set (fingerprint %q) for %d different requests (%s) — "+
			"this is the adtler#125 regression shape (the object list does not depend on which transport number was requested), not sparse test data",
			key, len(group), strings.Join(numbers, ", "))
	}

	if len(order) < 2 {
		t.Skipf("fewer than two modifiable requests with differing, non-empty object lists among the first %d of %d modifiable requests", probeLimit, len(requests))
	}

	c1, c2 := byKey[order[0]][0], byKey[order[1]][0]
	return c1.req, c2.req, c1.objects, c2.objects
}

// TestGetTransportObjects_TwoRequestsDiffer_Integration is the regression
// guard for adtler#125 on R/3: GetTransportObjects, run against two
// different modifiable requests on the same system, must return results
// that actually depend on the request number rather than a
// stuck/cached/misparsed identical list.
//
// What this test enforces, precisely: selectTwoDifferingTransports either
// (a) returns two requests it has already confirmed have distinct,
// non-empty object sets — in which case there is nothing left to
// re-verify here, so this test body does not re-check that equality, or
// (b) fails the test itself (t.Fatal) if it instead finds several
// different request numbers collapsing onto the same non-empty
// fingerprint — the regression's actual shape — or (c) skips if the
// system's current data is simply too sparse to tell (fewer than two
// non-empty results at all). See selectTwoDifferingTransports' doc comment
// for why those three cases are kept distinct rather than folded into one
// skip. On S/4 a request may resolve via the E070 fallback
// (adt/transport.go:444-447 in GetTransportRequests) and hand back an
// empty object list, which selectTwoDifferingTransports treats as sparse
// data (case c), not as a fingerprint collision.
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
