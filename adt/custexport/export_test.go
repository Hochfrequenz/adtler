package custexport

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Hochfrequenz/adtler/adt"
)

// mockClient implements adt.Client for testing.
// Only RunQuery is wired; all other methods panic if called.
type mockClient struct {
	runQueryFn func(ctx context.Context, sql string, maxRows int) (*adt.QueryResult, error)
}

func (m *mockClient) RunQuery(ctx context.Context, sql string, maxRows int) (*adt.QueryResult, error) {
	if m.runQueryFn != nil {
		return m.runQueryFn(ctx, sql, maxRows)
	}
	return &adt.QueryResult{}, nil
}
func (m *mockClient) GetObjectDependencies(context.Context, string, string, int, int) (*adt.DependencyResult, error) {
	panic("not implemented")
}
func (m *mockClient) RunClass(context.Context, string) (*adt.ClassRunResult, error) {
	panic("not implemented")
}

func (m *mockClient) GetSource(context.Context, string) (*adt.SourceResult, error) {
	panic("not implemented")
}
func (m *mockClient) GetClassDefinition(context.Context, string) (*adt.SourceResult, error) {
	panic("not implemented")
}
func (m *mockClient) SetSource(context.Context, string, string, string, string, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) GetIncludeSource(context.Context, string, string) (*adt.SourceResult, error) {
	panic("not implemented")
}
func (m *mockClient) SetIncludeSource(context.Context, string, string, string, string, string, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) CreateTestInclude(context.Context, string, string, string) error {
	panic("not implemented")
}
func (m *mockClient) ActivateObjects(context.Context, []string) (*adt.ActivationResult, error) {
	panic("not implemented")
}
func (m *mockClient) GetInactiveObjects(context.Context) ([]adt.ObjectInfo, error) {
	panic("not implemented")
}
func (m *mockClient) SearchObjects(context.Context, string, string, int) ([]adt.ObjectInfo, error) {
	panic("not implemented")
}
func (m *mockClient) SearchPackages(context.Context, string, int) ([]adt.ObjectInfo, error) {
	panic("not implemented")
}
func (m *mockClient) WhereUsed(context.Context, string) ([]adt.ObjectInfo, error) {
	panic("not implemented")
}
func (m *mockClient) BrowsePackage(context.Context, string) ([]adt.ObjectInfo, error) {
	panic("not implemented")
}
func (m *mockClient) GetObjectInfo(context.Context, string) (*adt.ObjectInfo, error) {
	panic("not implemented")
}
func (m *mockClient) SyntaxCheck(context.Context, string) ([]adt.SyntaxMessage, error) {
	panic("not implemented")
}
func (m *mockClient) VerifySource(context.Context, string) (bool, []adt.SyntaxMessage, error) {
	panic("not implemented")
}
func (m *mockClient) BatchSyntaxCheck(context.Context, []string) []adt.ObjectSyntaxResult {
	panic("not implemented")
}
func (m *mockClient) RunUnitTests(context.Context, string, int) (*adt.TestResult, error) {
	panic("not implemented")
}
func (m *mockClient) GetTransportRequests(context.Context, string, string) ([]adt.TransportRequest, error) {
	panic("not implemented")
}
func (m *mockClient) AddToTransport(context.Context, string, string) error {
	panic("not implemented")
}
func (m *mockClient) RemoveFromTransport(context.Context, string, string, string, string, string, string, string) error {
	panic("not implemented")
}
func (m *mockClient) GetTransportInfo(context.Context, string) (*adt.TransportRequest, error) {
	panic("not implemented")
}
func (m *mockClient) GetTransportObjects(context.Context, string) ([]adt.TransportObject, error) {
	panic("not implemented")
}
func (m *mockClient) LockObject(context.Context, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) UnlockObject(context.Context, string, string) error {
	panic("not implemented")
}
func (m *mockClient) PrettyPrint(context.Context, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) CreateObject(context.Context, string, string, string, string, string) error {
	panic("not implemented")
}
func (m *mockClient) CreateFunctionModule(context.Context, string, string, string, string, string) error {
	panic("not implemented")
}
func (m *mockClient) CreatePackage(context.Context, string, string, string, string, string, string) error {
	panic("not implemented")
}
func (m *mockClient) DeleteObject(context.Context, string, string, string) error {
	panic("not implemented")
}
func (m *mockClient) GetCompletions(context.Context, string, string, int, int) ([]adt.CompletionItem, error) {
	panic("not implemented")
}
func (m *mockClient) ExportPackage(context.Context, string) ([]byte, error) {
	panic("not implemented")
}
func (m *mockClient) ListAbapGitRepos(context.Context) (*adt.AbapGitRepoList, error) {
	panic("not implemented")
}
func (m *mockClient) PullAbapGitRepo(context.Context, adt.AbapGitPullRequest) (*adt.AbapGitPullResult, error) {
	panic("not implemented")
}
func (m *mockClient) PushAbapGitRepo(context.Context, adt.AbapGitPushRequest) (*adt.AbapGitPushResult, error) {
	panic("not implemented")
}
func (m *mockClient) GetATCCustomizing(context.Context) (*adt.ATCCustomizingResult, error) {
	panic("not implemented")
}
func (m *mockClient) RunATCCheck(context.Context, []string, string) (*adt.ATCResult, error) {
	panic("not implemented")
}
func (m *mockClient) CheckTransport(context.Context, string, string, string) (*adt.TransportCheckResult, error) {
	panic("not implemented")
}
func (m *mockClient) CreateTransport(context.Context, string, string, string, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) CreateTransportTask(context.Context, string, string, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) DeleteTransport(context.Context, string) error {
	panic("not implemented")
}
func (m *mockClient) ReleaseTransport(context.Context, string) (*adt.ReleaseResult, error) {
	panic("not implemented")
}
func (m *mockClient) RollbackTransport(context.Context, string) (*adt.RollbackResult, error) {
	panic("not implemented")
}
func (m *mockClient) ReleaseTransportWithTasks(context.Context, string) (*adt.ReleaseResult, error) {
	panic("not implemented")
}
func (m *mockClient) GetTransportTasks(context.Context, string) ([]string, error) {
	panic("not implemented")
}
func (m *mockClient) GetABAPDoc(context.Context, string) (string, error) { panic("not implemented") }
func (m *mockClient) GetTextElements(context.Context, string) (*adt.TextElements, error) {
	panic("not implemented")
}
func (m *mockClient) GetMessageClass(context.Context, string) (*adt.MessageClassInfo, error) {
	panic("not implemented")
}
func (m *mockClient) SearchMessages(context.Context, string, int) ([]adt.MessageSearchResult, error) {
	panic("not implemented")
}
func (m *mockClient) SetMessages(context.Context, string, string, []adt.Message) error {
	panic("not implemented")
}
func (m *mockClient) SetTextElements(context.Context, string, []adt.TextSymbol, []adt.SelectionText, string, string) error {
	panic("not implemented")
}
func (m *mockClient) NavigateToDefinition(context.Context, string, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) Rename(context.Context, string, string, string) (*adt.RenameResult, error) {
	panic("not implemented")
}
func (m *mockClient) GetVersionHistory(context.Context, string) ([]adt.VersionInfo, error) {
	panic("not implemented")
}
func (m *mockClient) GetVersionSource(context.Context, string) (string, error) {
	panic("not implemented")
}
func (m *mockClient) DiffActiveInactive(context.Context, string) (*adt.DiffResult, error) {
	panic("not implemented")
}
func (m *mockClient) GetTableFields(context.Context, string) ([]adt.FieldInfo, error) {
	panic("not implemented")
}
func (m *mockClient) GetEnhancementSpot(context.Context, string) (*adt.EnhancementSpotInfo, error) {
	panic("not implemented")
}
func (m *mockClient) GetEnhancementImplementation(context.Context, string) (*adt.BAdIImplementationInfo, error) {
	panic("not implemented")
}
func (m *mockClient) SetEnhancementImplementation(context.Context, string, string, string, string, string) error {
	panic("not implemented")
}
func (m *mockClient) ListShortDumps(context.Context, string, string, string) ([]adt.ShortDumpHeader, error) {
	panic("not implemented")
}
func (m *mockClient) GetShortDumps(context.Context, string, string, string) ([]adt.ShortDump, error) {
	panic("not implemented")
}
func (m *mockClient) SystemInfo() (string, string) {
	return "https://mock.example.com:443", "100"
}
func (m *mockClient) Logout(context.Context) error { panic("not implemented") }
func (m *mockClient) SystemFlavor(context.Context) (adt.SystemFlavor, error) {
	panic("not implemented")
}

