//go:build integration

package custexport_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hochfrequenz/adtler/adt"
	"github.com/Hochfrequenz/adtler/adt/custexport"
	sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"

	_ "modernc.org/sqlite"
)

// newClient creates a real ADT client from environment variables.
// Inline version of adt_test.newIntegrationClient since we cannot
// import test helpers across packages.
func newClient(t *testing.T) adt.Client {
	t.Helper()
	host := strings.TrimSpace(os.Getenv("SAP_INTEGRATION_HOST"))
	if host == "" {
		t.Skip("SAP_INTEGRATION_HOST not set, skipping integration test")
	}
	user := strings.TrimSpace(os.Getenv("SAP_INTEGRATION_USER"))
	if user == "" {
		t.Fatal("SAP_INTEGRATION_USER must be set when SAP_INTEGRATION_HOST is set")
	}
	password := os.Getenv("SAP_INTEGRATION_PASSWORD")
	if password == "" {
		t.Fatal("SAP_INTEGRATION_PASSWORD must be set when SAP_INTEGRATION_HOST is set")
	}
	return adt.NewClient(sapmcpconfig.SAPSystem{
		Host:          host,
		User:          user,
		Password:      password,
		Client:        os.Getenv("SAP_INTEGRATION_CLIENT"),
		TLSSkipVerify: true,
	})
}

func TestExportCustomizing_SmallTableSet(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	outputDir := t.TempDir()

	tables := []string{"T001", "T005", "T006", "TVARVC", "T000"}

	host, sapClient := client.SystemInfo()
	summary, err := custexport.RunExport(ctx, client, custexport.ExportConfig{
		OutputDir: outputDir,
		Tables:    tables,
		Workers:   1,
		System:    host,
		Client:    sapClient,
	})
	if err != nil {
		t.Fatalf("RunExport failed: %v", err)
	}

	// Verify no errors in summary.
	if len(summary.Errors) > 0 {
		for _, e := range summary.Errors {
			t.Errorf("export error for %s: %s", e.Table, e.Error)
		}
		t.FailNow()
	}

	// Verify customizing_{client}.db exists.
	dbName := "customizing.db"
	if sapClient != "" {
		dbName = fmt.Sprintf("customizing_%s.db", sapClient)
	}
	dbPath := filepath.Join(outputDir, dbName)
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("%s not found: %v", dbName, err)
	}

	// Verify json/ directory has one JSON file per table.
	jsonDir := filepath.Join(outputDir, "json")
	entries, err := os.ReadDir(jsonDir)
	if err != nil {
		t.Fatalf("reading json dir: %v", err)
	}
	jsonFiles := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			jsonFiles++
		}
	}
	if jsonFiles != len(tables) {
		t.Errorf("expected %d JSON files, got %d", len(tables), jsonFiles)
	}

	// Open SQLite DB and verify _metadata has entries for all tables.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	for _, table := range tables {
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM "_metadata" WHERE "table_name" = ?`, table).Scan(&count)
		if err != nil {
			t.Errorf("querying _metadata for %s: %v", table, err)
			continue
		}
		if count == 0 {
			t.Errorf("no _metadata entries for table %s", table)
		}
	}

	// Verify T001 table exists in SQLite and has rows.
	var t001Count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM "T001"`).Scan(&t001Count); err != nil {
		t.Fatalf("querying T001 row count: %v", err)
	}
	if t001Count == 0 {
		t.Error("T001 has 0 rows in SQLite, expected at least 1")
	}
	t.Logf("T001 has %d rows", t001Count)

	// Verify export_summary.json exists and has correct counts.
	summaryPath := filepath.Join(outputDir, "export_summary.json")
	summaryData, err := os.ReadFile(summaryPath)
	if err != nil {
		t.Fatalf("reading export_summary.json: %v", err)
	}

	var fileSummary custexport.ExportSummary
	if err := json.Unmarshal(summaryData, &fileSummary); err != nil {
		t.Fatalf("parsing export_summary.json: %v", err)
	}
	if fileSummary.TotalTables != len(tables) {
		t.Errorf("summary total_tables: got %d, want %d", fileSummary.TotalTables, len(tables))
	}
	if fileSummary.ExportedTables+fileSummary.EmptyTables != len(tables) {
		t.Errorf("exported(%d) + empty(%d) != total(%d)",
			fileSummary.ExportedTables, fileSummary.EmptyTables, len(tables))
	}
	// Verify system and client are populated (#90).
	if fileSummary.System == "" {
		t.Error("export_summary.json has empty 'system' field")
	}
	if fileSummary.Client == "" {
		t.Error("export_summary.json has empty 'client' field")
	}

	t.Logf("export summary: system=%s client=%s, %d tables, %d exported, %d empty, %d total rows",
		fileSummary.System, fileSummary.Client,
		fileSummary.TotalTables, fileSummary.ExportedTables, fileSummary.EmptyTables, fileSummary.TotalRows)
}

