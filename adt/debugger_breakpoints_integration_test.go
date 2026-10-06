//go:build integration

package adt_test

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// Integration tests for adtler#200: SetBreakpoints sends the complete list in
// one request, BreakpointScopeDebugger adds a breakpoint while the debuggee is
// halted, and RemoveBreakpoint deletes external breakpoints.
//
// Run with:
//
//	SAP_INTEGRATION_SYSTEMS="<r3-key>,<s4-key>" \
//	  go test -tags=integration -v -run 'Breakpoints_Integration' ./adt/...
//
// Each test debugs the unit-test method of its own throwaway $TMP report
// (createBpProgram) and uses a RunUnitTests run as the trigger, so it neither
// depends on nor changes the shared test report.

// bpIdeID is the IDE ID these tests register under, so their listener does
// not conflict with a debug session another tool keeps open for the same user.
const bpIdeID = "adtler-it-200"

// bpProgramLines is the source of the throwaway report. Built from
// double-quoted strings, not a Go backtick literal, per the CLAUDE.md
// ABAP-fixture rule. bpFirstLine and bpSecondLine are the 1-based lines of
// the two consecutive executable statements in the test method.
var bpProgramLines = []string{
	"REPORT %s.",
	"",
	"CLASS lcl_test DEFINITION FOR TESTING RISK LEVEL HARMLESS DURATION SHORT.",
	"  PRIVATE SECTION.",
	"    METHODS test_lines FOR TESTING.",
	"ENDCLASS.",
	"",
	"CLASS lcl_test IMPLEMENTATION.",
	"  METHOD test_lines.",
	"    DATA lv_val TYPE string.",
	"    lv_val = 'first'.",
	"    lv_val = 'second'.",
	"    cl_abap_unit_assert=>assert_equals( act = lv_val exp = 'second' ).",
	"  ENDMETHOD.",
	"ENDCLASS.",
}

const (
	bpFirstLine  = 11
	bpSecondLine = 12
)

// createBpProgram creates a fresh $TMP report with a unit-test method,
// activates it and registers its deletion. Returns the program URI.
func createBpProgram(t *testing.T, client adt.Client) string {
	t.Helper()
	ctx := context.Background()
	name := fmt.Sprintf("Z_ADT_BP200_%d", time.Now().UnixNano()%1000000000000)
	uri := "/sap/bc/adt/programs/programs/" + name
	if err := client.CreateObject(ctx, "PROG", name, "$TMP", "adtler#200 breakpoint probe", ""); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	t.Cleanup(func() {
		if err := client.DeleteObject(context.Background(), uri, "", ""); err != nil {
			t.Logf("cleanup: delete probe report failed: %v", err)
		}
	})

	src := fmt.Sprintf(strings.Join(bpProgramLines, "\n")+"\n", strings.ToLower(name))
	lock, err := client.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	cur, err := client.GetSource(ctx, uri)
	if err != nil {
		_ = client.UnlockObject(ctx, uri, lock)
		t.Fatalf("GetSource: %v", err)
	}
	if _, err := client.SetSource(ctx, uri, src, lock, "", cur.ETag); err != nil {
		_ = client.UnlockObject(ctx, uri, lock)
		t.Fatalf("SetSource: %v", err)
	}
	_ = client.UnlockObject(ctx, uri, lock)
	res, err := client.ActivateObjects(ctx, []string{uri})
	if err != nil {
		t.Fatalf("ActivateObjects: %v", err)
	}
	if !res.Success {
		t.Fatalf("activation failed: %d messages", len(res.Messages))
	}
	return uri
}

var stackLineRe = regexp.MustCompile(`\bline="(\d+)"`)

// stackTopLine returns the source line of the topmost stack entry.
func stackTopLine(t *testing.T, dbg *adt.DebugSession) int {
	t.Helper()
	data, err := dbg.GetStack(context.Background())
	if err != nil {
		t.Fatalf("GetStack: %v", err)
	}
	m := stackLineRe.FindSubmatch(data)
	if m == nil {
		t.Fatalf("GetStack: no line attribute in %d-byte response", len(data))
	}
	n, _ := strconv.Atoi(string(m[1]))
	return n
}