func TestDiscoverTables(t *testing.T) {
	var capturedSQL string
	var capturedMaxRows int
	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, maxRows int) (*adt.QueryResult, error) {
			capturedSQL = sql
			capturedMaxRows = maxRows
			return &adt.QueryResult{
				Columns: []adt.QueryColumn{
					{Name: "TABNAME", Type: "C"},
					{Name: "CONTFLAG", Type: "C"},
				},
				Rows: [][]string{
					{"T001", "C"},
					{"T002", "C"},
					{"ZTABLE", "G"},
				},
			}, nil
		},
	}

	tables, err := discoverTables(context.Background(), client)
	if err != nil {
		t.Fatalf("discoverTables: %v", err)
	}

	// Verify SQL.
	if !strings.Contains(capturedSQL, "DD02L") {
		t.Errorf("expected SQL to query DD02L, got: %s", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "CONTFLAG IN ('C','G')") {
		t.Errorf("expected SQL to filter CONTFLAG, got: %s", capturedSQL)
	}
	if !strings.Contains(capturedSQL, "AS4LOCAL = 'A'") {
		t.Errorf("expected SQL to filter AS4LOCAL, got: %s", capturedSQL)
	}
	if capturedMaxRows != 200000 {
		t.Errorf("expected maxRows=200000, got %d", capturedMaxRows)
	}

	// Verify results.
	if len(tables) != 3 {
		t.Fatalf("expected 3 tables, got %d", len(tables))
	}
	expected := []string{"T001", "T002", "ZTABLE"}
	for i, want := range expected {
		if tables[i] != want {
			t.Errorf("tables[%d]: expected %q, got %q", i, want, tables[i])
		}
	}
}

