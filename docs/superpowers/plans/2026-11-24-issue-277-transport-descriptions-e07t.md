# Issue 277: GetTransportRequests fills descriptions from E07T

Goal: every request of a `GetTransportRequests` page carries the description E07T holds for it, also when the
transport organizer tree does not show the request.

Design (decisions, all in `adt/transport.go`):

- After the E070 page query succeeds, read E07T for the page's request numbers with a second data preview query
  `SELECT TRKORR, LANGU, AS4TEXT FROM E07T WHERE TRKORR IN (...) ORDER BY TRKORR, LANGU`. A JOIN is not possible
  (the data preview rejects it). An empty page sends no query.
- The IN list reuses `chunkNames` + `buildSQLInList` with `ddicInListMaxBytes` (150 bytes: the documented limit of the
  data preview for IN lists; the join stays comma-only so `wrapLongSQLLines` never has to split it). Each number is
  checked against `transportNumberRe` first; a number that fails is not queried (its Description stays empty).
- E07T holds one row per language. Language choice is made on the client: the logon language (`cfg.Language`, ISO
  "EN" or SAP "E" form), then English, then any other, then an empty LANGU; ties keep the first row of the sorted
  answer. No LANGU condition in SQL, because how the data preview prints LANGU (internal or external) is not measured.
  An empty AS4TEXT never replaces a text.
- A query that returns `e07tMaxRows` rows or more counts as failed (the data preview truncates silently).
- The tree is read only when it is needed: when E070 failed (tree-only page, unchanged) or when E07T failed.
  - 401 / failed refresh from E07T: ends the call, no page (CLAUDE.md "A 401 is final").
  - E07T failed otherwise, tree ok: page described from the tree, error wraps `ErrTransportListPartial`.
  - E07T failed and tree failed: page without descriptions, error wraps `ErrTransportListPartial`.
  - E070 failed: unchanged (E07T is not queried).
- Docs: doc comment of `GetTransportRequests`, `getTransportRequestsViaQuery` comment, CLAUDE.md paragraph
  "Transport organizer tree is partial".

## Task 1: E07T description source (test first)

Files: `adt/transport.go`, `adt/transport_state_test.go`, `adt/transport_integration_test.go`, `CLAUDE.md`.

Step 1 (failing test first). In `adt/transport_state_test.go`:

- Extend `transportListAnswers` with `e07t string` and `e07tStatus int`; the mock's data preview handler answers a
  statement containing `FROM E07T` with `e07t` (or `e07tStatus`), records the call as `"e07t"` and the statement in a
  new field `e07tSQL []string`; every other statement is the E070 query as before.
- Add `e07tRow [3]string` (number, language, text) and `e07tRows(...)` that builds the data preview XML with the
  columns TRKORR, LANGU, AS4TEXT (same shape as `e070Rows`).
- Rewrite `TestGetTransportRequests_FiltersAndDescribes`: E07T answers a text for every request including
  AAAK900008 (not in the tree); assert every request has its E07T description, `calls == ["e070","e07t"]` (tree not
  read) and `treeQueries` empty.
- Add `TestGetTransportRequests_DescriptionLanguage`: logon `Language` "DE" picks the German text, "" picks English,
  "EN" picks English, a request with only a French text gets the French one, an empty text never wins.
- Add `TestGetTransportRequests_DescriptionQueries`: 30 requests produce several E07T queries; every request number
  is in exactly one statement; each IN list is at most `ddicInListMaxBytes` long (count bytes between the
  parentheses; the statement may contain it on its own line); a request number with a quote (`AAAK9'1`) is in no
  E07T statement and its Description is empty; an empty page sends no E07T query.
- Update `TestGetTransportRequests_SourceFailures` and the other callers of `transportListAnswers` for the new
  call sequence (see Design) and add the cases: 401 from E07T (calls e070, e07t; nil page), E07T fails + tree ok
  (calls e070, e07t, tree; partial; described from the tree: AAAK900002 and AAAK900001 yes, AAAK900008 no), E07T
  fails + tree fails (partial, no descriptions), E07T truncated at `e07tMaxRows` (treated as failure).
  Tests that only care about E070 give `e07t: e07tRows()` so that no fallback happens.

Run `bash <helper> run --gate test ./adt/ -run TestGetTransportRequests` and see them fail (compile error for the
missing fields counts only after the production names exist: add the production names as stubs first or run after
Step 2 with the new assertions failing on the old behaviour; the implementer must report which of the two it saw).

Step 2 (implementation) in `adt/transport.go`:

