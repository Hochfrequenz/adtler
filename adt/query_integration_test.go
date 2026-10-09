//go:build integration

package adt_test

import (
	"context"
	"strings"
	"testing"
)

func TestRunQuery_Integration(t *testing.T) {
	client := newIntegrationClient(t)
	ctx := context.Background()

	result, err := client.RunQuery(ctx, "SELECT BUKRS, BUTXT FROM T001 ORDER BY BUKRS", 10)
	if err != nil {
		t.Fatalf("RunQuery failed: %v", err)
	}

	if len(result.Columns) < 2 {
		t.Fatalf("expected at least 2 columns, got %d", len(result.Columns))
	}
	if result.Columns[0].Name != "BUKRS" {
		t.Errorf("first column name: got %q, want BUKRS", result.Columns[0].Name)
	}
	if result.Columns[1].Name != "BUTXT" {
		t.Errorf("second column name: got %q, want BUTXT", result.Columns[1].Name)
	}

	if len(result.Rows) == 0 {
		t.Fatal("expected at least one row")
	}
	if len(result.Rows) > 10 {
		t.Errorf("expected at most 10 rows, got %d", len(result.Rows))
	}
	t.Logf("got %d rows (totalRows=%d, executionMs=%.1f)", len(result.Rows), result.TotalRows, result.ExecutionMs)
	for i, row := range result.Rows {
		if len(row) < 2 {
			t.Errorf("row %d: expected at least 2 columns, got %d", i, len(row))
			continue
		}
		t.Logf("  %s = %s", row[0], row[1])
	}
}

// TestRunQuery_LongLine_Integration is the live regression guard for issue
// #183: the data preview cuts SQL lines after 255 characters. Both statements
// are single lines longer than 255 characters, and both must return T000 and
// T001 (SAP standard tables present on every system).
//
//   - "literal across 255": character 255 falls inside a string literal.
//     Without the fix SAP answers 400 on both systems.
//   - "whitespace across 255": the reproducer from the issue, padded so the
//     OR branch lies entirely after character 255. Without the fix the ECC
//     system returns only T000 and no error.
func TestRunQuery_LongLine_Integration(t *testing.T) {
	ctx := context.Background()

	literalAcross := "SELECT TABNAME FROM DD02L WHERE AS4LOCAL = 'A' AND TABNAME IN ( "
	for len(literalAcross) < 236 {
		literalAcross += "'T000', "
	}
	literalAcross += "'ZZ_NO_SUCH_TABLE_0183', 'T001' )"
	if strings.Count(literalAcross[:255], "'")%2 != 1 {
		t.Fatalf("fixture drifted: character 255 is not inside a literal")
	}

	head := "SELECT TABNAME FROM DD02L WHERE AS4LOCAL = 'A' AND TABNAME = 'T000'"
	tail := " OR TABNAME = 'T001'"
	whitespaceAcross := head + strings.Repeat(" ", 289-len(head)-len(tail)) + tail

	cases := []struct {
		name string
		sql  string
	}{
		{"literal across 255", literalAcross},
		{"whitespace across 255", whitespaceAcross},
	}

	for _, sys := range eachSystem(t) {
		t.Run(sys.Name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					if len(tc.sql) <= 255 {
						t.Fatalf("fixture is only %d characters; it must exceed 255", len(tc.sql))
					}
					result, err := sys.Client.RunQuery(ctx, tc.sql, 100)
					if err != nil {
						t.Fatalf("RunQuery: %v", err)
					}
					found := map[string]bool{}
					for _, row := range result.Rows {
						if len(row) > 0 {
							found[strings.TrimSpace(row[0])] = true
						}
					}
					for _, want := range []string{"T000", "T001"} {
						if !found[want] {
							t.Errorf("%s missing from the result (%d rows): the statement ran truncated", want, len(result.Rows))
						}
					}
				})
			}
		})
	}
}