func TestFetchAllKeys(t *testing.T) {
	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, _ int) (*adt.QueryResult, error) {
			if !strings.Contains(sql, "DD03L") {
				t.Errorf("expected SQL to query DD03L, got: %s", sql)
			}
			if !strings.Contains(sql, "'T001'") {
				t.Errorf("expected SQL to filter for T001, got: %s", sql)
			}
			return &adt.QueryResult{
				Columns: []adt.QueryColumn{
					{Name: "FIELDNAME", Type: "C"},
					{Name: "POSITION", Type: "N"},
				},
				Rows: [][]string{
					{"MANDT", "0001"},
					{"BUKRS", "0002"},
				},
			}, nil
		},
	}

	keys, _, err := fetchTableKeys(context.Background(), client, "T001")
	if err != nil {
		t.Fatalf("fetchTableKeys: %v", err)
	}

	if len(keys) != 2 {
		t.Fatalf("expected 2 keys for T001, got %d", len(keys))
	}
	if keys[0] != "MANDT" || keys[1] != "BUKRS" {
		t.Errorf("T001 keys: expected [MANDT BUKRS], got %v", keys)
	}
}

func TestFetchTableKeys_SkipsPseudoFields(t *testing.T) {
	client := &mockClient{
		runQueryFn: func(_ context.Context, _ string, _ int) (*adt.QueryResult, error) {
			return &adt.QueryResult{
				Columns: []adt.QueryColumn{
					{Name: "FIELDNAME", Type: "C"},
					{Name: "POSITION", Type: "N"},
				},
				Rows: [][]string{
					{"MANDT", "0001"},
					{".INCLUDE", "0002"},
					{"GRDB_ITEM_SCEN", "0003"},
					{".APPEND", "0004"},
				},
			}, nil
		},
	}

	keys, _, err := fetchTableKeys(context.Background(), client, "SOMETABLE")
	if err != nil {
		t.Fatalf("fetchTableKeys: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys (MANDT, GRDB_ITEM_SCEN), got %d: %v", len(keys), keys)
	}
	if keys[0] != "MANDT" || keys[1] != "GRDB_ITEM_SCEN" {
		t.Errorf("expected [MANDT GRDB_ITEM_SCEN], got %v", keys)
	}
}

