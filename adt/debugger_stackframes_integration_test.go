//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestGetStackFrames_MultiSystem_Integration is the live test for adtler#203:
// with the debuggee suspended on an external breakpoint, GetStackFrames must
// report the current frame (ActiveFrame) with the right program, line and a
// SourceURI/SourceLine pair that points at the breakpoint statement in the
// object's source.
func TestGetStackFrames_MultiSystem_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name+"/report", func(t *testing.T) {
			name := fmt.Sprintf("Z_ADT_203_%d", time.Now().Unix()%100000)
			uri := "/sap/bc/adt/programs/programs/" + name
			createReportWithTestClass(t, sys.Client, name, uri)
			// Line 14 is "lv_val = 'test'." inside test_hello.
			f := suspendAndReadActiveFrame(t, sys, uri, uri+"/source/main", 14, "PROG/P", name)
			if f.Program != name || f.Line != 14 {
				t.Errorf("[%s] active frame: %+v, want program %s line 14", sys.Name, f, name)
			}
			assertFrameSource(t, sys, f, "lv_val = 'test'")
		})

		t.Run(sys.Name+"/class_method", func(t *testing.T) {
			name := fmt.Sprintf("ZCL_ADT_203_%d", time.Now().Unix()%100000)
			uri := "/sap/bc/adt/oo/classes/" + name
			reportName := fmt.Sprintf("Z_ADT_203C_%d", time.Now().Unix()%100000)
			reportURI := "/sap/bc/adt/programs/programs/" + reportName
			createClassAndTriggerReport(t, sys.Client, name, uri, reportName, reportURI)
			// Line 8 is "rv = 'adtler203'." inside GET_VAL of the global class.
			f := suspendAndReadActiveFrame(t, sys, reportURI, uri+"/source/main", 8, "CLAS/OC", name)
			if !strings.EqualFold(f.SourceURI, uri+"/source/main") || f.SourceLine != 8 {
				t.Errorf("[%s] active frame: %+v, want source %s/source/main line 8", sys.Name, f, uri)
			}
			if !strings.EqualFold(f.EventName, "GET_VAL") {
				t.Errorf("[%s] active frame: %+v, want event GET_VAL (the class method, not the calling test method)", sys.Name, f)
			}
			assertFrameSource(t, sys, f, "rv = 'adtler203'")
		})
	}
}

// suspendAndReadActiveFrame sets an external breakpoint, runs the unit tests of
// runURI so the debuggee hits it, attaches, reads the stack and returns the
// active frame. The debugger session is detached before returning.
func suspendAndReadActiveFrame(t *testing.T, sys integrationSystem, runURI, bpURI string, bpLine int, bpType, bpName string) adt.StackFrame {
	t.Helper()
	const listenSecs = 20
	ctx := context.Background()
	dbg := adt.NewDebugSession(sys.Client, sys.Config.User)
	attached := false
	// Cleanups run LIFO: end the debug session before the object is deleted.
	// There is no RemoveBreakpoint yet; StopListener ends the listener, and
	// the session-scoped breakpoint dies with the debugger session.
	t.Cleanup(func() {
		if attached {
			_, _ = dbg.Step(context.Background(), "detachDebugger")
		}
		_ = dbg.StopListener(context.Background())
	})

	bp, err := dbg.SetBreakpoint(ctx, bpURI, bpLine, bpType, bpName)
	if err != nil {
		t.Fatalf("[%s] SetBreakpoint: %v", sys.Name, err)
	}
	if bp.ErrorMessage != "" {
		t.Fatalf("[%s] SetBreakpoint: %s", sys.Name, bp.ErrorMessage)
	}

	type listenerOut struct {
		r   *adt.ListenerResult
		err error
	}
	listenerCh := make(chan listenerOut, 1)
	go func() {
		r, err := dbg.StartListener(ctx, listenSecs)
		listenerCh <- listenerOut{r, err}
	}()
	time.Sleep(4 * time.Second) // the listener long-poll gives no "registered" signal

	runCh := make(chan error, 1)
	go func() {
		_, err := sys.Client.RunUnitTests(ctx, runURI, 120)
		runCh <- err
	}()

	lo := <-listenerCh
	if lo.err != nil || lo.r.Status != "attached" {
		t.Fatalf("[%s] breakpoint not caught: status=%v err=%v", sys.Name, lo.r, lo.err)
	}
	if err := dbg.Attach(ctx, lo.r.DebuggeeID); err != nil {
		t.Fatalf("[%s] Attach: %v", sys.Name, err)
	}
	attached = true

	frames, err := dbg.GetStackFrames(ctx)
	if err != nil {
		t.Fatalf("[%s] GetStackFrames: %v", sys.Name, err)
	}
	for _, fr := range frames {
		t.Logf("[%s] frame: %+v", sys.Name, fr)
	}
	f, ok := adt.ActiveFrame(frames)
	if !ok {
		t.Fatalf("[%s] no frames returned", sys.Name)
	}

	// Let the debuggee finish so the unit-test run returns.
	_, _ = dbg.Step(ctx, "detachDebugger")
	attached = false
	select {
	case err := <-runCh:
		if err != nil {
			t.Logf("[%s] RunUnitTests after detach: %v", sys.Name, err)
		}
	case <-time.After(60 * time.Second):
		t.Logf("[%s] RunUnitTests did not return within 60 s after detach", sys.Name)
	}
	return f
}