func TestExportCustomizing_EmptyTable(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	outputDir := t.TempDir()

	// TPARA is a small customizing table that may be empty on test systems.
	// We export it alongside T000 (which is never empty) to verify the
	// writer handles empty tables correctly regardless.
	tables := []string{"T000", "TPARA"}

	summary, err := custexport.RunExport(ctx, client, custexport.ExportConfig{
		OutputDir: outputDir,
		Tables:    tables,
		Workers:   1,
	})
	if err != nil {
		t.Fatalf("RunExport failed: %v", err)
	}

	dbPath := filepath.Join(outputDir, "customizing.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	// Both tables should exist in SQLite (schema created even if empty).
	for _, table := range tables {
		var count int
		err := db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&count)
		if err != nil {
			t.Errorf("checking existence of table %s: %v", table, err)
			continue
		}
		if count == 0 {
			t.Errorf("table %s was not created in SQLite", table)
		}
	}

	// Both tables should have JSON files.
	for _, table := range tables {
		jsonPath := filepath.Join(outputDir, "json", table+".json")
		data, err := os.ReadFile(jsonPath)
		if err != nil {
			t.Errorf("reading %s.json: %v", table, err)
			continue
		}

		var jt struct {
			Rows []json.RawMessage `json:"rows"`
		}
		if err := json.Unmarshal(data, &jt); err != nil {
			t.Errorf("parsing %s.json: %v", table, err)
			continue
		}
		t.Logf("%s.json has %d rows", table, len(jt.Rows))
	}

	// T000 should always have rows.
	var t000Count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM "T000"`).Scan(&t000Count); err != nil {
		t.Fatalf("querying T000 row count: %v", err)
	}
	if t000Count == 0 {
		t.Error("T000 has 0 rows, expected at least 1")
	}

	// Log which tables were empty for diagnostic purposes.
	t.Logf("summary: exported=%d, empty=%d, errors=%d",
		summary.ExportedTables, summary.EmptyTables, len(summary.Errors))
}

func TestExportCustomizing_Pagination(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	outputDir := t.TempDir()

	// T006 (units of measurement) typically has ~410 rows.
	// Using page_size=100 forces multiple pages.
	tables := []string{"T006"}
	pageSize := 100

	summary, err := custexport.RunExport(ctx, client, custexport.ExportConfig{
		OutputDir: outputDir,
		Tables:    tables,
		PageSize:  pageSize,
		Workers:   1,
	})
	if err != nil {
		t.Fatalf("RunExport failed: %v", err)
	}
	if len(summary.Errors) > 0 {
		t.Fatalf("export had errors: %v", summary.Errors)
	}

	// Verify all rows landed in SQLite (not just the first page).
	dbPath := filepath.Join(outputDir, "customizing.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	var rowCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM "T006"`).Scan(&rowCount); err != nil {
		t.Fatalf("querying T006 row count: %v", err)
	}
	t.Logf("T006: %d rows in SQLite", rowCount)

	if rowCount <= pageSize {
		t.Errorf("T006 has %d rows, expected more than %d (page_size) to verify pagination", rowCount, pageSize)
	}

	// Verify the JSON file shows pages > 1.
	jsonPath := filepath.Join(outputDir, "json", "T006.json")
	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("reading T006.json: %v", err)
	}

	var jt struct {
		TotalRows int `json:"total_rows"`
		Pages     int `json:"pages"`
	}
	if err := json.Unmarshal(data, &jt); err != nil {
		t.Fatalf("parsing T006.json: %v", err)
	}

	if jt.Pages <= 1 {
		t.Errorf("T006 JSON pages=%d, expected >1 with page_size=%d", jt.Pages, pageSize)
	}
	if jt.TotalRows != rowCount {
		t.Errorf("JSON total_rows=%d != SQLite count=%d", jt.TotalRows, rowCount)
	}

	t.Logf("T006: %d rows across %d pages (page_size=%d)", jt.TotalRows, jt.Pages, pageSize)
}

// exportSQLLimit mirrors maxSQLLength in export.go: the length above which the
// export shortens its pagination keys.
const exportSQLLimit = 250

