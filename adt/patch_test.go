package adt_test

import (
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

const fourLineSource = "line1\nline2\nline3\nline4"

func TestApplyOpsInsert(t *testing.T) {
	source := "line1\nline2\nline3"
	ops := []adt.PatchOp{
		{Type: "insert", AfterLine: 1, Content: "inserted"},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "line1\ninserted\nline2\nline3"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyOpsInsertAtZero(t *testing.T) {
	source := "line1\nline2"
	ops := []adt.PatchOp{
		{Type: "insert", AfterLine: 0, Content: "before"},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "before\nline1\nline2"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyOpsReplace(t *testing.T) {
	source := fourLineSource
	ops := []adt.PatchOp{
		{Type: "replace", FromLine: 2, ToLine: 3, Content: "new"},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "line1\nnew\nline4"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyOpsDelete(t *testing.T) {
	source := fourLineSource
	ops := []adt.PatchOp{
		{Type: "delete", FromLine: 2, ToLine: 3},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "line1\nline4"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyOpsSearchReplace(t *testing.T) {
	source := "REPORT ZTEST.\nDATA: lv_x TYPE i."
	ops := []adt.PatchOp{
		{Type: "search_replace", Search: "ZTEST", Replace: "ZNEW"},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "REPORT ZNEW.\nDATA: lv_x TYPE i."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyOpsSearchReplaceAll(t *testing.T) {
	source := "foo bar foo baz foo"
	ops := []adt.PatchOp{
		{Type: "search_replace", Search: "foo", Replace: "qux", All: true},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "qux bar qux baz qux"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// adtler#69: a multi-line LF search must match a CRLF source (SAP stores CRLF),
// and the replacement must be written back with CRLF so line endings stay
// consistent. Previously this silently no-op'd while issuing a fresh ETag.
func TestApplyOpsSearchReplace_CRLFSourceLFSearch(t *testing.T) {
	source := "REPORT ZTEST.\r\nDATA: lv_x TYPE i.\r\nWRITE lv_x."
	ops := []adt.PatchOp{
		// search/replace use bare LF, as a caller would naturally write them.
		{Type: "search_replace", Search: "DATA: lv_x TYPE i.\nWRITE lv_x.", Replace: "DATA: lv_y TYPE string.\nWRITE lv_y."},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "REPORT ZTEST.\r\nDATA: lv_y TYPE string.\r\nWRITE lv_y."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "\n") && !strings.Contains(got, "\r\n") {
		t.Errorf("result lost CRLF line endings: %q", got)
	}
}

// A single-line (newline-free) search must still match a CRLF source unchanged.
func TestApplyOpsSearchReplace_CRLFSourceSingleLineSearch(t *testing.T) {
	source := "REPORT ZTEST.\r\nDATA: lv_x TYPE i."
	ops := []adt.PatchOp{
		{Type: "search_replace", Search: "ZTEST", Replace: "ZNEW"},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "REPORT ZNEW.\r\nDATA: lv_x TYPE i."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// adtler#69: a search string that is genuinely absent must ERROR, not silently
// succeed with an unchanged source.
func TestApplyOpsSearchReplace_NotFoundErrors(t *testing.T) {
	source := "REPORT ZTEST.\nDATA: lv_x TYPE i."
	ops := []adt.PatchOp{
		{Type: "search_replace", Search: "DOES_NOT_EXIST", Replace: "x"},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err == nil {
		t.Fatalf("expected error for a search string not present, got success with %q", got)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should explain the search was not found, got: %v", err)
	}
}

// An empty search string is meaningless and must error rather than prepend the
// replacement at offset 0.
func TestApplyOpsSearchReplace_EmptySearchErrors(t *testing.T) {
	_, err := adt.ApplyPatchOps("REPORT ZTEST.", []adt.PatchOp{
		{Type: "search_replace", Search: "", Replace: "x"},
	})
	if err == nil {
		t.Fatal("expected error for an empty search string")
	}
}

func TestApplyOpsMultiple(t *testing.T) {
	// Two inserts: ops should be sorted descending by after_line so bottom-up application works.
	source := "line1\nline2\nline3"
	ops := []adt.PatchOp{
		{Type: "insert", AfterLine: 1, Content: "after1"},
		{Type: "insert", AfterLine: 2, Content: "after2"},
	}
	got, err := adt.ApplyPatchOps(source, ops)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// After sorting desc (after_line 2 first, then 1):
	// Apply after_line=2: line1, line2, after2, line3
	// Apply after_line=1: line1, after1, line2, after2, line3
	want := "line1\nafter1\nline2\nafter2\nline3"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestApplyOpsOverlapRejected(t *testing.T) {
	source := fourLineSource
	ops := []adt.PatchOp{
		{Type: "replace", FromLine: 1, ToLine: 3, Content: "new1"},
		{Type: "replace", FromLine: 2, ToLine: 4, Content: "new2"},
	}
	_, err := adt.ApplyPatchOps(source, ops)
	if err == nil {
		t.Fatal("expected error for overlapping ops")
	}
	if !strings.Contains(err.Error(), "overlap") {
		t.Errorf("error should mention overlap, got: %v", err)
	}
}