// assertFrameSource reads the frame's source via SourceURI and checks that the
// statement at SourceLine contains want.
func assertFrameSource(t *testing.T, sys integrationSystem, f adt.StackFrame, want string) {
	t.Helper()
	if f.SourceURI == "" || f.SourceLine <= 0 {
		t.Fatalf("[%s] frame has no source position: %+v", sys.Name, f)
	}
	src, err := sys.Client.GetSource(context.Background(), strings.TrimSuffix(f.SourceURI, "/source/main"))
	if err != nil {
		t.Fatalf("[%s] GetSource(%s): %v", sys.Name, f.SourceURI, err)
	}
	lines := strings.Split(strings.ReplaceAll(src.Source, "\r\n", "\n"), "\n")
	if f.SourceLine > len(lines) {
		t.Fatalf("[%s] SourceLine %d beyond source of %d lines", sys.Name, f.SourceLine, len(lines))
	}
	if got := lines[f.SourceLine-1]; !strings.Contains(got, want) {
		t.Errorf("[%s] source line %d is %q, want it to contain %q", sys.Name, f.SourceLine, got, want)
	}
}

// createClassAndTriggerReport creates a $TMP global class (no local tests, so
// no class test include is needed, which is unreliable on ECC) whose public
// method GET_VAL holds the breakpoint statement at line 8, plus a $TMP report
// whose local test method calls it. Running the report's unit tests triggers
// the class method. Both objects are deleted on cleanup, the report first.
func createClassAndTriggerReport(t *testing.T, client adt.Client, className, classURI, reportName, reportURI string) {
	t.Helper()
	classSource := "CLASS " + className + " DEFINITION PUBLIC FINAL CREATE PUBLIC.\n" +
		"  PUBLIC SECTION.\n" +
		"    METHODS get_val RETURNING VALUE(rv) TYPE string.\n" +
		"ENDCLASS.\n" +
		"\n" +
		"CLASS " + className + " IMPLEMENTATION.\n" +
		"  METHOD get_val.\n" +
		"    rv = 'adtler203'.\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
	createSourceObject(t, client, "CLAS", className, classURI, classSource, "adtler#203 stack frames")

	reportSource := "REPORT " + reportName + ".\n" +
		"\n" +
		"CLASS lcl_test DEFINITION FOR TESTING RISK LEVEL HARMLESS DURATION SHORT.\n" +
		"  PRIVATE SECTION.\n" +
		"    METHODS test_get FOR TESTING.\n" +
		"ENDCLASS.\n" +
		"\n" +
		"CLASS lcl_test IMPLEMENTATION.\n" +
		"  METHOD test_get.\n" +
		"    DATA lv_val TYPE string.\n" +
		"    DATA lo TYPE REF TO " + className + ".\n" +
		"    CREATE OBJECT lo.\n" +
		"    lv_val = lo->get_val( ).\n" +
		"    cl_abap_unit_assert=>assert_equals( act = lv_val exp = 'adtler203' ).\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"
	createSourceObject(t, client, "PROG", reportName, reportURI, reportSource, "adtler#203 class trigger")
}

// createSourceObject creates, fills and activates a $TMP object and registers
// its deletion.
func createSourceObject(t *testing.T, client adt.Client, objType, name, uri, source, description string) {
	t.Helper()
	ctx := context.Background()
	if err := client.CreateObject(ctx, objType, name, "$TMP", description, ""); err != nil {
		t.Fatalf("CreateObject %s: %v", name, err)
	}
	// Delete WITHOUT locking first, see createReportWithTestClass (issue #187).
	t.Cleanup(func() {
		if err := client.DeleteObject(context.Background(), uri, "", ""); err != nil {
			t.Errorf("cleanup delete %s: %v — delete the $TMP object by hand", name, err)
		}
	})

	lh, err := client.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("LockObject %s: %v", name, err)
	}
	src, err := client.GetSource(ctx, uri)
	if err != nil {
		_ = client.UnlockObject(ctx, uri, lh)
		t.Fatalf("GetSource %s: %v", name, err)
	}
	if _, err := client.SetSource(ctx, uri, source, lh, "", src.ETag); err != nil {
		_ = client.UnlockObject(ctx, uri, lh)
		t.Fatalf("SetSource %s: %v", name, err)
	}
	if err := client.UnlockObject(ctx, uri, lh); err != nil {
		t.Fatalf("UnlockObject %s: %v", name, err)
	}
	res, err := client.ActivateObjects(ctx, []string{uri})
	if err != nil || !res.Success {
		t.Fatalf("Activate %s: err=%v messages=%v", name, err, res)
	}
}
