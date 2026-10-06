//go:build integration

package adt_test

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// varsTestSource returns the report source and the 1-based line of the
// cl_abap_unit_assert statement, computed from the slice (never hard-coded).
func varsTestSource(name string) (string, int) {
	lines := []string{
		"REPORT " + name + ".",
		"CLASS lcl_item DEFINITION.",
		"  PUBLIC SECTION.",
		"    DATA mv_name TYPE string.",
		"    DATA mt_tags TYPE string_table.",
		"ENDCLASS.",
		"CLASS lcl_item IMPLEMENTATION.",
		"ENDCLASS.",
		"CLASS lcl_test DEFINITION FOR TESTING RISK LEVEL HARMLESS DURATION SHORT.",
		"  PRIVATE SECTION.",
		"    TYPES: BEGIN OF ty_row, id TYPE i, text TYPE string, flag TYPE abap_bool, END OF ty_row.",
		"    METHODS test_vars FOR TESTING.",
		"ENDCLASS.",
		"CLASS lcl_test IMPLEMENTATION.",
		"  METHOD test_vars.",
		"    DATA ls_row TYPE ty_row.",
		"    DATA lt_rows TYPE STANDARD TABLE OF ty_row WITH EMPTY KEY.",
		"    DATA lo_item TYPE REF TO lcl_item.",
		"    DATA lr_data TYPE REF TO ty_row.",
		"    ls_row = VALUE #( id = 7 text = `seven` flag = abap_true ).",
		"    DO 5 TIMES.",
		"      APPEND VALUE #( id = sy-index text = |row { sy-index }| ) TO lt_rows.",
		"    ENDDO.",
		"    lo_item = NEW #( ).",
		"    lo_item->mv_name = `item`.",
		"    APPEND `a` TO lo_item->mt_tags.",
		"    APPEND `b` TO lo_item->mt_tags.",
		"    lr_data = REF #( ls_row ).",
		"    cl_abap_unit_assert=>assert_equals( act = lines( lt_rows ) exp = 5 ).",
		"  ENDMETHOD.",
		"ENDCLASS.",
	}
	bpLine := 0
	for i, l := range lines {
		if strings.Contains(l, "cl_abap_unit_assert") {
			bpLine = i + 1
			break
		}
	}
	return strings.Join(lines, "\n") + "\n", bpLine
}

// findVar returns the variable whose ID or Name equals key, or nil. The server
// identifies children by ID (e.g. LS_ROW-TEXT); Name is the short display name.
func findVar(vars []adt.DebugVariable, key string) *adt.DebugVariable {
	for i := range vars {
		if vars[i].ID == key || vars[i].Name == key {
			return &vars[i]
		}
	}
	return nil
}

// trimPad trims trailing spaces only: SAP keeps ABAP padding in values.
func trimPad(s string) string { return strings.TrimRight(s, " ") }

