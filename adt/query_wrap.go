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
	rawWords, unterminated := sqlWords(line)
	for _, w := range rawWords {
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
		if utf8.RuneCountInString(l) <= dataPreviewMaxLine {
			continue
		}
		// Only a single word can overflow a line, so the line minus its indent
		// is the token.
		token := strings.TrimLeft(l, " ")
		if unterminated {
			return nil, fmt.Errorf("the line ends inside an unterminated string literal starting with %q",
				tokenPreview(rawWords[len(rawWords)-1]))
		}
		return nil, fmt.Errorf("a token of %d characters cannot be split (%q); "+
			"the ADT data preview truncates SQL lines after %d characters",
			utf8.RuneCountInString(token), tokenPreview(token), dataPreviewMaxLine)
	}
	return out, nil
}

// tokenPreview shortens s to its first 30 characters for an error message.
func tokenPreview(s string) string {
	const maxRunes = 30
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes]) + "…"
}

// sqlWords splits one SQL line into whitespace-separated words. A string
// literal ('…' or `…`) or string template (|…|, where '\' escapes the next
// character) always stays inside a single word, and everything from a '"'
// outside of those to the end of the line is an ABAP comment and is dropped.
//
// A doubled delimiter needs no special case: closing a literal and immediately
// reopening it keeps both halves in the same word.
//
// unterminated reports that the line ended inside a literal or template, whose
// word then holds the rest of the line.
func sqlWords(line string) (words []string, unterminated bool) {
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
		case quote == '|' && r == '\\' && i+1 < len(rs):
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
			return words, false
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return words, quote != 0
}
