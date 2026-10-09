//go:build integration

package adt_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
	"github.com/Hochfrequenz/adtler/adt/custexport"
)

// TestCustexportPagination_RowCountMatchesSource_Integration guards #218 on a
// live system: the customizing export must deliver exactly as many rows as the
// table holds and must report no per-table error, while it needs several pages.
//
// The test discovers a suitable customizing table at runtime (a transparent
// customizing table with a key besides the client, between 10 and 5000 rows)
// and skips when it finds none. It runs the export twice:
//
//   - with a page size that leaves a short last page, and
//   - where the row count has a divisor, with a page size that divides the row
//     count exactly, so the export ends on an empty page.
//
// Both runs must return the row count that SELECT COUNT(*) reports through the
// data preview. It logs counts and page numbers only, never a table name or
// table contents.
//
// Run with:
//
//	SAP_INTEGRATION_SYSTEMS="<r3-key>,<s4-key>" \
//	  go test -tags=integration -v -run TestCustexportPagination_RowCountMatchesSource_Integration ./adt/...
func TestCustexportPagination_RowCountMatchesSource_Integration(t *testing.T) {
	for _, sys := range eachSystem(t) {
		sys := sys
		t.Run(sys.Name, func(t *testing.T) {
			table, rows := discoverPaginationTable(t, sys.Client)

			// Three pages, the last one short.
			shortLast := (rows + 2) / 3
			if rows%shortLast == 0 {
				shortLast++ // keep the last page short, not exact
			}
			runPaginationExport(t, sys.Client, table, rows, shortLast, ceilDiv(rows, shortLast))

			// An exact multiple of the page size: full pages, then an empty one.
			if d := smallestFactor(rows); d < rows {
				exact := rows / d
				runPaginationExport(t, sys.Client, table, rows, exact, d+1)
			} else {
				t.Logf("row count %d has no divisor, skipping the exact-multiple run", rows)
			}
		})
	}
}

// runPaginationExport exports one table with the given page size and checks
// the row count, the page count and the absence of per-table errors.
func runPaginationExport(t *testing.T, client adt.Client, table string, rows, pageSize, wantPages int) {
	t.Helper()
	outputDir := t.TempDir()

	summary, err := custexport.RunExport(context.Background(), client, custexport.ExportConfig{
		OutputDir: outputDir,
		Tables:    []string{table},
		PageSize:  pageSize,
		Workers:   1,
	})
	if err != nil {
		t.Fatalf("RunExport failed: %v", err)
	}
	for i, te := range summary.Errors {
		// Not te.Error: the message names the table.
		t.Errorf("per-table error %d (%T)", i, te.Err)
	}
	if len(summary.Errors) > 0 {
		t.FailNow()
	}
	if summary.ExportedTables != 1 || summary.TotalRows != rows {
		t.Errorf("exported %d table(s) with %d rows, source has %d rows (page size %d)",
			summary.ExportedTables, summary.TotalRows, rows, pageSize)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, "json", strings.ReplaceAll(table, "/", "#")+".json"))
	if err != nil {
		t.Fatalf("reading the table's JSON file failed: %v", err)
	}
	var exported struct {
		TotalRows int `json:"total_rows"`
		Pages     int `json:"pages"`
	}
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("parsing the table's JSON file failed: %v", err)
	}
	t.Logf("page size %d: %d source rows, %d exported rows on %d pages (expected %d pages)",
		pageSize, rows, exported.TotalRows, exported.Pages, wantPages)
	if exported.TotalRows != rows {
		t.Errorf("JSON total_rows=%d, source has %d rows", exported.TotalRows, rows)
	}
	if exported.Pages != wantPages {
		t.Errorf("export used %d pages, want %d", exported.Pages, wantPages)
	}
}

// discoverPaginationTable returns a customizing table with a key besides the
// client and its row count, or skips the test. Row counts are read with
// SELECT COUNT(*) through the data preview.
func discoverPaginationTable(t *testing.T, client adt.Client) (string, int) {
	t.Helper()
	const (
		maxCandidates = 200
		minRows       = 10
		maxRows       = 5000
		searchBudget  = 2 * time.Minute
	)
	ctx, cancel := context.WithTimeout(context.Background(), searchBudget)
	defer cancel()

	// Only DD02L is read here. Joining it with DD03L and DISTINCT is too slow
	// on ERP 6.0 to finish within the budget; the key columns are checked per
	// candidate below instead.
	candidates, err := client.RunQuery(ctx,
		"SELECT TABNAME FROM DD02L WHERE TABCLASS = 'TRANSP' AND CONTFLAG IN ('C','G')"+
			" AND AS4LOCAL = 'A' AND TABNAME LIKE 'T%' ORDER BY TABNAME", maxCandidates)
	if err != nil {
		if ctx.Err() != nil {
			t.Skip("candidate search exceeded its time budget")
		}
		t.Fatalf("candidate query failed")
	}

	checked := 0
	fallbackName, fallbackRows := "", 0
	for _, row := range candidates.Rows {
		if ctx.Err() != nil {
			break
		}
		if len(row) == 0 {
			continue
		}
		name := strings.TrimSpace(row[0])
		if name == "" || strings.HasPrefix(name, "/") {
			continue // skip namespaced tables
		}
		checked++
		count, err := client.RunQuery(ctx, "SELECT COUNT(*) FROM "+name, 1)
		if err != nil || len(count.Rows) != 1 || len(count.Rows[0]) != 1 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(count.Rows[0][0]))
		if err != nil || n < minRows || n > maxRows {
			continue
		}
		// The export can only paginate on a key besides the client.
		keys, err := client.RunQuery(ctx,
			"SELECT COUNT(*) FROM DD03L WHERE TABNAME = '"+name+"' AND AS4LOCAL = 'A'"+
				" AND KEYFLAG = 'X' AND FIELDNAME <> 'MANDT' AND FIELDNAME NOT LIKE '.%'", 1)
		if err != nil || len(keys.Rows) != 1 || len(keys.Rows[0]) != 1 {
			continue
		}
		if k, err := strconv.Atoi(strings.TrimSpace(keys.Rows[0][0])); err != nil || k == 0 {
			continue
		}
		// Prefer a row count with a divisor, so that the exact-multiple run
		// (full pages, then an empty one) can happen; keep the first suitable
		// table as a fallback.
		if smallestFactor(n) == n {
			if fallbackName == "" {
				fallbackName, fallbackRows = name, n
			}
			continue
		}
		t.Logf("%d candidate tables, %d counted, chose a table with %d rows", len(candidates.Rows), checked, n)
		return name, n
	}
	if fallbackName != "" {
		t.Logf("%d candidate tables, %d counted, only a table with a prime row count (%d) fits", len(candidates.Rows), checked, fallbackRows)
		return fallbackName, fallbackRows
	}
	t.Skipf("no customizing table with %d to %d rows found (%d candidates, %d counted)",
		minRows, maxRows, len(candidates.Rows), checked)
	return "", 0
}

func ceilDiv(a, b int) int { return (a + b - 1) / b }

// smallestFactor returns the smallest divisor of n that is at least 2; for a
// prime number that is n itself.
func smallestFactor(n int) int {
	for d := 2; d*d <= n; d++ {
		if n%d == 0 {
			return d
		}
	}
	return n
}