// TestDebugVariables_MultiSystem_Integration is the live test for issue #201:
// structured variable access (structure, table, object reference, data
// reference) at a breakpoint hit by a unit test run.
func TestDebugVariables_MultiSystem_Integration(t *testing.T) {
	ctx := context.Background()
	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			name := fmt.Sprintf("Z_ADT_201_%06d", rand.Intn(1000000)) //nolint:gosec // throwaway name
			uri := "/sap/bc/adt/programs/programs/" + name
			source, bpLine := varsTestSource(name)
			if bpLine == 0 {
				t.Fatal("breakpoint line not found in source")
			}
			createReportWithSource(t, sys.Client, name, uri, source)

			dbg := adt.NewDebugSession(sys.Client, sys.Config.User)
			attached := false

			bp, err := dbg.SetBreakpoint(ctx, uri+"/source/main", bpLine, "PROG/P", name)
			if err != nil {
				t.Fatalf("[%s] SetBreakpoint: %v", sys.Name, err)
			}
			if bp.ErrorMessage != "" {
				t.Fatalf("[%s] SetBreakpoint: %s", sys.Name, bp.ErrorMessage)
			}

			// Registered before the listener cleanup so it runs after it (LIFO).
			t.Cleanup(func() {
				if err := dbg.RemoveBreakpoint(context.Background(), adt.BreakpointScopeExternal, bp.ID); err != nil {
					t.Errorf("[%s] cleanup RemoveBreakpoint %s: %v", sys.Name, bp.ID, err)
				}
			})

			// End the debug session before the breakpoint and report are removed
			// (cleanups run LIFO), so a failed run leaves no suspended debuggee.
			t.Cleanup(func() {
				if attached {
					_, _ = dbg.Step(context.Background(), "detachDebugger")
				}
				_ = dbg.StopListener(context.Background())
			})

			type listenerOut struct {
				r   *adt.ListenerResult
				err error
			}
			listenerCh := make(chan listenerOut, 1)
			go func() {
				r, err := dbg.StartListener(ctx, 30)
				listenerCh <- listenerOut{r, err}
			}()
			time.Sleep(4 * time.Second) // the listener long-poll gives no "registered" signal

			go func() { _, _ = sys.Client.RunUnitTests(ctx, uri, 120) }()

			lo := <-listenerCh
			if lo.err != nil || lo.r == nil || lo.r.Status != "attached" {
				t.Fatalf("[%s] breakpoint not caught: status=%v err=%v", sys.Name, lo.r, lo.err)
			}
			if err := dbg.Attach(ctx, lo.r.DebuggeeID); err != nil {
				t.Fatalf("[%s] Attach: %v", sys.Name, err)
			}
			attached = true

			locals, err := dbg.GetChildVariables(ctx, "@LOCALS")
			if err != nil {
				t.Fatalf("[%s] GetChildVariables(@LOCALS): %v", sys.Name, err)
			}
			wantMeta := map[string]string{"LS_ROW": "structure", "LT_ROWS": "table", "LO_ITEM": "objectref", "LR_DATA": "dataref"}
			for n, meta := range wantMeta {
				v := findVar(locals.Variables, n)
				if v == nil {
					t.Errorf("[%s] @LOCALS lacks %s", sys.Name, n)
					continue
				}
				if v.MetaType != meta {
					t.Errorf("[%s] %s MetaType: got %q, want %q", sys.Name, n, v.MetaType, meta)
				}
			}
			if v := findVar(locals.Variables, "LT_ROWS"); v != nil && v.TableLines != 5 {
				t.Errorf("[%s] LT_ROWS TableLines: got %d, want 5", sys.Name, v.TableLines)
			}

			row, err := dbg.GetChildVariables(ctx, "LS_ROW")
			if err != nil {
				t.Fatalf("[%s] GetChildVariables(LS_ROW): %v", sys.Name, err)
			}
			if v := findVar(row.Variables, "LS_ROW-TEXT"); v == nil {
				t.Errorf("[%s] LS_ROW children lack LS_ROW-TEXT: %+v", sys.Name, row.Variables)
			} else if trimPad(v.Value) != "seven" {
				t.Errorf("[%s] LS_ROW-TEXT value: got %q, want %q", sys.Name, v.Value, "seven")
			}

			item, err := dbg.GetChildVariables(ctx, "LO_ITEM")
			if err != nil {
				t.Fatalf("[%s] GetChildVariables(LO_ITEM): %v", sys.Name, err)
			}
			foundName := false
			for _, v := range item.Variables {
				if strings.HasSuffix(v.ID, "-MV_NAME") || v.Name == "MV_NAME" {
					foundName = true
					if trimPad(v.Value) != "item" {
						t.Errorf("[%s] %s value: got %q, want %q", sys.Name, v.Name, v.Value, "item")
					}
				}
			}
			if !foundName {
				t.Errorf("[%s] LO_ITEM children lack an attribute with ID ending in -MV_NAME or Name MV_NAME: %+v", sys.Name, item.Variables)
			}

			ref, err := dbg.GetChildVariables(ctx, "LR_DATA")
			if err != nil {
				t.Fatalf("[%s] GetChildVariables(LR_DATA): %v", sys.Name, err)
			}
			if findVar(ref.Variables, "LR_DATA->*") == nil {
				t.Errorf("[%s] LR_DATA children lack LR_DATA->*: %+v", sys.Name, ref.Variables)
			} else {
				deref, err := dbg.GetChildVariables(ctx, "LR_DATA->*")
				if err != nil {
					t.Fatalf("[%s] GetChildVariables(LR_DATA->*): %v", sys.Name, err)
				}
				if findVar(deref.Variables, "LR_DATA->TEXT") == nil {
					t.Errorf("[%s] LR_DATA->* children lack LR_DATA->TEXT: %+v", sys.Name, deref.Variables)
				}
			}

			page, err := dbg.GetTableRows(ctx, "LT_ROWS", 2, 2, "TEXT")
			if err != nil {
				t.Fatalf("[%s] GetTableRows: %v", sys.Name, err)
			}
			if len(page.Rows) != 2 || page.Rows[0].Index != 2 || page.Rows[1].Index != 3 {
				t.Fatalf("[%s] GetTableRows rows: got %+v, want indexes 2 and 3", sys.Name, page.Rows)
			}
			for i, want := range []string{"row 2", "row 3"} {
				row := page.Rows[i]
				// Field selection: only TEXT may come back.
				if len(row.Fields) != 1 || !strings.HasSuffix(row.Fields[0].Path, "TEXT") {
					t.Errorf("[%s] row %d fields: got %+v, want exactly one TEXT field", sys.Name, row.Index, row.Fields)
					continue
				}
				if got := trimPad(row.Fields[0].Value); got != want {
					t.Errorf("[%s] row %d TEXT: got %q, want %q", sys.Name, row.Index, got, want)
				}
			}

			// Paging on an object attribute (SAP may echo it in instance-ID form).
			tagPage, err := dbg.GetTableRows(ctx, "LO_ITEM->MT_TAGS", 1, 10)
			if err != nil {
				t.Fatalf("[%s] GetTableRows(LO_ITEM->MT_TAGS): %v", sys.Name, err)
			}
			if len(tagPage.Rows) != 2 {
				t.Errorf("[%s] GetTableRows(LO_ITEM->MT_TAGS): got %d rows, want 2: %+v", sys.Name, len(tagPage.Rows), tagPage.Rows)
			}

			tags, err := dbg.GetVariables(ctx, "LO_ITEM->MT_TAGS[2]")
			if err != nil {
				t.Fatalf("[%s] GetVariables(LO_ITEM->MT_TAGS[2]): %v", sys.Name, err)
			}
			if len(tags) != 1 {
				t.Errorf("[%s] GetVariables: got %d variables, want 1", sys.Name, len(tags))
			}

			if _, err := dbg.Step(ctx, "detachDebugger"); err != nil {
				t.Logf("[%s] detachDebugger: %v", sys.Name, err)
			}
			attached = false
		})
	}
}