// bpTrigger starts a listener, runs the report's unit tests and returns
// whether the listener caught the run. When it did, the debugger is attached
// on return. The returned channel closes when the unit-test run has finished.
func bpTrigger(t *testing.T, client adt.Client, dbg *adt.DebugSession, progURI string, listenSeconds int) (caught bool, runDone <-chan struct{}) {
	t.Helper()
	ctx := context.Background()
	type listenerOut struct {
		result *adt.ListenerResult
		err    error
	}
	listenerCh := make(chan listenerOut, 1)
	go func() {
		r, err := dbg.StartListener(ctx, listenSeconds)
		listenerCh <- listenerOut{r, err}
	}()
	// Give the listener time to register before the run starts.
	time.Sleep(2 * time.Second)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = client.RunUnitTests(ctx, progURI, 120)
	}()

	lo := <-listenerCh
	if lo.err != nil {
		t.Fatalf("StartListener: %v", lo.err)
	}
	if lo.result.Status != "attached" || lo.result.DebuggeeID == "" {
		return false, done
	}
	if err := dbg.Attach(ctx, lo.result.DebuggeeID); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	return true, done
}

// bpRelease lets a halted debuggee run to completion and waits for the run.
func bpRelease(t *testing.T, dbg *adt.DebugSession, runDone <-chan struct{}) {
	t.Helper()
	_, err := dbg.Step(context.Background(), "stepContinue")
	var ended *adt.DebuggeeEndedError
	if err != nil && !errors.As(err, &ended) {
		t.Logf("stepContinue: %v", err)
	}
	select {
	case <-runDone:
	case <-time.After(2 * time.Minute):
		t.Log("unit-test run did not finish within 2 minutes")
	}
}

// bpSet sets bps in scope, fails the test unless all were set, and registers
// their removal.
func bpSet(t *testing.T, dbg *adt.DebugSession, scope adt.BreakpointScope, bps []adt.LineBreakpoint) []string {
	t.Helper()
	results, err := dbg.SetBreakpoints(context.Background(), scope, bps)
	if err != nil {
		t.Fatalf("SetBreakpoints(%s): %v", scope, err)
	}
	ids := make([]string, len(results))
	for i, r := range results {
		if !r.IsSet() {
			t.Fatalf("SetBreakpoints(%s)[%d] not set: kind=%q message=%q", scope, i, r.ErrorKind, r.ErrorMessage)
		}
		ids[i] = r.ID
	}
	if scope == adt.BreakpointScopeExternal {
		t.Cleanup(func() {
			for _, id := range ids {
				_ = dbg.RemoveBreakpoint(context.Background(), scope, id)
			}
		})
	}
	return ids
}

// TestSetBreakpoints_TwoInOneRequest_Integration: two external breakpoints
// sent in one request are both active — the run stops at the first line and,
// after stepContinue, at the second. Calling SetBreakpoint once per line
// instead keeps only the last one on SAP_BASIS 816.
func TestSetBreakpoints_TwoInOneRequest_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			prog := createBpProgram(t, sys.Client)
			first, second := bpFirstLine, bpSecondLine
			dbg := adt.NewDebugSession(sys.Client, sys.Config.User, bpIdeID)
			src := prog + "/source/main"

			bpSet(t, dbg, adt.BreakpointScopeExternal, []adt.LineBreakpoint{
				{ObjectURI: src, Line: second},
				{ObjectURI: src, Line: first},
			})

			caught, runDone := bpTrigger(t, sys.Client, dbg, prog, 60)
			if !caught {
				t.Fatal("run was not caught")
			}
			defer bpRelease(t, dbg, runDone)
			if got := stackTopLine(t, dbg); got != first {
				t.Fatalf("first stop: line %d, want %d", got, first)
			}
			if _, err := dbg.Step(context.Background(), "stepContinue"); err != nil {
				t.Fatalf("stepContinue: %v", err)
			}
			if got := stackTopLine(t, dbg); got != second {
				t.Fatalf("second stop: line %d, want %d", got, second)
			}
			t.Logf("stopped at both lines (%d, %d)", first, second)
		})
	}
}