func TestExportTable_SinglePage(t *testing.T) {
	client := &mockClient{
		runQueryFn: func(_ context.Context, _ string, _ int) (*adt.QueryResult, error) {
			return &adt.QueryResult{
				Columns: []adt.QueryColumn{
					{Name: "MANDT", Type: "C", IsKey: true},
					{Name: "BUKRS", Type: "C", IsKey: true},
					{Name: "BUTXT", Type: "C", IsKey: false},
				},
				Rows: [][]string{
					{"100", "1000", "Company A"},
					{"100", "2000", "Company B"},
				},
			}, nil
		},
	}

	result, err := exportTable(context.Background(), client, "T001", []string{"MANDT", "BUKRS"}, nil, 100)
	if err != nil {
		t.Fatalf("exportTable: %v", err)
	}

	if result.TableName != "T001" {
		t.Errorf("expected table T001, got %s", result.TableName)
	}
	if result.Pages != 1 {
		t.Errorf("expected 1 page, got %d", result.Pages)
	}
	if result.TotalRows != 2 {
		t.Errorf("expected 2 rows, got %d", result.TotalRows)
	}
	if len(result.Columns) != 3 {
		t.Errorf("expected 3 columns, got %d", len(result.Columns))
	}
}

func TestExportTable_ThreePages(t *testing.T) {
	pageSize := 2
	callCount := 0

	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, _ int) (*adt.QueryResult, error) {
			callCount++
			cols := []adt.QueryColumn{
				{Name: "MANDT", Type: "C", IsKey: true},
				{Name: "BUKRS", Type: "C", IsKey: true},
				{Name: "BUTXT", Type: "C", IsKey: false},
			}

			switch callCount {
			case 1:
				// First page: full (2 rows = pageSize).
				if strings.Contains(sql, "WHERE") {
					t.Error("first page should not have WHERE clause")
				}
				return &adt.QueryResult{
					Columns: cols,
					Rows: [][]string{
						{"100", "1000", "Company A"},
						{"100", "2000", "Company B"},
					},
				}, nil
			case 2:
				// Second page: full (2 rows = pageSize).
				if !strings.Contains(sql, "BUKRS > '2000'") {
					t.Errorf("second page WHERE should reference BUKRS > '2000', got: %s", sql)
				}
				return &adt.QueryResult{
					Columns: cols,
					Rows: [][]string{
						{"100", "3000", "Company C"},
						{"100", "4000", "Company D"},
					},
				}, nil
			case 3:
				// Third page: partial (1 row < pageSize).
				if !strings.Contains(sql, "BUKRS > '4000'") {
					t.Errorf("third page WHERE should reference BUKRS > '4000', got: %s", sql)
				}
				return &adt.QueryResult{
					Columns: cols,
					Rows: [][]string{
						{"100", "5000", "Company E"},
					},
				}, nil
			default:
				t.Fatal("unexpected fourth call to RunQuery")
				return nil, nil
			}
		},
	}

	result, err := exportTable(context.Background(), client, "T001", []string{"MANDT", "BUKRS"}, nil, pageSize)
	if err != nil {
		t.Fatalf("exportTable: %v", err)
	}

	if callCount != 3 {
		t.Errorf("expected 3 RunQuery calls, got %d", callCount)
	}
	if result.Pages != 3 {
		t.Errorf("expected 3 pages, got %d", result.Pages)
	}
	if result.TotalRows != 5 {
		t.Errorf("expected 5 total rows, got %d", result.TotalRows)
	}
	if len(result.Rows) != 5 {
		t.Errorf("expected 5 rows, got %d", len(result.Rows))
	}
	// Verify last row.
	if result.Rows[4][1] != "5000" {
		t.Errorf("expected last row BUKRS=5000, got %s", result.Rows[4][1])
	}
}