```go
// e07tMaxRows caps one E07T description query. The data preview truncates without an error, so
// an answer this long counts as failed.
const e07tMaxRows = 5000

// getTransportDescriptionsViaQuery reads the short texts of the requests numbers from E07T
// (TRKORR -> AS4TEXT) through the data preview, which rejects a JOIN of E070 with E07T. A request
// without a text, or with a number that is not safe to embed in the statement, is missing from
// the result. E07T holds one text per language; descriptionLanguageRank picks one.
func (c *httpClient) getTransportDescriptionsViaQuery(ctx context.Context, numbers []string) (map[string]string, error) {
	var safe []string
	for _, n := range numbers {
		if transportNumberRe.MatchString(n) {
			safe = append(safe, n)
		}
	}
	texts := make(map[string]string, len(safe))
	ranks := make(map[string]int, len(safe))
	for _, chunk := range chunkNames(safe, ddicInListMaxBytes) {
		qr, err := c.RunQuery(ctx,
			"SELECT TRKORR, LANGU, AS4TEXT FROM E07T WHERE TRKORR IN ("+buildSQLInList(chunk)+") ORDER BY TRKORR, LANGU",
			e07tMaxRows)
		if err != nil {
			return nil, fmt.Errorf("GetTransportRequests: E07T query: %w", err)
		}
		idx := queryColumnIndexes(qr, "TRKORR", "LANGU", "AS4TEXT")
		if err := requireQueryColumns("GetTransportRequests: E07T query", idx, "TRKORR", "LANGU", "AS4TEXT"); err != nil {
			return nil, err
		}
		if len(qr.Rows) >= e07tMaxRows {
			return nil, fmt.Errorf("GetTransportRequests: E07T query reached the row cap of %d and may be truncated", e07tMaxRows)
		}
		for _, row := range qr.Rows {
			number, text := queryCell(row, idx["TRKORR"]), queryCell(row, idx["AS4TEXT"])
			if text == "" {
				continue
			}
			rank := descriptionLanguageRank(queryCell(row, idx["LANGU"]), c.cfg.Language)
			if prev, seen := ranks[number]; !seen || rank < prev {
				texts[number], ranks[number] = text, rank
			}
		}
	}
	return texts, nil
}

// descriptionLanguageRank orders the languages of an E07T row, lower is better: the logon language
// (SAP "E" or ISO "EN" form), English, any other, none.
// shortcut: an ISO code is matched by its first letter, which holds for E and D but not for every
// language (ZH is "1" in SAP); such a logon language falls back to English, upgrade with a real
// ISO-to-SAP table when a consumer needs it.
func descriptionLanguageRank(langu, logon string) int {
	l, own := strings.ToUpper(langu), strings.ToUpper(strings.TrimSpace(logon))
	switch {
	case l == "":
		return 3
	case own != "" && (l == own || (len(own) == 2 && l == own[:1])):
		return 0
	case l == "E" || l == "EN":
		return 1
	}
	return 2
}

func transportNumbers(requests []TransportRequest) []string {
	numbers := make([]string, len(requests))
	for i, tr := range requests {
		numbers[i] = tr.Number
	}
	return numbers
}

// fillDescriptions sets Description of every request that has an entry in descriptions and clears
// the others.
func fillDescriptions(requests []TransportRequest, descriptions map[string]string) {
	for i := range requests {
		requests[i].Description = descriptions[requests[i].Number]
	}
}
```

`GetTransportRequests` after the E070 call and its credential check:

```go
	var textErr error
	if queryErr == nil {
		var texts map[string]string
		texts, textErr = c.getTransportDescriptionsViaQuery(ctx, transportNumbers(page.Requests))
		if textErr != nil && credentialRejected(textErr) {
			return nil, textErr
		}
		if textErr == nil {
			fillDescriptions(page.Requests, texts)
			return page, nil
		}
	}

	// The tree is read only when E070 or E07T failed: as the page (E070) or as the source of the
	// descriptions (E07T).
	treeRequests, treeErr := c.getTransportRequestsTree(ctx, user)
	if treeErr != nil && credentialRejected(treeErr) {
		return nil, treeErr
	}

	switch {
	case queryErr != nil && treeErr != nil:  (unchanged)
	case queryErr != nil:                      (unchanged)
	}

	descriptions := make(map[string]string, len(treeRequests))
	for _, tr := range treeRequests { descriptions[tr.Number] = tr.Description }
	fillDescriptions(page.Requests, descriptions)
	if treeErr != nil {
		return page, fmt.Errorf("%w: neither E07T nor the transport organizer tree could be read, descriptions are missing: E07T: %w; tree: %w",
			ErrTransportListPartial, textErr, treeErr)
	}
	return page, fmt.Errorf("%w: E07T could not be read, the descriptions are those the transport organizer tree shows: %w",
		ErrTransportListPartial, textErr)
```

