package adt

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Fixtures for the issue #183 wrap tests. wrapTestPrefix starts every long
// statement; wrapTestFiller produces n predicates of 10 characters each.
const (
	wrapTestPrefix = "SELECT A FROM T000 WHERE "
	wrapTestClause = "B = B AND "
	wrapTestEnd    = "C = C"
)

func wrapTestFiller(n int) string { return strings.Repeat(wrapTestClause, n) }

// assertLinesWithin fails if any line of sql is longer than limit runes.
func assertLinesWithin(t *testing.T, sql string, limit int) {
	t.Helper()
	for i, line := range strings.Split(sql, "\n") {
		if n := utf8.RuneCountInString(line); n > limit {
			t.Errorf("line %d has %d runes, limit %d: %q", i+1, n, limit, line)
		}
	}
}

// assertSameTokens fails if wrapping changed anything but whitespace outside
// literals. Only valid for input without comments.
func assertSameTokens(t *testing.T, in, out string) {
	t.Helper()
	if a, b := strings.Join(strings.Fields(in), " "), strings.Join(strings.Fields(out), " "); a != b {
		t.Errorf("tokens changed:\n in: %s\nout: %s", a, b)
	}
}

// assertContinuationsIndented fails if any line after the first starts with a
// non-space character. A '*' in column 1 would turn the line into a comment.
func assertContinuationsIndented(t *testing.T, out string) {
	t.Helper()
	for i, line := range strings.Split(out, "\n")[1:] {
		if !strings.HasPrefix(line, " ") {
			t.Errorf("continuation line %d not indented: %q", i+2, line)
		}
	}
}

func TestWrapLongSQLLines_Unchanged(t *testing.T) {
	sel, from := "SELECT MANDT", " FROM T000"
	exactly255 := sel + strings.Repeat(" ", dataPreviewMaxLine-len(sel)-len(from)) + from
	tests := []struct {
		name string
		sql  string
	}{
		{"short single line", "SELECT * FROM T000"},
		{"short CRLF lines kept verbatim", "SELECT *\r\nFROM T000\r\nWHERE MANDT = '000'"},
		{"line of exactly 255", exactly255},
		{"multibyte literal counted in runes", wrapTestPrefix + "B = '" + strings.Repeat("ä", 200) + "'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wrapLongSQLLines(tt.sql)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.sql {
				t.Errorf("SQL changed:\n got: %q\nwant: %q", got, tt.sql)
			}
		})
	}
}

func TestWrapLongSQLLines_Wraps(t *testing.T) {
	sel, from := "SELECT MANDT", " FROM T000"
	line256 := sel + strings.Repeat(" ", dataPreviewMaxLine+1-len(sel)-len(from)) + from
	inList := "SELECT TABNAME FROM DD02L WHERE TABNAME IN ( " + strings.Repeat("'T000', ", 40) + "'T001' )"
	tabbed := strings.TrimSuffix(wrapTestPrefix, " ") + strings.Repeat("\tB\t=\tB\tAND", 30) + "\t" + wrapTestEnd
	tests := []struct {
		name string
		sql  string
	}{
		{"line of 256", line256},
		{"long IN list", inList},
		{"tab separators", tabbed},
		{"long second line", "SELECT A\nFROM T000 WHERE " + wrapTestFiller(30) + wrapTestEnd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := wrapLongSQLLines(tt.sql)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got == tt.sql {
				t.Fatalf("SQL was not wrapped")
			}
			assertLinesWithin(t, got, dataPreviewWrapWidth)
			assertSameTokens(t, tt.sql, got)
		})
	}
	t.Run("line of 256 collapses to one short line", func(t *testing.T) {
		got, _ := wrapLongSQLLines(line256)
		if got != "SELECT MANDT FROM T000" {
			t.Errorf("got %q", got)
		}
	})
	t.Run("continuations of a wrapped first line are indented", func(t *testing.T) {
		got, _ := wrapLongSQLLines(inList)
		assertContinuationsIndented(t, got)
	})
}

func TestWrapLongSQLLines_KeepsLiteralsIntact(t *testing.T) {
	quoted := "'it''s  a long literal'"
	backtick := "`x``y  z`"
	quoteInLiteral := `'say "hi" there'`
	// String template with a double space, a '"' and an escaped '|'.
	template := `|a  "b\| c|`
	sql := wrapTestPrefix + wrapTestFiller(19) + "C = " + quoted + " AND D = " + backtick +
		" AND E = " + quoteInLiteral + " AND F = @( " + template + " ) AND " + wrapTestFiller(10) + wrapTestEnd
	got, err := wrapLongSQLLines(sql)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertLinesWithin(t, got, dataPreviewMaxLine)
	assertSameTokens(t, sql, got)
	// assertSameTokens above also proves nothing after the literal holding a
	// '"' was dropped as a comment.
	for _, lit := range []string{quoted, backtick, quoteInLiteral, template} {
		if !strings.Contains(got, lit) {
			t.Errorf("literal %s was split or altered:\n%s", lit, got)
		}
	}
}