// TestSetBreakpoints_DebuggerScopeWhileHalted_Integration: a breakpoint added
// with BreakpointScopeDebugger while the debuggee is halted is honoured by the
// next stepContinue, and the debugger stays attached. Sent the way
// SetBreakpoint did before adtler#200, the same request detached the debugger
// on SAP_BASIS 750.
func TestSetBreakpoints_DebuggerScopeWhileHalted_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			prog := createBpProgram(t, sys.Client)
			first, second := bpFirstLine, bpSecondLine
			dbg := adt.NewDebugSession(sys.Client, sys.Config.User, bpIdeID)
			src := prog + "/source/main"

			bpSet(t, dbg, adt.BreakpointScopeExternal, []adt.LineBreakpoint{{ObjectURI: src, Line: first}})

			caught, runDone := bpTrigger(t, sys.Client, dbg, prog, 60)
			if !caught {
				t.Fatal("run was not caught")
			}
			defer bpRelease(t, dbg, runDone)
			if got := stackTopLine(t, dbg); got != first {
				t.Fatalf("first stop: line %d, want %d", got, first)
			}

			bpSet(t, dbg, adt.BreakpointScopeDebugger, []adt.LineBreakpoint{{ObjectURI: src, Line: second}})

			if _, err := dbg.Step(context.Background(), "stepContinue"); err != nil {
				t.Fatalf("stepContinue after debugger-scope breakpoint: %v", err)
			}
			if got := stackTopLine(t, dbg); got != second {
				t.Fatalf("second stop: line %d, want %d", got, second)
			}
			t.Logf("debugger-scope breakpoint honoured at line %d", second)
		})
	}
}

// TestSetBreakpoints_DebuggerScopeWithoutAttach_Integration: a
// BreakpointScopeDebugger request without an attached debugger fails with
// ErrNoSessionAttached.
func TestSetBreakpoints_DebuggerScopeWithoutAttach_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			dbg := adt.NewDebugSession(sys.Client, sys.Config.User, bpIdeID)
			_, err := dbg.SetBreakpoints(context.Background(), adt.BreakpointScopeDebugger, []adt.LineBreakpoint{
				{ObjectURI: testReportURI + "/source/main", Line: 1},
			})
			if !errors.Is(err, adt.ErrNoSessionAttached) {
				var adtErr *adt.ADTError
				if errors.As(err, &adtErr) {
					t.Fatalf("got %d type=%q properties=%v, want ErrNoSessionAttached", adtErr.StatusCode, adtErr.Type, adtErr.Properties)
				}
				t.Fatalf("got %v, want ErrNoSessionAttached", err)
			}
		})
	}
}

// TestRemoveBreakpoint_Integration: after RemoveBreakpoint for every ID that
// was set, the next run is not caught.
func TestRemoveBreakpoint_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			prog := createBpProgram(t, sys.Client)
			first, second := bpFirstLine, bpSecondLine
			dbg := adt.NewDebugSession(sys.Client, sys.Config.User, bpIdeID)
			src := prog + "/source/main"

			ids := bpSet(t, dbg, adt.BreakpointScopeExternal, []adt.LineBreakpoint{
				{ObjectURI: src, Line: first},
				{ObjectURI: src, Line: second},
			})
			for _, id := range ids {
				if err := dbg.RemoveBreakpoint(context.Background(), adt.BreakpointScopeExternal, id); err != nil {
					t.Fatalf("RemoveBreakpoint: %v", err)
				}
			}

			caught, runDone := bpTrigger(t, sys.Client, dbg, prog, 20)
			if caught {
				bpRelease(t, dbg, runDone)
				t.Fatal("run was caught after every breakpoint was removed")
			}
			select {
			case <-runDone:
			case <-time.After(2 * time.Minute):
				t.Log("unit-test run did not finish within 2 minutes")
			}
			t.Logf("%d breakpoints removed; run not caught", len(ids))
		})
	}
}