Update the doc comments (`GetTransportRequests`, `getTransportRequestsViaQuery`, `getTransportRequestsTree` stays) and
the CLAUDE.md paragraph "Transport organizer tree is partial".

Step 3 (integration test, compile-checked only: `go vet -tags=integration ./adt/...`; never run). In
`adt/transport_integration_test.go` add `TestGetTransportRequests_Descriptions_Integration`: on every system
(`eachSystem`), read the first page (Limit 50) of every user's released requests; skip if empty or
`ErrTransportListPartial`-wrapped errors fail the test; for each request ask E07T directly with `RunQuery`
(`SELECT AS4TEXT FROM E07T WHERE TRKORR = '<number>'`, number validated by the page itself) and require: if E07T
holds a non-empty text for the request in any language, `Description` is non-empty (and, if E07T holds exactly one text,
equal to it). Log counts only (requests, described, with E07T text), never numbers or texts.

Step 4: run the covering tests (`--gate test ./adt/ -run 'TestGetTransportRequests|TestTransport'`), then the
gate steps `fmt`, `lint`, `datacheck`, `vet` (also with `go vet -tags=integration ./adt/...`), and commit.

No task without a failing test first.

## Revisions after the plan review (these override the text above)

1. Language: no first-letter shortcut. Add `var isoToSAPLanguage = map[string]string{"EN":"E","DE":"D","FR":"F","ES":"S","IT":"I","PT":"P","NL":"N","PL":"L","DA":"K","SV":"V","JA":"J","RU":"R","ZH":"1","KO":"3","CS":"C","TR":"T","FI":"U","NO":"O","HU":"H","EL":"G"}`.
   `descriptionLanguageRank(langu, logon)`: normalise `own` = upper-case trimmed logon; if `len(own)==2` and the map has it, `sap := map value` else `sap := own`. Rank 0 when `l == own || l == sap`; rank 1 when `l == "E" || l == "EN"`; rank 3 when `l == ""`; else 2. Drop the shortcut comment.
2. Partial result: `getTransportDescriptionsViaQuery` returns the texts read so far together with the error when a chunk fails (`return texts, fmt.Errorf(...)`). In `GetTransportRequests`, the fallback fills descriptions from the E07T texts first and from the tree only for requests without one (`fillDescriptions` takes a list of maps, first hit wins; empty strings do not count). With `treeErr` the page keeps the E07T texts. New test: chunk 2 fails (mock answers the second E07T statement with 500), the texts of chunk 1 survive.
3. Row cap: no truncation check. `e07tMaxRows = 1000` is only the maxRows argument (a chunk holds about 11 numbers; 1000 rows would need ~90 installed languages). Remove the `len(qr.Rows) >= e07tMaxRows` check and its test.
4. Mock: the data preview handler answers an `FROM E07T` statement from a list `e07tTexts []e07tRow` of the answers struct: rows whose number occurs in the statement (as `'NUMBER'`), so every chunk gets its own rows. Nothing set means no rows (`e07tRows()`); `e07tStatus` answers that status. `e07tFailOnCall int` (1-based, 0 never) answers 500 for that E07T statement only. E07T statements are recorded in `e07tSQL` and `calls` only, never in `sql` / `rowNumbers`.
5. Existing tests: update `EmptyUserIsLogonUser` and `AllUsers`: the tree is read only after a failure, so assert the user scope of the tree read in a failure case (`e07tStatus` 500 with a non-empty E070 page), and for an empty E070 page assert that no tree and no E07T read happened. `SourceFailures`: replace "401 from the tree" by "E07T 500 + tree 401", add "401 from E07T", "E07T 500 + tree ok (E07T none, tree texts)", "E07T 500 + tree 500". `UnexpectedSortKey` and `Paging` get E07T texts so that no fallback happens. Tests are in package `adt_test`: hardcode 150 (`ddicInListMaxBytes`) with a comment.
6. Docs: also update the `ErrTransportListPartial` doc ("two sources" -> three), the doc comment (E07T read authorization; a user who may read E070 but not E07T gets a partial error on every call; the cost: one data preview query per about 11 requests of the page), and the CLAUDE.md paragraph "A 401 is final under Basic auth" (E07T as a third source: E070, then E07T, then the tree only if needed). Remove the comment "Unscoped (every user), the tree shows the logon user's requests...", keep the information next to the tree read.
7. Integration test: skip on an empty page, any error (partial included) fails; compare with `strings.TrimSpace`; log how many requests were checked.
