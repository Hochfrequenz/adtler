//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// TestRunUnitTests_OutlastsShortClient_MultiSystem_Integration is the live
// regression test for issue #186: a unit-test run whose HTTP request stays open
// longer than the short client's 30 s cap must still return its result.
//
// The run is held open the way aibap.mcp#558 will hold it: an external
// breakpoint in the test method suspends the debuggee, the test keeps it
// suspended for holdFor (> 30 s), then ends the debugger session. Before the fix
// RunUnitTests failed with "Client.Timeout exceeded while awaiting headers";
// after it, RunUnitTests returns the run result.
func TestRunUnitTests_OutlastsShortClient_MultiSystem_Integration(t *testing.T) {
	const (
		holdFor    = 35 * time.Second
		listenSecs = 15 // the breakpoint is hit within seconds of the trigger
		bpLine     = 14 // lv_val = 'test'. inside test_hello
		runBudget  = 120
	)
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			name := fmt.Sprintf("Z_ADT_186_%d", time.Now().Unix()%100000)
			uri := "/sap/bc/adt/programs/programs/" + name
			createReportWithTestClass(t, sys.Client, name, uri)

			dbg := adt.NewDebugSession(sys.Client, sys.Config.User)
			attached := false
			// End the debug session before the report is deleted (cleanups run
			// LIFO), so a failed run does not leave a suspended debuggee behind.
			t.Cleanup(func() {
				if attached {
					_, _ = dbg.Step(context.Background(), "detachDebugger")
				}
				_ = dbg.StopListener(context.Background())
			})

			bp, err := dbg.SetBreakpoint(ctx, uri+"/source/main", bpLine, "PROG/P", name)
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

			type runOut struct {
				res *adt.TestResult
				err error
			}
			runCh := make(chan runOut, 1)
			start := time.Now()
			go func() {
				res, err := sys.Client.RunUnitTests(ctx, uri, runBudget)
				runCh <- runOut{res, err}
			}()

			lo := <-listenerCh
			if lo.err != nil || lo.r.Status != "attached" {
				t.Fatalf("[%s] breakpoint not caught: status=%v err=%v", sys.Name, lo.r, lo.err)
			}
			if err := dbg.Attach(ctx, lo.r.DebuggeeID); err != nil {
				t.Fatalf("[%s] Attach: %v", sys.Name, err)
			}
			attached = true

			t.Logf("[%s] holding the debuggee for %v", sys.Name, holdFor)
			time.Sleep(holdFor)
			if _, err := dbg.Step(ctx, "stepContinue"); err != nil {
				t.Logf("[%s] stepContinue: %v (a debuggee-ended error is expected here)", sys.Name, err)
			}

			ro := <-runCh
			elapsed := time.Since(start)
			if ro.err != nil {
				t.Fatalf("[%s] RunUnitTests failed after %v: %v — issue #186 (short-client cap) not fixed",
					sys.Name, elapsed, ro.err)
			}
			if elapsed < 30*time.Second {
				t.Fatalf("[%s] run finished after %v; the request was not held past the 30 s cap, so this run proves nothing",
					sys.Name, elapsed)
			}
			if ro.res.Passed != 1 {
				t.Errorf("[%s] Passed: got %d, want 1", sys.Name, ro.res.Passed)
			}
			t.Logf("[%s] RunUnitTests returned after %v with passed=%d", sys.Name, elapsed, ro.res.Passed)
		})
	}
}

// createReportWithTestClass creates, fills and activates a $TMP report whose
// local test class has a single passing method, and registers its deletion.
// Line 14 is the first executable statement of the test method.
func createReportWithTestClass(t *testing.T, client adt.Client, name, uri string) {
	t.Helper()
	ctx := context.Background()
	if err := client.CreateObject(ctx, "PROG", name, "$TMP", "adtler#186 long unit-test run", ""); err != nil {
		t.Fatalf("CreateObject: %v", err)
	}
	// Delete WITHOUT locking first: DeleteObject ignores the lock handle and
	// deletes statelessly, so on S/4HANA a preceding LockObject blocks the
	// delete and leaves an orphaned TRDIR lock (issue #187).
	t.Cleanup(func() {
		if err := client.DeleteObject(context.Background(), uri, "", ""); err != nil {
			t.Errorf("cleanup delete %s: %v — delete the $TMP report by hand", name, err)
		}
	})

	source := "REPORT " + name + ".\n" +
		"DATA: lv_test TYPE string.\n" +
		"lv_test = 'Hello debugger'.\n" +
		"WRITE: / lv_test.\n" +
		"\n" +
		"CLASS lcl_test DEFINITION FOR TESTING RISK LEVEL HARMLESS DURATION SHORT.\n" +
		"  PRIVATE SECTION.\n" +
		"    METHODS test_hello FOR TESTING.\n" +
		"ENDCLASS.\n" +
		"\n" +
		"CLASS lcl_test IMPLEMENTATION.\n" +
		"  METHOD test_hello.\n" +
		"    DATA: lv_val TYPE string.\n" +
		"    lv_val = 'test'.\n" +
		"    cl_abap_unit_assert=>assert_equals( act = lv_val exp = 'test' ).\n" +
		"  ENDMETHOD.\n" +
		"ENDCLASS.\n"

	lh, err := client.LockObject(ctx, uri)
	if err != nil {
		t.Fatalf("LockObject: %v", err)
	}
	src, err := client.GetSource(ctx, uri)
	if err != nil {
		_ = client.UnlockObject(ctx, uri, lh)
		t.Fatalf("GetSource: %v", err)
	}
	if _, err := client.SetSource(ctx, uri, source, lh, "", src.ETag); err != nil {
		_ = client.UnlockObject(ctx, uri, lh)
		t.Fatalf("SetSource: %v", err)
	}
	if err := client.UnlockObject(ctx, uri, lh); err != nil {
		t.Fatalf("UnlockObject: %v", err)
	}
	res, err := client.ActivateObjects(ctx, []string{uri})
	if err != nil || !res.Success {
		t.Fatalf("Activate: err=%v messages=%v", err, res)
	}
}