// discoverTables runs a discovery query and returns the first column of up to
// max rows. It fails the test on a query error.
func discoverTables(t *testing.T, client adt.Client, sql string, max int) []string {
	t.Helper()
	result, err := client.RunQuery(context.Background(), sql, max)
	if err != nil {
		t.Fatalf("discovery query failed: %v", err)
	}
	var tables []string
	for _, row := range result.Rows {
		if len(row) > 0 && strings.TrimSpace(row[0]) != "" {
			tables = append(tables, strings.TrimSpace(row[0]))
		}
	}
	return tables
}

// sourceRowCount returns SELECT COUNT(*) for a table.
func sourceRowCount(t *testing.T, client adt.Client, table string) int {
	t.Helper()
	result, err := client.RunQuery(context.Background(), "SELECT COUNT(*) FROM "+table, 1)
	if err != nil {
		t.Fatalf("counting source rows failed: %v", err)
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
		t.Fatalf("unexpected COUNT(*) result shape: %d rows", len(result.Rows))
	}
	n, err := strconv.Atoi(strings.TrimSpace(result.Rows[0][0]))
	if err != nil {
		t.Fatalf("parsing COUNT(*) result: %v", err)
	}
	return n
}

// customizingTablesSQL builds a query over the active transparent customizing
// tables and their active key fields, narrowed by extraCondition.
func customizingTablesSQL(selectList, extraCondition, tail string) string {
	return "SELECT " + selectList + " FROM DD02L AS a INNER JOIN DD03L AS b ON a~TABNAME = b~TABNAME" +
		" WHERE a~TABCLASS = 'TRANSP' AND a~CONTFLAG IN ('C','G') AND a~AS4LOCAL = 'A'" +
		" AND b~KEYFLAG = 'X' AND b~AS4LOCAL = 'A' AND " + extraCondition + " " + tail
}