func TestExportTable_NoKeys(t *testing.T) {
	callCount := 0
	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, _ int) (*adt.QueryResult, error) {
			callCount++
			if strings.Contains(sql, "ORDER BY") {
				t.Error("no-key tables should not have ORDER BY")
			}
			return &adt.QueryResult{
				Columns: []adt.QueryColumn{
					{Name: "FIELD1", Type: "C"},
				},
				Rows: [][]string{{"value1"}, {"value2"}},
			}, nil
		},
	}

	result, err := exportTable(context.Background(), client, "T000", nil, nil, 100)
	if err != nil {
		t.Fatalf("exportTable: %v", err)
	}

	// Should only make one call (no pagination without keys).
	if callCount != 1 {
		t.Errorf("expected 1 call for no-key table, got %d", callCount)
	}
	if result.TotalRows != 2 {
		t.Errorf("expected 2 rows, got %d", result.TotalRows)
	}
}

func TestExportTable_ErrorOnQuery(t *testing.T) {
	client := &mockClient{
		runQueryFn: func(_ context.Context, _ string, _ int) (*adt.QueryResult, error) {
			return nil, fmt.Errorf("connection timeout")
		},
	}

	_, err := exportTable(context.Background(), client, "T001", []string{"MANDT", "BUKRS"}, nil, 100)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "connection timeout") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestExtractKeyValues(t *testing.T) {
	columns := []adt.QueryColumn{
		{Name: "MANDT", Type: "C"},
		{Name: "BUKRS", Type: "C"},
		{Name: "GJAHR", Type: "N"},
		{Name: "BUTXT", Type: "C"},
	}
	row := []string{"100", "1000", "2025", "Test"}

	values := extractKeyValues(columns, []string{"BUKRS", "GJAHR"}, row)
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if values[0] != "1000" || values[1] != "2025" {
		t.Errorf("expected [1000 2025], got %v", values)
	}

	// Missing key returns nil.
	values = extractKeyValues(columns, []string{"BUKRS", "MISSING"}, row)
	if values != nil {
		t.Errorf("expected nil for missing key, got %v", values)
	}
}

func TestRunExport_EndToEnd(t *testing.T) {
	callCount := 0
	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, _ int) (*adt.QueryResult, error) {
			callCount++
			if strings.Contains(sql, "DD02L") {
				// discoverTables — not called since we provide tables.
				t.Error("should not query DD02L when tables provided")
			}
			if strings.Contains(sql, "DD03L") {
				// fetchAllKeys.
				return &adt.QueryResult{
					Columns: []adt.QueryColumn{
						{Name: "TABNAME", Type: "C"},
						{Name: "FIELDNAME", Type: "C"},
						{Name: "POSITION", Type: "N"},
					},
					Rows: [][]string{
						{"T001", "MANDT", "0001"},
						{"T001", "BUKRS", "0002"},
					},
				}, nil
			}
			// Table export query.
			return &adt.QueryResult{
				Columns: []adt.QueryColumn{
					{Name: "MANDT", Type: "C", IsKey: true},
					{Name: "BUKRS", Type: "C", IsKey: true},
					{Name: "BUTXT", Type: "C", IsKey: false},
				},
				Rows: [][]string{
					{"100", "1000", "Test Company"},
				},
			}, nil
		},
	}

	dir := t.TempDir()
	cfg := ExportConfig{
		OutputDir: dir,
		Tables:    []string{"T001"},
		PageSize:  100,
		Workers:   1,
		System:    "https://mock.example.com:443",
		Client:    "100",
	}

	summary, err := RunExport(context.Background(), client, cfg)
	if err != nil {
		t.Fatalf("RunExport: %v", err)
	}

	if summary.TotalTables != 1 {
		t.Errorf("expected 1 total table, got %d", summary.TotalTables)
	}
	if summary.ExportedTables != 1 {
		t.Errorf("expected 1 exported table, got %d", summary.ExportedTables)
	}
	if summary.TotalRows != 1 {
		t.Errorf("expected 1 total row, got %d", summary.TotalRows)
	}
	if len(summary.Errors) != 0 {
		t.Errorf("expected 0 errors, got %d", len(summary.Errors))
	}
	if summary.Workers != 1 {
		t.Errorf("expected workers=1, got %d", summary.Workers)
	}
	if summary.System != "https://mock.example.com:443" {
		t.Errorf("expected system %q, got %q", "https://mock.example.com:443", summary.System)
	}
	if summary.Client != "100" {
		t.Errorf("expected client %q, got %q", "100", summary.Client)
	}
}

