# RunQuery long SQL lines (adtler#183) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `RunQuery` never sends a SQL line longer than 255 characters to the ADT data preview. Long lines are re-wrapped at whitespace outside string literals and comments, and a line that cannot be wrapped is rejected with an error before any request is sent.

**Architecture:** One unexported function, `wrapLongSQLLines(sql string) (string, error)`, in a new file `adt/query_wrap.go`. It returns the input unchanged when no line exceeds 255 characters, so short queries and existing callers see byte-identical requests. Otherwise it re-wraps only the overlong lines with a small per-line lexer (`sqlWords`) that keeps string literals intact and drops end-of-line comments. `RunQuery` calls it once, after `validateSelectOnly` and before the POST.

**Tech Stack:** Go 1.25, `net/http/httptest` for unit tests, the `integration` build tag with `eachSystem(t)` for live tests.

**Spec:** [adtler#183](https://github.com/Hochfrequenz/adtler/issues/183), issue body plus the follow-up comment that measured the same failure on S/4HANA.

**Branch:** `fix/183-runquery-long-lines`, from `main` @ `3945f35`.

Revision 2, after an independent plan review that checked the behaviour live on both systems. Its findings are already worked into the tasks below: the `*` handling on S/4, `|…|` templates, the tab fixture, and no live run in the implementation step.

## Background

`POST /sap/bc/adt/datapreview/freestyle` cuts every line of the submitted SQL into 255-character pieces and re-joins the pieces with whitespace before appending its own ` INTO TABLE @DATA(...)`. The 255 limit is per line, not per statement. A token or literal that crosses a 255 boundary is split (`WHE` + `RE`; a literal loses its closing quote). The failure modes are:

- a misleading 400 (`"INTO" ist grammatikalisch hier nicht erlaubt.`, `Das Textliteral "…" ist länger als 255 Zeichen.`, `Es ist nur eine SELECT-Anweisung zulässig.`), or
- on the ECC system, a **successful query with a wrong result** when the cut falls on whitespace (reproducer 1 below returns only `T000`).

The issue body says this happens only on SAP_BASIS 750. The follow-up comment measured it on S/4HANA too, with literal-crossing and token-crossing cuts. The fix is therefore applied unconditionally and does not depend on the release.

`RunQuery` (`adt/query.go:35`) today only trims surrounding whitespace and posts the statement as it is.

## Global Constraints

- Hard line limit: **255 characters**, counted in runes (`utf8.RuneCountInString`), not bytes. ABAP counts characters, and a literal with umlauts must not be rejected for its byte length.
- Soft wrap width: **200 characters**. Lines a wrap produces stay at or under 200 unless a single token forces more. Nothing may exceed 255.
- Lines are split on `\n`, and a trailing `\r` is ignored for length. If **no** line exceeds 255, `RunQuery` sends the SQL byte-for-byte unchanged, including any `\r\n`.
- Break only at spaces or tabs **outside** string literals. Literals are `'…'` and `` `…` ``, with doubled-delimiter escapes `''` and ``` `` ```.
- `"` outside a literal starts an end-of-line comment. On a line being wrapped, the comment is dropped. Moving it onto a new line would make it live SQL.
- A line whose column 1 is `*` is a full-line comment. If such a line exceeds 255, it is dropped. Every continuation line a wrap produces starts with exactly one space.
- **A wrap point never falls before a word starting with `*`.** Measured during the plan review: on the S/4 system, a line whose first *non-blank* character is `*` is also treated as a comment. `SELECT MANDT\n *\n FROM T000` silently runs as `SELECT MANDT FROM T000`, and `SELECT\n * FROM DD02L …` returns 500. The ECC system only checks column 1. Indentation alone therefore does not protect a `SELECT *`. The wrapper glues every word that starts with `*` onto the previous word before wrapping.
- `|…|` string templates are live syntax in the data preview (`TABNAME = @( |T000| )` works on both systems). The lexer treats `|` as a third literal delimiter, where `\` escapes the next character, so whitespace and `"` inside a template survive.
- A line that cannot be brought to 255 or less (a token or literal longer than 254 characters) makes `RunQuery` return an error and send nothing.
- Public repository: test fixtures use only SAP standard tables (`T000`, `T001`, `DD02L`). No system names, hosts, or namespaces anywhere.
- No new dependencies. `goconst` is enabled: hoist literals repeated three or more times in test files into a `const` block.
- Commit messages and the PR use the attribution lines from the session (`Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`).

## Review Focus

1. **Multibyte content.** A line of 231 runes but more than 255 bytes (umlauts in a literal) must be sent unchanged. Covered in Task 1 (`multibyte literal counted in runes`).
2. **Tabs as separators.** A long line whose tokens are separated by tabs must wrap at the tabs and must not keep a tab-joined overlong line. Covered in Task 1 (`tab separators`).
3. **Leading `*` after indentation.** An indented long continuation line such as `  * FROM …` must not lose its indentation. Otherwise `*` lands in column 1, which comments out the line on ECC too. Covered in Task 1 (`indented line starting with star keeps indent`). The S/4 system already treats such a user-authored line as a comment before the fix, and the wrapper cannot repair that (see Out of scope).
4. **Neighbouring short lines.** Lines next to a wrapped line must stay verbatim, including odd internal spacing. Covered in Task 1 (`short neighbour line verbatim`).
5. **No request on rejection.** An unwrappable statement must not reach the server at all. Covered in Task 2 (`TestRunQuery_RejectsUnwrappableLine`).

## File Structure

| File | Responsibility |
|---|---|
| `adt/query_wrap.go` (new) | `dataPreviewMaxLine`, `dataPreviewWrapWidth`, `wrapLongSQLLines`, `wrapSQLLine`, `sqlWords` |
| `adt/query_wrap_test.go` (new, package `adt`) | Unit tests for the wrap logic |
| `adt/query.go` (modify `RunQuery`, ~line 31–55) | Call `wrapLongSQLLines`, update the godoc |
| `adt/query_test.go` (append) | httptest tests showing that `RunQuery` sends wrapped SQL and rejects without a request |
| `adt/query_integration_test.go` (append) | `TestRunQuery_LongLine_Integration` over `eachSystem(t)` |

---

### Task 1: SQL line wrapper

**Files:**
- Create: `adt/query_wrap.go`
- Test: `adt/query_wrap_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const dataPreviewMaxLine = 255`, `const dataPreviewWrapWidth = 200`
  - `func wrapLongSQLLines(sql string) (string, error)`. It returns `sql` unchanged when no line exceeds `dataPreviewMaxLine`. Otherwise it returns the re-wrapped SQL joined with `"\n"` (no `\r`). On an unwrappable line it returns an error whose text contains `"255"` and starts with `"line <n>: "`.

- [ ] **Step 1: Write the failing tests**

Create `adt/query_wrap_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./adt/ -run TestWrapLongSQLLines -v`
Expected: build failure, `undefined: wrapLongSQLLines` and `undefined: dataPreviewMaxLine`.

- [ ] **Step 3: Write the implementation**

Create `adt/query_wrap.go`:

```go
package adt

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// dataPreviewMaxLine is the longest SQL line the ADT data preview
	// (POST /sap/bc/adt/datapreview/freestyle) processes intact. The endpoint
	// cuts every line into 255-character pieces and re-joins them with
	// whitespace, which splits any token or literal crossing a boundary
	// (issue #183). Measured on SAP_BASIS 750 and SAP_BASIS 816.
	dataPreviewMaxLine = 255
	// dataPreviewWrapWidth is the width wrapLongSQLLines aims for. It stays
	// well under dataPreviewMaxLine because only lines under the limit have
	// been observed to work. The 255 boundary is inferred from where the cuts
	// fell.
	dataPreviewWrapWidth = 200
)

// wrapLongSQLLines makes sql safe for the ADT data preview, which truncates
// lines longer than dataPreviewMaxLine characters (issue #183). If no line is
// longer, sql is returned unchanged. Otherwise every overlong line is
// re-wrapped at whitespace outside string literals and templates, each
// continuation line is indented by one space, and the result is joined with
// "\n". A line that cannot be brought under the limit yields an error, because
// sending it would run a silently truncated query.
func wrapLongSQLLines(sql string) (string, error) {
	lines := strings.Split(sql, "\n")
	tooLong := false
	for _, line := range lines {
		if utf8.RuneCountInString(strings.TrimSuffix(line, "\r")) > dataPreviewMaxLine {
			tooLong = true
			break
		}
	}
	if !tooLong {
		return sql, nil
	}

	out := make([]string, 0, len(lines))
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if utf8.RuneCountInString(line) <= dataPreviewMaxLine {
			out = append(out, line)
			continue
		}
		wrapped, err := wrapSQLLine(line)
		if err != nil {
			return "", fmt.Errorf("line %d: %w", i+1, err)
		}
		out = append(out, wrapped...)
	}
	return strings.Join(out, "\n"), nil
}

// wrapSQLLine re-wraps one overlong SQL line greedily to dataPreviewWrapWidth.
// A full-line comment ('*' in column 1) and an end-of-line comment ('"') are
// dropped rather than moved, since a moved comment would become live SQL. A
// line that starts with whitespace keeps a one-space indent.
//
// A wrap never falls before a word starting with '*': the ECC data preview
// treats only a '*' in column 1 as a comment, but the S/4 data preview also
// treats a line whose first non-blank character is '*' as one, so the
// one-space indent alone would not protect "SELECT *".
func wrapSQLLine(line string) ([]string, error) {
	if strings.HasPrefix(line, "*") {
		return nil, nil
	}

	var words []string
	for _, w := range sqlWords(line) {
		if strings.HasPrefix(w, "*") && len(words) > 0 {
			words[len(words)-1] += " " + w
			continue
		}
		words = append(words, w)
	}

	cur := ""
	if strings.TrimLeft(line, " \t") != line {
		cur = " "
	}
	curLen := len(cur)
	onLine := 0

	var out []string
	for _, w := range words {
		wLen := utf8.RuneCountInString(w)
		if onLine > 0 && curLen+1+wLen > dataPreviewWrapWidth {
			out = append(out, cur)
			cur, curLen, onLine = " ", 1, 0
		}
		if onLine > 0 {
			cur += " "
			curLen++
		}
		cur += w
		curLen += wLen
		onLine++
	}
	if onLine > 0 {
		out = append(out, cur)
	}

	for _, l := range out {
		if n := utf8.RuneCountInString(l); n > dataPreviewMaxLine {
			return nil, fmt.Errorf("a token too long to split leaves a line of %d characters; "+
				"the ADT data preview truncates SQL lines after %d characters", n, dataPreviewMaxLine)
		}
	}
	return out, nil
}

// sqlWords splits one SQL line into whitespace-separated words. A string
// literal ('…' or `…`) or string template (|…|, where '\' escapes the next
// character) always stays inside a single word, and everything from a '"'
// outside of those to the end of the line is an ABAP comment and is dropped.
//
// Doubled delimiters ('' and ``) need no special case: closing a literal and
// immediately reopening it keeps both halves in the same word.
func sqlWords(line string) []string {
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}

	rs := []rune(line)
	var quote rune // delimiter of the open literal or template, 0 outside
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '|' && r == '\' && i+1 < len(rs):
			cur.WriteRune(r)
			cur.WriteRune(rs[i+1])
			i++
		case quote != 0:
			cur.WriteRune(r)
			if r == quote {
				quote = 0
			}
		case r == '\'' || r == '`' || r == '|':
			quote = r
			cur.WriteRune(r)
		case r == '"':
			flush()
			return words
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return words
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./adt/ -run TestWrapLongSQLLines -v`
Expected: PASS for every subtest.

Then break the logic on purpose and confirm that the tests catch it, restoring the code after each mutant:
- Remove the `case quote != 0:` arm. `TestWrapLongSQLLines_KeepsLiteralsIntact` must FAIL.
- Remove `|| r == '|'` from the opening-delimiter case. `TestWrapLongSQLLines_KeepsLiteralsIntact` must FAIL, because the template is altered.
- Remove the `*`-glue loop (use `sqlWords(line)` directly). `TestWrapLongSQLLines_StarAtWrapPoint` must FAIL.
- Remove `|| r == '	'`. `TestWrapLongSQLLines_Wraps/tab_separators` must FAIL.

- [ ] **Step 5: Lint, vet, and commit**

Run: `gofmt -l ./adt/ && go vet ./adt/ && golangci-lint run --enable dupl,goconst,gocyclo ./adt/...`. If `golangci-lint` is not installed locally, say so in the report and rely on CI.
Expected: no output from `gofmt -l`, no findings.

```bash
git add adt/query_wrap.go adt/query_wrap_test.go
git commit -m "fix(#183): wrap SQL lines longer than 255 characters for the data preview

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Use the wrapper in RunQuery

**Files:**
- Modify: `adt/query.go` (`RunQuery`, godoc at lines 31–34 and body at lines 35–55)
- Test: `adt/query_test.go` (append)
- Docs: `CLAUDE.md`, one bullet under "SAP system differences" (exact text in the plan's "Documentation" section near the end)

**Interfaces:**
- Consumes: `wrapLongSQLLines(sql string) (string, error)` from Task 1. Test fixtures `discoveryPath`, `dataPreviewPath` and `emptyTableData` already exist in `adt/classrun_timeout_internal_test.go` (same package `adt`). Reuse them, do not redeclare them.
- Produces: no new API. `RunQuery` now returns `RunQuery: line <n>: …` errors for unwrappable SQL.

- [ ] **Step 1: Write the failing tests**

Append to `adt/query_test.go`, and extend its import block with `"io"`, `"net/http"`, `"net/http/httptest"`, `"strings"`, `"unicode/utf8"` and `sapmcpconfig "github.com/Hochfrequenz/sap-mcp-config"`:

```go
// dataPreviewCaptureServer answers the CSRF preflight and the data preview
// endpoint, and forwards every data preview request body to the returned
// channel (buffered, so the handler never blocks).
func dataPreviewCaptureServer(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	bodies := make(chan string, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == discoveryPath {
			w.Header().Set("X-CSRF-Token", "token")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == dataPreviewPath {
			b, _ := io.ReadAll(r.Body)
			bodies <- string(b)
		}
		w.Header().Set("Content-Type", "application/vnd.sap.adt.datapreview.table.v1+xml")
		_, _ = w.Write([]byte(emptyTableData))
	}))
	t.Cleanup(srv.Close)
	return srv, bodies
}

func newDataPreviewTestClient(host string) Client {
	return NewClient(sapmcpconfig.SAPSystem{Host: host, User: "U", Password: "P", Client: "100"})
}

// TestRunQuery_WrapsLongLines is the unit-level regression guard for issue
// #183: a statement with a line over 255 characters must reach SAP wrapped,
// with the last literal still intact.
func TestRunQuery_WrapsLongLines(t *testing.T) {
	srv, bodies := dataPreviewCaptureServer(t)
	c := newDataPreviewTestClient(srv.URL)

	sql := "SELECT TABNAME FROM DD02L WHERE TABNAME IN ( " + strings.Repeat("'T000', ", 40) + "'T001' )"
	if _, err := c.RunQuery(context.Background(), sql, 10); err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	sent := <-bodies
	for i, line := range strings.Split(sent, "\n") {
		if n := utf8.RuneCountInString(line); n > dataPreviewMaxLine {
			t.Errorf("line %d sent with %d characters", i+1, n)
		}
	}
	if a, b := strings.Join(strings.Fields(sql), " "), strings.Join(strings.Fields(sent), " "); a != b {
		t.Errorf("tokens changed on the way to SAP:\n in: %s\nout: %s", a, b)
	}
}

// TestRunQuery_ShortSQLUnchanged pins that short statements reach SAP exactly
// as before the fix (after the existing TrimSpace).
func TestRunQuery_ShortSQLUnchanged(t *testing.T) {
	srv, bodies := dataPreviewCaptureServer(t)
	c := newDataPreviewTestClient(srv.URL)

	sql := "SELECT *\r\n  FROM T000"
	if _, err := c.RunQuery(context.Background(), "  "+sql+"\n", 10); err != nil {
		t.Fatalf("RunQuery: %v", err)
	}
	if sent := <-bodies; sent != sql {
		t.Errorf("sent %q, want %q", sent, sql)
	}
}

// TestRunQuery_RejectsUnwrappableLine pins that a line SAP would silently
// truncate is refused locally, without any data preview request.
func TestRunQuery_RejectsUnwrappableLine(t *testing.T) {
	srv, bodies := dataPreviewCaptureServer(t)
	c := newDataPreviewTestClient(srv.URL)

	sql := "SELECT A FROM T000 WHERE B = '" + strings.Repeat("x", 300) + "'"
	_, err := c.RunQuery(context.Background(), sql, 10)
	if err == nil {
		t.Fatal("expected an error for an unwrappable line")
	}
	if !strings.Contains(err.Error(), "255") {
		t.Errorf("error does not name the limit: %v", err)
	}
	select {
	case sent := <-bodies:
		t.Errorf("request was sent despite the error: %q", sent)
	default:
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./adt/ -run "TestRunQuery_(WrapsLongLines|ShortSQLUnchanged|RejectsUnwrappableLine)" -v`
Expected: `TestRunQuery_WrapsLongLines` FAILs with `line 1 sent with 3xx characters`. `TestRunQuery_RejectsUnwrappableLine` FAILs with `expected an error`. `TestRunQuery_ShortSQLUnchanged` PASSes, because it pins existing behaviour.

- [ ] **Step 3: Wire the wrapper into RunQuery**

In `adt/query.go`, replace the godoc and the opening of `RunQuery`:

```go
// RunQuery executes a read-only SQL query via the ADT data preview endpoint.
// Only single SELECT statements are allowed; anything else is rejected.
// The data preview truncates SQL lines longer than 255 characters, so longer
// lines are re-wrapped at whitespace outside string literals before sending,
// and a line that cannot be wrapped is rejected (issue #183).
// The request goes through the long-timeout HTTP client; if the caller's context
// has no deadline, defaultLongRunTimeout is applied.
func (c *httpClient) RunQuery(ctx context.Context, sql string, maxRows int) (*QueryResult, error) {
	trimmed := strings.TrimSpace(sql)
	if err := validateSelectOnly(trimmed); err != nil {
		return nil, fmt.Errorf("RunQuery: %w", err)
	}
	wrapped, err := wrapLongSQLLines(trimmed)
	if err != nil {
		return nil, fmt.Errorf("RunQuery: %w", err)
	}
```

Then change the POST body from `strings.NewReader(trimmed)` to `strings.NewReader(wrapped)`. Leave the rest of `RunQuery` unchanged. The later `resp, err := c.doMutateLong(...)` still compiles, because `resp` is a new variable.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./adt/ -run "TestRunQuery|TestWrapLongSQLLines" -v`, then `go test ./...`
Expected: all PASS.

- [ ] **Step 5: Document, lint, vet, and commit**

Add the CLAUDE.md bullet from the "Documentation" section verbatim. Then run: `gofmt -l ./adt/ && go vet ./adt/ && golangci-lint run --enable dupl,goconst,gocyclo ./adt/...`

```bash
git add adt/query.go adt/query_test.go CLAUDE.md
git commit -m "fix(#183): RunQuery wraps long SQL lines before posting

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Live regression test on both systems

**Files:**
- Modify: `adt/query_integration_test.go` (append, and add `"strings"` to the imports)

**Interfaces:**
- Consumes: `eachSystem(t) []integrationSystem` (`adt/integration_helpers_test.go:162`, fields `Name`, `Client`). `adt.Client.RunQuery(ctx, sql string, maxRows int) (*adt.QueryResult, error)`. `QueryResult.Rows [][]string`.
- Produces: `TestRunQuery_LongLine_Integration`.

**Do not run this test in the implementation step.** This machine has a SAP config, so `go test -tags integration` would reach a real system. `TestMain` (`setupFixtures`) also creates a transport request on every integration run, whatever `-run` selects, and does not always release it. The test only has to compile under the `integration` tag. The workflow's step-4 agent runs it live, with and without the fix. Without the fix it is expected to fail on both systems in the `literal across 255` case and on the ECC system in the `whitespace across 255` case.

- [ ] **Step 1: Write the integration test**

Append to `adt/query_integration_test.go`:

```go
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
```

- [ ] **Step 2: Verify that it compiles and vets under the integration tag**

Run: `go build -tags integration ./adt/... && go vet -tags integration ./adt/...`
Expected: no output.


- [ ] **Step 3: Full suite and commit**

Run: `go test ./...` and `gofmt -l ./adt/`
Expected: PASS, no gofmt output.

```bash
git add adt/query_integration_test.go
git commit -m "test(#183): live guard for SQL lines longer than 255 characters

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Out of scope

- `validateSelectOnly` rejects a `;` even inside a string literal. That is existing behaviour and unrelated to line length.
- Short lines (255 characters or less) that start with `*` or contain `"` keep their ABAP comment meaning, exactly as before.
- SQL with a string literal spanning several physical lines is invalid in ABAP. The lexer works per line and does not try to repair it.
- A user-authored line whose first non-blank character is `*` (for example `SELECT
  * FROM …`) is already a comment on the S/4 system before this fix. The wrapper keeps the indent of such a line but cannot change how S/4 reads it.
- String templates with embedded expressions that themselves contain `|` (`|a{ `x|y` }b|`) are not lexed. That nesting is not expected in data preview SQL.

## Documentation

As part of Task 2, add one bullet to `CLAUDE.md`, section "SAP system differences":

```markdown
- **Data preview comment lines**: both releases cut each SQL line of `POST /sap/bc/adt/datapreview/freestyle` after 255 characters (#183). S/4 additionally treats a line whose first *non-blank* character is `*` as a comment, while R/3 only checks column 1. `wrapLongSQLLines` (adt/query_wrap.go) therefore never starts a continuation line with `*`.
```