func TestExportCustomizing_IncludeTables(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	outputDir := t.TempDir()

	// Tables with a key-level .INCLUDE pseudo-field in DD03L previously caused
	// "invalid key column" errors in the export. Discover two that hold data.
	const maxCandidates = 300
	candidates := discoverTables(t, client, customizingTablesSQL(
		"DISTINCT a~TABNAME", "b~FIELDNAME = '.INCLUDE'", "ORDER BY a~TABNAME"), maxCandidates)

	var tables []string
	for _, c := range candidates {
		if n := sourceRowCount(t, client, c); n >= 1 && n < 50000 {
			tables = append(tables, c)
		}
		if len(tables) == 2 {
			break
		}
	}
	t.Logf("%d candidate tables with a key-level .INCLUDE, %d usable", len(candidates), len(tables))
	if len(tables) == 0 {
		t.Skip("no customizing table with a key-level .INCLUDE found")
	}

	summary, err := custexport.RunExport(ctx, client, custexport.ExportConfig{
		OutputDir: outputDir,
		Tables:    tables,
		PageSize:  100000,
		Workers:   1,
	})
	if err != nil {
		t.Fatalf("RunExport failed: %v", err)
	}

	if len(summary.Errors) > 0 {
		t.Errorf("%d of %d tables failed to export", len(summary.Errors), len(tables))
	}
	if summary.ExportedTables != len(tables) {
		t.Errorf("exported %d tables, want %d", summary.ExportedTables, len(tables))
	}

	dbPath := filepath.Join(outputDir, "customizing.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	for i, table := range tables {
		jsonName := strings.ReplaceAll(table, "/", "#") + ".json"
		if _, err := os.Stat(filepath.Join(outputDir, "json", jsonName)); err != nil {
			t.Errorf("table %d: missing JSON file", i+1)
		}
		var ddl string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = ?`, table).Scan(&ddl); err != nil {
			t.Errorf("table %d: not found in SQLite: %v", i+1, err)
			continue
		}
		if !strings.Contains(ddl, "PRIMARY KEY") {
			t.Errorf("table %d: expected PRIMARY KEY in SQLite DDL", i+1)
		}
	}

	t.Logf("summary: %d exported, %d errors", summary.ExportedTables, len(summary.Errors))
}

// tableKeyFields returns the key fields of a table in key order, without
// DDIC pseudo-fields such as .INCLUDE.
func tableKeyFields(t *testing.T, client adt.Client, table string) []string {
	t.Helper()
	sql := fmt.Sprintf("SELECT FIELDNAME FROM DD03L WHERE TABNAME = '%s' AND KEYFLAG = 'X' AND AS4LOCAL = 'A' ORDER BY POSITION",
		adt.EscapeValue(table))
	var keys []string
	for _, k := range discoverTables(t, client, sql, 1000) {
		if !strings.HasPrefix(k, ".") {
			keys = append(keys, k)
		}
	}
	return keys
}

// paginationSQLTooLong reports whether the export's first pagination query
// for the table, built from a real row, exceeds the export's length limit.
func paginationSQLTooLong(t *testing.T, client adt.Client, table string) bool {
	t.Helper()
	keys := tableKeyFields(t, client, table)
	nonMandt := adt.FilterNonMandtKeys(keys)
	result, err := client.RunQuery(context.Background(),
		"SELECT "+strings.Join(keys, ", ")+" FROM "+table, 1)
	if err != nil || len(result.Rows) != 1 {
		return false
	}
	values := make(map[string]string, len(keys))
	for i, k := range keys {
		if i < len(result.Rows[0]) {
			values[k] = result.Rows[0][i]
		}
	}
	last := make([]string, 0, len(nonMandt))
	for _, k := range nonMandt {
		last = append(last, values[k])
	}
	sqlStr, err := adt.BuildExportSQL(table, keys, nonMandt, last)
	return err == nil && len(sqlStr) > exportSQLLimit
}

func TestExportCustomizing_LongKeyPagination(t *testing.T) {
	client := newClient(t)
	ctx := context.Background()
	outputDir := t.TempDir()

	// A table with at least four non-client key fields and more rows than one
	// page whose pagination SQL exceeds the export's length limit, so that the
	// export has to shorten its pagination keys. Aggregating DD03L is too slow
	// on older releases, so candidates are checked one by one.
	const (
		maxCandidates     = 2000
		pageSize          = 1000
		minKeys           = 4
		maxByKeyCount     = 400
		aggregationBudget = 20 * time.Second
	)
	candidates := discoverTables(t, client,
		"SELECT TABNAME FROM DD02L WHERE TABCLASS = 'TRANSP' AND CONTFLAG IN ('C','G') AND AS4LOCAL = 'A' ORDER BY TABNAME",
		maxCandidates)
	// Where the system answers the aggregation quickly, start with the tables
	// that have the most keys: their pagination SQL is the longest.
	aggCtx, cancel := context.WithTimeout(ctx, aggregationBudget)
	byKeyCount, err := client.RunQuery(aggCtx, customizingTablesSQL(
		"b~TABNAME, COUNT(*) AS N",
		"b~FIELDNAME <> 'MANDT' AND b~FIELDNAME NOT LIKE '.%'",
		"GROUP BY b~TABNAME HAVING COUNT(*) >= 4 ORDER BY N DESCENDING, b~TABNAME"), maxByKeyCount)
	cancel()
	if err == nil && len(byKeyCount.Rows) > 0 {
		candidates = candidates[:0]
		for _, row := range byKeyCount.Rows {
			candidates = append(candidates, strings.TrimSpace(row[0]))
		}
	}

	table, sourceRows, manyKeys, sized := "", 0, 0, 0
	for _, c := range candidates {
		if len(adt.FilterNonMandtKeys(tableKeyFields(t, client, c))) < minKeys {
			continue
		}
		manyKeys++
		n := sourceRowCount(t, client, c)
		if n <= pageSize || n >= 200000 {
			continue
		}
		sized++
		if paginationSQLTooLong(t, client, c) {
			table, sourceRows = c, n
			break
		}
	}
	t.Logf("%d customizing tables checked, %d with >= %d non-client keys, %d of suitable size, long pagination SQL found: %t",
		len(candidates), manyKeys, minKeys, sized, table != "")
	if table == "" {
		t.Skip("no customizing table with >= 4 keys whose pagination SQL exceeds the export limit")
	}

	summary, err := custexport.RunExport(ctx, client, custexport.ExportConfig{
		OutputDir: outputDir,
		Tables:    []string{table},
		PageSize:  pageSize,
		Workers:   1,
	})
	if err != nil {
		t.Fatalf("RunExport failed: %v", err)
	}
	if len(summary.Errors) > 0 {
		t.Errorf("%d export errors", len(summary.Errors))
	}
	if summary.ExportedTables != 1 {
		t.Errorf("expected 1 exported table, got %d", summary.ExportedTables)
	}

	db, err := sql.Open("sqlite", filepath.Join(outputDir, "customizing.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()

	var rowCount int
	if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, table)).Scan(&rowCount); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	t.Logf("%d source rows, %d rows in SQLite (page_size=%d)", sourceRows, rowCount, pageSize)
	if rowCount != sourceRows {
		t.Errorf("exported %d rows, source has %d", rowCount, sourceRows)
	}
}