// pageWhereRe matches one comparison of the keyset-pagination WHERE clause.
var pageWhereRe = regexp.MustCompile(`(?:CAST\( (\w+) AS CHAR\( 6 \) \)|(\w+)) (=|>) '([^']*)'`)

// errTimeLiteral mimics the data preview's refusal of '240000' as a literal
// compared with a TIMS field.
var errTimeLiteral = errors.New("'240000' is not a valid value for T(6,0)")

// matchesPageWhere evaluates the OR-chain WHERE clause built by
// adt.BuildExportSQL against a row, the way the database would. Plain
// comparisons of a column in timeColumns with '240000' are refused like the
// data preview does; CAST( col AS CHAR( 6 ) ) comparisons are text compares.
func matchesPageWhere(sql string, columns []string, row []string, timeColumns ...string) (bool, error) {
	_, where, found := strings.Cut(sql, " WHERE ")
	if !found {
		return true, nil
	}
	where, _, _ = strings.Cut(where, " ORDER BY ")
	value := func(name string) string {
		for i, c := range columns {
			if c == name {
				return row[i]
			}
		}
		return ""
	}
	for _, term := range strings.Split(where, " OR ") {
		all := true
		for _, m := range pageWhereRe.FindAllStringSubmatch(term, -1) {
			name, cast, op, lit := m[1], true, m[3], m[4]
			if name == "" {
				name, cast = m[2], false
			}
			if !cast && lit == "240000" && slices.Contains(timeColumns, name) {
				return false, errTimeLiteral
			}
			v := value(name)
			if (op == "=" && v != lit) || (op == ">" && v <= lit) {
				all = false
			}
		}
		if all {
			return true, nil
		}
	}
	return false, nil
}

// TestExportTable_LongKeyPaginationKeepsAllRows exports a table whose
// pagination SQL is far longer than 250 characters, with page boundaries
// inside groups of rows that share their first key. Every row must arrive.
func TestExportTable_LongKeyPaginationKeepsAllRows(t *testing.T) {
	const pageSize = 4
	keys := []string{
		"MANDT",
		"FIRST_KEY_FIELD_WITH_A_LONG_NAME",
		"SECOND_KEY_FIELD_WITH_A_LONG_NAME",
		"THIRD_KEY_FIELD_WITH_A_LONG_NAME",
		"FOURTH_KEY_FIELD_WITH_A_LONG_NAME",
	}
	columns := append(append([]string{}, keys...), "PAYLOAD")

	// 3 first-key groups of 6 rows each; with a page size of 4 every page
	// boundary falls inside a group.
	var all [][]string
	for _, first := range []string{"FIRST_VALUE_A", "FIRST_VALUE_B", "FIRST_VALUE_C"} {
		for i := 1; i <= 6; i++ {
			second := fmt.Sprintf("SECOND_VALUE_%02d", i)
			all = append(all, []string{"100", first, second, "THIRD_VALUE_CONSTANT", "FOURTH_VALUE_CONSTANT", first + "/" + second})
		}
	}

	fullSQL, err := adt.BuildExportSQL("SOMETABLE", keys, adt.FilterNonMandtKeys(keys), all[0][1:5])
	if err != nil {
		t.Fatalf("BuildExportSQL: %v", err)
	}
	if len(fullSQL) <= 250 {
		t.Fatalf("test setup: full-key pagination SQL is %d characters, want more than 250", len(fullSQL))
	}

	resultColumns := make([]adt.QueryColumn, len(columns))
	for i, c := range columns {
		resultColumns[i] = adt.QueryColumn{Name: c, Type: "C"}
	}
	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, maxRows int) (*adt.QueryResult, error) {
			var page [][]string
			for _, row := range all { // all is already in key order
				ok, err := matchesPageWhere(sql, columns, row)
				if err != nil {
					return nil, err
				}
				if len(page) < maxRows && ok {
					page = append(page, row)
				}
			}
			return &adt.QueryResult{Columns: resultColumns, Rows: page}, nil
		},
	}

	result, err := exportTable(context.Background(), client, "SOMETABLE", keys, nil, pageSize)
	if err != nil {
		t.Fatalf("exportTable: %v", err)
	}
	if len(result.Rows) != len(all) {
		t.Fatalf("exported %d rows, want %d", len(result.Rows), len(all))
	}
	for i, row := range all {
		if got := strings.Join(result.Rows[i], "|"); got != strings.Join(row, "|") {
			t.Errorf("row %d: got %q, want %q", i, got, strings.Join(row, "|"))
		}
	}
}

