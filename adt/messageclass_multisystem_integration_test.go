//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestGetMessageClass_MultiSystem_Integration regression-tests adtler#5
// against every system in the SAP_INTEGRATION_SYSTEMS whitelist.
//
// Before the fix, GetMessageClass sent Accept: application/xml. R/3 was
// lenient and accepted it; S/4 returned HTTP 406 ExceptionResourceNotAcceptable
// and explicitly named application/vnd.sap.adt.mc.messageclass+xml as the
// only supported representation. The bug therefore went unnoticed in any test
// run that only hit R/3 — and the existing single-system integration tests
// (TestGetMessageClass_Integration in messageclass_integration_test.go) were
// effectively R/3-only on the heavy-test rig.
//
// This test runs against BOTH systems via eachSystem(t). It calls
// GetMessageClass for the standard SAP message classes 00, 01, and SY (each
// of which exists on every R/3 and S/4 system since R/3 1.0). A failure on
// either system means the Accept-header fix has regressed.
func TestGetMessageClass_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			for _, mc := range []string{"00", "01", "SY"} {
				t.Run(mc, func(t *testing.T) {
					result, err := sys.Client.GetMessageClass(ctx, mc)
					if err != nil {
						t.Fatalf("GetMessageClass(%q) on %q: %v", mc, sys.Name, err)
					}
					if !strings.EqualFold(result.Name, mc) {
						t.Errorf("Name: got %q, want %q (case-insensitive)", result.Name, mc)
					}
					if len(result.Messages) == 0 {
						t.Errorf("expected at least one message in standard class %q on %q", mc, sys.Name)
					}
					t.Logf("[%s] class %q: %d messages, ETag=%q", sys.Name, result.Name, len(result.Messages), result.ETag)
				})
			}
		})
	}
}

// TestSetMessages_MultiSystem_Integration also regression-tests adtler#5,
// for the PUT path. The PUT was missing an Accept header entirely (only
// Content-Type was set), which S/4 also rejected with 406. R/3 silently
// accepted the missing Accept and let the call through.
//
// The test creates (or reuses) a Z* message class on each whitelisted system,
// reads its ETag, then writes a single message via SetMessages. A failure on
// any system means either the Content-Type or the Accept header is no longer
// the vendor type S/4 demands.
func TestSetMessages_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			const msgClass = "Z_ADT_MCP_MULTISYS"

			// Ensure the message class exists on this system.
			if _, err := sys.Client.GetMessageClass(ctx, msgClass); err != nil {
				if cerr := sys.Client.CreateObject(ctx, "MSAG", msgClass, "$TMP", "MCP multi-system test", ""); cerr != nil {
					t.Fatalf("CreateObject MSAG on %q: %v", sys.Name, cerr)
				}
				t.Logf("[%s] created %s", sys.Name, msgClass)
			}

			info, err := sys.Client.GetMessageClass(ctx, msgClass)
			if err != nil {
				t.Fatalf("GetMessageClass for ETag on %q: %v", sys.Name, err)
			}

			err = sys.Client.SetMessages(ctx, msgClass, info.ETag, []adt.Message{
				{Number: "001", Text: "MCP test multi-system &1", SelfExpl: true},
			})
			if err != nil {
				t.Fatalf("SetMessages on %q: %v", sys.Name, err)
			}
			t.Logf("[%s] SetMessages OK", sys.Name)
		})
	}
}