func TestWrapLongSQLLines_Comments(t *testing.T) {
	t.Run("end-of-line comment dropped on wrapped line", func(t *testing.T) {
		code := wrapTestPrefix + wrapTestFiller(25) + wrapTestEnd
		sql := code + ` " OR 1 = 1 ` + strings.Repeat("x", 50)
		got, err := wrapLongSQLLines(sql)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(got, "OR 1 = 1") || strings.Contains(got, `"`) {
			t.Errorf("comment survived the wrap:\n%s", got)
		}
		assertSameTokens(t, code, got) // SQL before the comment is fully kept
		assertLinesWithin(t, got, dataPreviewMaxLine)
	})
	t.Run("long full-line star comment dropped", func(t *testing.T) {
		sql := "SELECT A FROM T000\n* " + strings.Repeat("x", 300) + "\nWHERE A = A"
		got, err := wrapLongSQLLines(sql)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "SELECT A FROM T000\nWHERE A = A" {
			t.Errorf("got %q", got)
		}
	})
}

func TestWrapLongSQLLines_StarAtWrapPoint(t *testing.T) {
	// "SELECT" plus a 193-character word fills the first line to exactly 200,
	// so a naive greedy wrap would start the next line with " *". The S/4
	// system treats a line whose first non-blank character is '*' as a
	// comment, so the wrapper must keep '*' glued to the word before it.
	sql := "SELECT " + strings.Repeat("A", 193) + " * FROM T000 WHERE " + wrapTestFiller(6) + wrapTestEnd
	got, err := wrapLongSQLLines(sql)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "*") {
			t.Errorf("line %d starts with '*' after indentation: %q", i+1, line)
		}
	}
	assertContinuationsIndented(t, got)
	assertSameTokens(t, sql, got)
}

func TestWrapLongSQLLines_Neighbours(t *testing.T) {
	t.Run("short neighbour line verbatim", func(t *testing.T) {
		neighbour := "  AND   X = X"
		sql := wrapTestPrefix + wrapTestFiller(30) + wrapTestEnd + "\n" + neighbour
		got, err := wrapLongSQLLines(sql)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasSuffix(got, "\n"+neighbour) {
			t.Errorf("neighbour line changed:\n%q", got)
		}
	})
	t.Run("CRLF input with a long line has no CR left", func(t *testing.T) {
		sql := "SELECT A\r\nFROM T000 WHERE " + wrapTestFiller(30) + wrapTestEnd + "\r\nAND X = X"
		got, err := wrapLongSQLLines(sql)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(got, "\r") {
			t.Errorf("CR left in output: %q", got)
		}
		assertLinesWithin(t, got, dataPreviewMaxLine)
		assertSameTokens(t, sql, got)
	})
	t.Run("indented line starting with star keeps indent", func(t *testing.T) {
		sql := "SELECT\n  * FROM T000 WHERE " + wrapTestFiller(30) + wrapTestEnd
		got, err := wrapLongSQLLines(sql)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for i, line := range strings.Split(got, "\n") {
			if strings.HasPrefix(line, "*") {
				t.Errorf("line %d starts with '*': %q", i+1, line)
			}
		}
		assertSameTokens(t, sql, got)
	})
}

func TestWrapLongSQLLines_TokenLimit(t *testing.T) {
	t.Run("254-character literal fits on its own line", func(t *testing.T) {
		lit := "'" + strings.Repeat("x", 252) + "'"
		sql := wrapTestPrefix + "B = " + lit + " AND " + wrapTestEnd
		got, err := wrapLongSQLLines(sql)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(got, "\n "+lit+"\n") {
			t.Errorf("literal not on its own continuation line:\n%s", got)
		}
		assertLinesWithin(t, got, dataPreviewMaxLine)
	})
	t.Run("255-character literal is rejected", func(t *testing.T) {
		sql := wrapTestPrefix + "B = '" + strings.Repeat("x", 253) + "'"
		_, err := wrapLongSQLLines(sql)
		if err == nil {
			t.Fatal("expected an error for an unwrappable literal")
		}
		if !strings.Contains(err.Error(), "line 1: ") || !strings.Contains(err.Error(), "255") {
			t.Errorf("error does not name the line and the limit: %v", err)
		}
	})
}
