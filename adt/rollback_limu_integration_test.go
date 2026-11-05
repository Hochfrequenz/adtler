//go:build integration && transport

package adt_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// Persistent fixtures of TestRollbackTransport_SubObjects_Integration. Each is
// created once in a request that is then released, so that it has a version
// outside the request every run changes it in. They are left in place.
const (
	limuFixtureClass    = "ZCL_ADT_MCP_RB_LIMU"
	limuFixtureClassURI = "/sap/bc/adt/oo/classes/zcl_adt_mcp_rb_limu"
	limuFixtureProg     = "Z_ADT_MCP_RB_LIMU"
	limuFixtureProgURI  = "/sap/bc/adt/programs/programs/z_adt_mcp_rb_limu"
)

func limuClassSource(marker string) string {
	return "CLASS zcl_adt_mcp_rb_limu DEFINITION PUBLIC FINAL CREATE PUBLIC.\n" +
		"  PUBLIC SECTION.\n" +
		"    METHODS marker RETURNING VALUE(r) TYPE string.\n" +
		"ENDCLASS.\n" +
		"\n" +
		"CLASS zcl_adt_mcp_rb_limu IMPLEMENTATION.\n" +
		"  METHOD marker.\n" +
		"    r = '" + marker + "'.\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
}

func limuProgSource(marker string) string {
	return "REPORT z_adt_mcp_rb_limu.\n" +
		"WRITE '" + marker + "'.\n"
}

// TestRollbackTransport_SubObjects_Integration changes one method of an
// existing class, and the source of an existing program, in a new request
// and rolls the request back (#134). SAP records such a change as sub-object
// entries (LIMU METH, LIMU REPS), not as the class or program, and the
// rollback used to skip every one of them and restore nothing.
//
// The class part also guards the class restore itself: it reads the history
// of the class's main include and writes it back to the main source. Before
// #134 the client read only the local includes, so a method change was not
// found in the history at all.
//
// The fixtures need a released request once. Where release over ADT does not
// work (ECC), the test skips until the fixture request is released by hand.
func TestRollbackTransport_SubObjects_Integration(t *testing.T) {
	ctx := context.Background()
	kinds := []struct {
		name, objType, objName, uri string
		source                      func(string) string
		// needLIMU: SAP records a method change as LIMU METH on both
		// releases. A program change is LIMU REPS on ECC but R3TR PROG on
		// S/4 (SAP_BASIS 816), where the program part then only confirms
		// that the whole-object path still works.
		needLIMU bool
	}{
		{"class", "CLAS", limuFixtureClass, limuFixtureClassURI, limuClassSource, true},
		{"program", "PROG", limuFixtureProg, limuFixtureProgURI, limuProgSource, false},
	}
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			for _, k := range kinds {
				k := k
				t.Run(k.name, func(t *testing.T) {
					c := sys.Client
					_ = c.Logout(ctx)
					ensureReleasedFixture(t, c, k.objType, k.objName, k.uri, k.source("v1"))

					request := createDisposableTransport(t, c, "adtler #134 rollback of sub-object entries")
					t.Cleanup(func() { cleanupTransportEntries(t, c, request) })
					t.Cleanup(func() { resetFixtureSource(t, c, k.uri, k.source("v1"), request) })

					// 1. Change the source in the request.
					writeAndActivate(t, c, k.uri, k.source("v2"), request)

					// 2. The request records the change as sub-object entries.
					objs, err := c.GetTransportObjects(ctx, request)
					if err != nil {
						t.Fatalf("[2] GetTransportObjects: %s", redact(err.Error(), "", request))
					}
					byKind := map[string]int{}
					for _, o := range objs {
						byKind[o.PgmID+" "+o.Type]++
					}
					t.Logf("[2] request entries by kind: %v", sortedCounts(byKind))
					limu := 0
					for _, o := range objs {
						if o.PgmID == "LIMU" {
							limu++
						}
					}
					switch {
					case limu == 0 && k.needLIMU:
						t.Fatalf("[2] SAP recorded no LIMU entry for the change, so this run does not reach the #134 path")
					case limu == 0:
						t.Logf("[2] no LIMU entry on this release; checking the whole-object restore only")
					}

					// 3. Roll back: the object is restored, once, under its own type.
					result, err := c.RollbackTransport(ctx, request)
					if err != nil {
						t.Fatalf("[3] RollbackTransport: %s", redact(err.Error(), "", request))
					}
					logRollback(t, "[3]", request, result)
					restored := 0
					for _, e := range result.Restored {
						if e.Type == k.objType && e.Name == k.objName {
							restored++
						}
					}
					if restored != 1 {
						t.Errorf("[3] fixture restored %d times, want 1 (restored=%d skipped=%d failed=%d)",
							restored, len(result.Restored), len(result.Skipped), len(result.Failed))
					}
					src, err := c.GetSource(ctx, k.uri)
					if err != nil {
						t.Fatalf("[3] GetSource: %v", err)
					}
					if !strings.Contains(src.Source, "'v1'") || strings.Contains(src.Source, "'v2'") {
						t.Errorf("[3] source after rollback is not the v1 version:\n%s", src.Source)
					}
				})
			}
		})
	}
}