// TestExportTable_TimsKeyAtPageBoundary exports a table whose page boundaries
// fall on a TIMS key holding 240000. The data preview refuses that value as a
// literal, so the export has to compare TIMS keys as text.
func TestExportTable_TimsKeyAtPageBoundary(t *testing.T) {
	const pageSize = 3
	keys := []string{"MANDT", "DAY_KEY", "END_TIME"}
	columns := []string{"MANDT", "DAY_KEY", "END_TIME", "PAYLOAD"}

	// Three rows per day, the last one at 24:00:00, so every page ends on it.
	var all [][]string
	for _, day := range []string{"20250101", "20250102", "20250103"} {
		for _, tm := range []string{"080000", "160000", "240000"} {
			all = append(all, []string{"100", day, tm, day + "/" + tm})
		}
	}
	resultColumns := make([]adt.QueryColumn, len(columns))
	for i, c := range columns {
		resultColumns[i] = adt.QueryColumn{Name: c, Type: "C"}
	}
	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, maxRows int) (*adt.QueryResult, error) {
			var page [][]string
			for _, row := range all {
				ok, err := matchesPageWhere(sql, columns, row, "END_TIME")
				if err != nil {
					return nil, err
				}
				if len(page) < maxRows && ok {
					page = append(page, row)
				}
			}
			return &adt.QueryResult{Columns: resultColumns, Rows: page}, nil
		},
	}

	result, err := exportTable(context.Background(), client, "SOMETABLE", keys,
		map[string]string{"MANDT": "CLNT", "DAY_KEY": "DATS", "END_TIME": "TIMS"}, pageSize)
	if err != nil {
		t.Fatalf("exportTable: %v", err)
	}
	if len(result.Rows) != len(all) {
		t.Fatalf("exported %d rows, want %d", len(result.Rows), len(all))
	}
	for i, row := range all {
		if got := strings.Join(result.Rows[i], "|"); got != strings.Join(row, "|") {
			t.Errorf("row %d: got %q, want %q", i, got, strings.Join(row, "|"))
		}
	}
}

func TestFetchTableKeys_ReturnsDataTypes(t *testing.T) {
	client := &mockClient{
		runQueryFn: func(_ context.Context, sql string, _ int) (*adt.QueryResult, error) {
			if !strings.Contains(sql, "DATATYPE") {
				t.Errorf("key query must select DATATYPE, got: %s", sql)
			}
			return &adt.QueryResult{Rows: [][]string{
				{"MANDT", "0001", "CLNT"},
				{".INCLUDE", "0002", ""},
				{"END_TIME", "0003", "TIMS"},
			}}, nil
		},
	}
	keys, types, err := fetchTableKeys(context.Background(), client, "SOMETABLE")
	if err != nil {
		t.Fatalf("fetchTableKeys: %v", err)
	}
	if len(keys) != 2 || types["END_TIME"] != "TIMS" || types["MANDT"] != "CLNT" {
		t.Errorf("got keys %v, types %v", keys, types)
	}
}
