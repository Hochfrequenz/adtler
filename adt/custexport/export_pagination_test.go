package custexport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
)

// paginationColumns is the column layout the pagination tests (#218) share.
func paginationColumns() []adt.QueryColumn {
	return []adt.QueryColumn{
		{Name: "MANDT", Type: "C"},
		{Name: "BUKRS", Type: "C"},
		{Name: "BUTXT", Type: "C"},
	}
}

// TestExportTable_MissingKeyColumnIsAnError guards #218: a full page whose
// result lacks a key column used to end the table silently with the rows read
// so far, reported as a success.
func TestExportTable_MissingKeyColumnIsAnError(t *testing.T) {
	calls := 0
	client := &mockClient{
		runQueryFn: func(_ context.Context, _ string, _ int) (*adt.QueryResult, error) {
			calls++
			return &adt.QueryResult{
				// BUKRS, a key field, is missing from the result.
				Columns: []adt.QueryColumn{{Name: "MANDT", Type: "C"}, {Name: "BUTXT", Type: "C"}},
				Rows:    [][]string{{"100", "Company A"}, {"100", "Company B"}},
			}, nil
		},
	}

	result, err := exportTable(context.Background(), client, "T001", []string{"MANDT", "BUKRS"}, nil, 2)
	if err == nil {
		t.Fatalf("expected an error for a result without a key column, got %d rows without error", result.TotalRows)
	}
	if !strings.Contains(err.Error(), "T001") || !strings.Contains(err.Error(), "BUKRS") {
		t.Errorf("error should name the table and the missing key column, got: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 query before giving up, got %d", calls)
	}
}

// TestExportTable_RepeatedPageIsAnError guards #218: when the next page starts
// where the previous one did, the export used to fetch the same page forever.
// The mock cancels the context after a few calls, so a regression fails the
// test instead of hanging the build.
func TestExportTable_RepeatedPageIsAnError(t *testing.T) {
	const runaway = 10
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	calls := 0
	client := &mockClient{
		runQueryFn: func(_ context.Context, _ string, _ int) (*adt.QueryResult, error) {
			calls++
			if calls > runaway {
				cancel()
				return nil, errors.New("test guard: pagination did not stop")
			}
			return &adt.QueryResult{
				Columns: paginationColumns(),
				Rows:    [][]string{{"100", "1000", "Company A"}, {"100", "2000", "Company B"}},
			}, nil
		},
	}

	result, err := exportTable(ctx, client, "T001", []string{"MANDT", "BUKRS"}, nil, 2)
	if err == nil {
		t.Fatalf("expected an error for a repeated page, got %d rows without error", result.TotalRows)
	}
	if calls != 2 {
		t.Errorf("expected the export to stop after the second page, got %d queries", calls)
	}
	for _, want := range []string{"T001", "page 2", "no progress"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// TestExportTable_ExactMultipleOfPageSize checks that a table whose row count
// is a multiple of the page size ends on an empty page without an error.
func TestExportTable_ExactMultipleOfPageSize(t *testing.T) {
	pages := [][][]string{
		{{"100", "1000", "A"}, {"100", "2000", "B"}},
		{{"100", "3000", "C"}, {"100", "4000", "D"}},
		{},
	}
	calls := 0
	client := &mockClient{
		runQueryFn: func(_ context.Context, _ string, _ int) (*adt.QueryResult, error) {
			if calls >= len(pages) {
				t.Fatalf("unexpected query %d", calls+1)
			}
			page := pages[calls]
			calls++
			return &adt.QueryResult{Columns: paginationColumns(), Rows: page}, nil
		},
	}

	result, err := exportTable(context.Background(), client, "T001", []string{"MANDT", "BUKRS"}, nil, 2)
	if err != nil {
		t.Fatalf("exportTable: %v", err)
	}
	if result.TotalRows != 4 || result.Pages != 3 {
		t.Errorf("expected 4 rows on 3 pages, got %d rows on %d pages", result.TotalRows, result.Pages)
	}
}

// TestExportTable_CompositeKeyPageBoundary checks that pages whose last rows
// share the first key field, but differ in the second, count as progress.
func TestExportTable_CompositeKeyPageBoundary(t *testing.T) {
	pages := [][][]string{
		{{"100", "A", "1"}, {"100", "A", "2"}},
		{{"100", "A", "3"}, {"100", "A", "4"}},
		{{"100", "B", "1"}},
	}
	calls := 0
	client := &mockClient{
		runQueryFn: func(_ context.Context, _ string, _ int) (*adt.QueryResult, error) {
			if calls >= len(pages) {
				t.Fatalf("unexpected query %d", calls+1)
			}
			page := pages[calls]
			calls++
			return &adt.QueryResult{
				Columns: []adt.QueryColumn{{Name: "MANDT"}, {Name: "GRP"}, {Name: "POS"}},
				Rows:    page,
			}, nil
		},
	}

	result, err := exportTable(context.Background(), client, "SOMETABLE", []string{"MANDT", "GRP", "POS"}, nil, 2)
	if err != nil {
		t.Fatalf("exportTable: %v", err)
	}
	if result.TotalRows != 5 || result.Pages != 3 {
		t.Errorf("expected 5 rows on 3 pages, got %d rows on %d pages", result.TotalRows, result.Pages)
	}
}