// ensureReleasedFixture creates the fixture objType/name with source in a
// request of its own and releases that request, unless the fixture exists.
// Without a release the fixture has no version outside the requests the test
// changes it in, so the test skips when SAP does not release the request.
func ensureReleasedFixture(t *testing.T, c adt.Client, objType, name, uri, source string) {
	t.Helper()
	ctx := context.Background()
	if _, err := c.GetObjectInfo(ctx, uri); err == nil {
		requireReleasedVersion(t, c, uri)
		return
	} else if adt.ClassifyError(err) != adt.ErrorNotFound {
		t.Fatalf("[0] GetObjectInfo: %v", err)
	}
	request, err := c.CreateTransport(ctx, adt.CreateTransportOptions{Description: "adtler #134 rollback fixture", Package: testPackage})
	if err != nil {
		t.Fatalf("[0] CreateTransport: %v", err)
	}
	if err := c.CreateObject(ctx, objType, name, testPackage, "adtler #134 rollback fixture", request); err != nil {
		t.Fatalf("[0] CreateObject: %s", redact(err.Error(), "", request))
	}
	_ = c.Logout(ctx)
	writeAndActivate(t, c, uri, source, request)
	res, err := c.ReleaseTransportWithTasks(ctx, request)
	if err != nil {
		t.Fatalf("[0] ReleaseTransportWithTasks: %s", redact(err.Error(), "", request))
	}
	// S/4 releases in a background job: wait for it before deciding.
	for i := 0; res.State == adt.TransportStateReleaseInProgress && i < 12; i++ {
		time.Sleep(5 * time.Second)
		info, infoErr := c.GetTransportInfo(ctx, request)
		if infoErr != nil {
			continue
		}
		res.State = info.State
	}
	if res.State != adt.TransportStateReleased {
		t.Skipf("[0] fixture created, but its request was not released (state %v); release it by hand and run again", res.State)
	}
	t.Logf("[0] fixture created and released")
}

// requireReleasedVersion skips the test unless the existing fixture at uri
// has a version from a released request. A fixture created by an earlier run
// whose request SAP did not release is still locked in that request, and
// changing it in a new one would fail for that reason alone.
func requireReleasedVersion(t *testing.T, c adt.Client, uri string) {
	t.Helper()
	ctx := context.Background()
	versions, err := c.GetVersionHistory(ctx, uri)
	if err != nil {
		t.Fatalf("[0] GetVersionHistory: %v", err)
	}
	checked := map[string]bool{}
	for _, v := range versions {
		if v.Transport == "" || checked[v.Transport] {
			continue
		}
		checked[v.Transport] = true
		// E070, not GetTransportInfo: the ECC organizer tree lists modifiable
		// requests only.
		qr, err := c.RunQuery(ctx, "SELECT TRSTATUS FROM E070 WHERE TRKORR = '"+adt.EscapeValue(v.Transport)+"'", 1)
		if err != nil {
			t.Fatalf("[0] read request status: %s", redact(err.Error(), "", v.Transport))
		}
		if len(qr.Rows) == 1 && len(qr.Rows[0]) == 1 {
			if st := strings.TrimSpace(qr.Rows[0][0]); st == "R" || st == "N" {
				return
			}
		}
	}
	t.Skipf("[0] fixture exists, but no version of it comes from a released request (%d requests checked); release its request by hand and run again", len(checked))
}

// resetFixtureSource writes source back if the fixture still holds the v2
// marker, so a failed rollback does not leave the next run starting from v2.
func resetFixtureSource(t *testing.T, c adt.Client, uri, source, request string) {
	t.Helper()
	src, err := c.GetSource(context.Background(), uri)
	if err != nil {
		t.Logf("[cleanup] read fixture: %v", err)
		return
	}
	if !strings.Contains(src.Source, "'v2'") {
		return
	}
	t.Logf("[cleanup] fixture not at v1, writing v1 back")
	writeAndActivate(t, c, uri, source, request)
}

func sortedCounts(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(out)
	return out
}
