# Redact internal environment data (adtler#135, adtler#143) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** No tracked file identifies the internal SAP landscape any more. Integration tests find their fixtures at runtime instead of naming them, and a CI check stops transport numbers and namespaced object names from coming back.

**Architecture:** Fixture identifiers are replaced by synthetic values of the same shape, so parsers and the tests that assert on them still work: transport numbers keep three characters, `K` and six digits; a namespaced name keeps its `/X/` (or `%2fX%2f`) form; a class name stays padded to the same column. The integration tests that pin real objects discover a suitable one through `RunQuery` on the repository tables instead. A small Go command, `tools/datacheck`, scans every tracked file for the identifier shapes that can be checked mechanically, and a workflow runs it on every push and pull request.

**Tech Stack:** Go 1.26 (`go.mod`), `regexp`, GitHub Actions, the `integration` build tag for the live tests.

**Spec:** [adtler#135](https://github.com/Hochfrequenz/adtler/issues/135) with its 2026-09-21 comment, [adtler#143](https://github.com/Hochfrequenz/adtler/issues/143), and the "Public Repository — No Internal Data" section of `CLAUDE.md`.

**Branch:** `chore/135-redact-internal-data`, from `main` @ `e499ecc`.

Revision 2, after an independent plan review: adds the VIT integration test (#143 names it), replaces Task 3's unworkable mutation check, adds the encoded and lower-case namespace forms, the `K000000`-style and malformed transport numbers, and further logon-ID and alias locations.

## Background

Measured on `e499ecc`:

| Category | Where | Count |
|---|---|---|
| Transport/task numbers on three distinct system IDs | 19 files, nearly all unit tests; non-test: comments in `adt/transport.go`, `adt/types.go` | 280 lines with the `K9` shape, plus `K000000`-style numbers at `adt/transport_ecc_parsing_test.go:178,180,410,412` and deliberately malformed ones at `adt/transport_e071_fallback_test.go:353-356` |
| SAP logon IDs of colleagues and a technical user | `adt/errorkind_transport_test.go:16,17,21`, `adt/errorproperties_test.go:50,177`, `adt/transport.go:1026` (comment) | 4 IDs |
| Object name from a live capture (class + method) | `adt/transport_e071_fallback_test.go:251,273` | 1 |
| Own and partner namespace, raw upper-case form | `adt/abapsql.go:10`, `adt/abapsql_test.go`, `adt/classrun_test.go`, `adt/client.go:791`, `adt/custexport/writer_test.go`, `adt/errorkind_transport_test.go`, `adt/repository_test.go`, two docs | ~30 |
| Same namespaces, URL-encoded or lower-case | `adt/repository_test.go:181,193,205,217,229`, `adt/client.go:793`, `adt/classrun.go:53`, `adt/namespace_path_internal_test.go:27-41`, `adt/vit_integration_test.go:25-45`, docs (`docs/superpowers/plans/2026-07-23-classrun-endpoint.md:200,293,320`, `docs/superpowers/specs/2026-07-22-classrun-endpoint-design.md:58-59`) | ~20 |
| Real objects pinned in integration tests | `adt/vit_integration_test.go` (five objects in the own namespace), `adt/custexport/export_integration_test.go` (three namespaced tables of installed add-ons), `adt/object_integration_test.go:79` (a real transport layer) | 9 |
| System aliases and derived values used as prose or fixture | `adt/classrun_integration_test.go`, `adt/transport_syst_cust_investigation_integration_test.go`, `adt/completions_namespace_integration_test.go:42`, `adt/discovery_accept_header_test.go:13-21`, `adt/source_test.go:692` (includes a proxy variant), `adt/repository_test.go:185-233` (descriptions), `adt/custexport/writer_test.go:232`, `adt/client_test.go:185` (cookie name with alias and client), `adt/object_test.go:164` (transport layer built from an alias), `adt/lockmap_test.go:130` (a system ID used as system key), three docs under `docs/superpowers/` | ~65 |

Rechecked and found clean (no action): #135 category 3 (project names in transport descriptions — the remaining descriptions are generic), and the files #135 lists under `adt/adtxml/` and `adt/custexport/export.go` (cleaned earlier by #142).

Out of scope, by decision:

- **Git history is not rewritten.** A force push on a public repository breaks every clone and fork, and the values have been public since they were committed. #135 leaves this to the maintainers; the decision is recorded in the PR.
- **`README.md`'s `SAP_INTEGRATION_SYSTEMS=…` example** keeps its two lower-case keys: it is the documented config value, the exception `CLAUDE.md` allows.
- **Issue and PR texts on GitHub** are not part of this branch.

This plan itself must not quote any of the values. It refers to them by location and by the grep that finds them; do the same in commit messages, comments and the PR.

## Global Constraints

- No new dependency.
- Nothing committed may contain a real transport/task number, system ID, system alias (also not inside a longer identifier such as a cookie name or a transport layer), logon ID, host name, client number tied to a system, customer or partner namespace (raw, URL-encoded or lower-case), or a real object name from a live capture. That includes commit messages, code comments and docs. Reports in the local SDD workspace are not published, but cite file:line there instead of copying values.
- Replacement values (use exactly these):
  - System IDs in transport/task numbers: the three IDs that `git grep -h -o -E '\b[A-Z][A-Z0-9]{2}K9[0-9]{5}\b' | cut -c1-3 | sort -u` prints are mapped, in that sorted order, to `AAA`, `BBB`, `CCC`. Only the three-character prefix changes; the rest of the number stays, so numbers stay distinct and keep their shape. The same mapping applies to every other occurrence of those IDs glued to `K` (`K000000`-style, malformed test inputs).
  - A system ID used as a system key in a unit test: `sysA` / `sysB` (as `CLAUDE.md` already prescribes).
  - In an SAP cookie name (`sap-XSRF_<SID>_<client>`): `AAA` and `100`.
  - Logon IDs: `USERA`, `USERB`, `USERC`, `USERD` (one per distinct real ID, consistently).
  - Own namespace → `/ABC/` (`%2fabc%2f`, `/abc/` in the other forms); partner namespace → `/XYZ/` (`%2fxyz%2f`, `/xyz/`).
  - Transport layer derived from an alias, in unit tests → `ZLAY`.
  - System aliases in prose → "the ECC system" / "the S/4 system" (and "a second ECC entry" for a proxy variant); in a command line inside a comment → `<ecc-key>` / `<s4-key>`.
  - Real class name from a capture → `ZCL_EXAMPLE_CLASS`, method → `GET_DATA`, keeping the 30-column padding of the object-name field.
- Tests that parse a replaced value must keep asserting the same thing. Replace a value consistently within a test file.
- Integration tests that need a real object discover it at runtime, `t.Skip` with a count-only message when the system has none, and log types and counts, never names.
- Comments and commit messages: neutral, English, factual.
- After every task: `go test ./...`, `go build -tags integration ./...`, `go vet -tags integration ./...` and `golangci-lint run --enable dupl,goconst,gocyclo ./...` pass, and the changed Go files are gofmt-clean. This checkout uses `core.autocrlf=true`, so check formatting on LF content — this must print nothing: `for f in $(git diff --name-only origin/main -- '*.go'); do tr -d '\r' < "$f" | gofmt -d; done`. CRLF-only gofmt findings from golangci-lint on untouched files are expected locally.
- In Git Bash, prefix any grep whose pattern starts with `/` with `MSYS_NO_PATHCONV=1`; otherwise the pattern is rewritten into a Windows path and silently matches nothing.
- Integration runs only where a task says so, always with `-run` and `-count=1`. Never run `adt/object_integration_test.go`'s package-creating tests live: adtler cannot delete a package (#150).
- Commit messages end with a `Co-Authored-By:` trailer naming the model that wrote the commit.

## Review Focus

1. **A replaced value inside an assertion that silently stops testing anything**, e.g. a regex that extracted the request number from a CTS message. Pinned in Task 1 Step 5 (mutation check on the CTS-conflict tests).
2. **Padding-sensitive names**: the E071 `OBJ_NAME` field (class name padded to 30 columns, then the method) and class-pool include names (`<class>` padded with `=` to 30 characters, then `CCIMP`). Pinned in Task 1 Step 4 and Task 2 Step 2.
3. **A discovered fixture that does not exercise the code path the test is about**: pinned by the see-it-fail steps in Task 3 and Task 4.
4. **The CI check printing what it found**: it must report file, line and kind only, never the matched value, because the CI log is public. Pinned in Task 5 (`TestFormat_OmitsValue`).
5. **The CI check flagging legitimate upper-case path segments** as namespaces. Handled by the allowlist built from the tree scan in Task 5, which lists every allowlisted prefix in the report for review.

---

### Task 1: Transport numbers, logon IDs, captured object name

**Files:**
- Modify: the 19 files that `git grep -l -E '\b[A-Z][A-Z0-9]{2}K9[0-9]{5}\b'` lists
- Modify: `adt/transport_ecc_parsing_test.go`, `adt/transport_e071_fallback_test.go` (other number forms, object name)
- Modify: `adt/errorkind_transport_test.go`, `adt/errorproperties_test.go`, `adt/transport.go` (logon IDs)
- Modify: `adt/lockmap_test.go`, `adt/client_test.go` (system ID as key, cookie name)

**Interfaces:**
- Produces: placeholder system IDs `AAA`, `BBB`, `CCC` — Task 5's allowlist relies on exactly these.

- [ ] **Step 1: Map the system IDs with a one-off script (not committed)**

Write the script to the scratchpad, not the repository. It replaces, in every tracked text file, each of the three real system IDs (determined as in Global Constraints) wherever it is immediately followed by `K` and a digit — `\b(<sid1>|<sid2>|<sid3>)(K[0-9])` → placeholder + the captured rest. This covers the `K9…` numbers, the `K000000`-style numbers and the malformed test inputs in one pass. Preserve each file's line endings (read and write bytes; do not normalise CRLF).

- [ ] **Step 2: Remaining standalone system IDs**

Grep for each real system ID followed by `K` (`git grep -n -E '\b<sid>K'`) — must be empty now — and then as a whole word (`git grep -n -w <sid>`). Handle each word hit by hand: a comment naming the fixture prefix (e.g. `adt/transport_ecc_test.go:20`) gets the placeholder and adjusted wording; `adt/lockmap_test.go:130` uses a system ID as system key → `sysA`; a synthetic owner name or an unrelated word stays — name each such location in your report. Replace the cookie name at `adt/client_test.go:185` with `sap-XSRF_AAA_100` and check the test still asserts what it did.

- [ ] **Step 3: Logon IDs**

Replace the logon IDs in the CTS messages at `adt/errorkind_transport_test.go:16,17,21`, `adt/errorproperties_test.go:50,177` and in the E071 audit-string example in the comment at `adt/transport.go:1026` with `USERA`…`USERD`, one per distinct ID. Then search for more: `git grep -n -E 'of user [A-Z]|von Benutzer [A-Z]|User [A-Z][A-Z0-9_-]+ is currently|[0-9]{8} [0-9]{6} [A-Z]'` across the tree and handle every hit that is not already a placeholder (`X`, `TESTUSER<n>`, `USERA..D`).

- [ ] **Step 4: Captured object name**

In `adt/transport_e071_fallback_test.go:251,273` replace class and method in the padded `OBJ_NAME` value with `ZCL_EXAMPLE_CLASS` and `GET_DATA`. The class part stays padded with spaces to column 30, exactly like the original. Check the surrounding test and comments for other references to the same name.

- [ ] **Step 5: Verify the CTS-conflict tests still guard their behaviour**

Run `go test ./adt/ -run 'ErrorKind|Transport|Rollback|E071|RemoveObject|Release|LockKey|CSRF|Cookie' -v` — PASS. Then, as a mutation check, temporarily change one replaced request number in a "locked in request" message so it loses its shape (drop the `9`), run the same tests and confirm at least one fails (e.g. the `LockingTransport` assertions); restore.

- [ ] **Step 6: Full checks and commit**

Global Constraints checks, plus: `git grep -E '\b[A-Z][A-Z0-9]{2}K[0-9]{6}\b' | grep -v -E '\b(AAA|BBB|CCC)K'` lists only SAP's public demo system ID used in `adt/registry_test.go` (and nothing else).

```
chore(#135): replace transport numbers, logon IDs and a captured object name with placeholders

Transport and task numbers in fixtures and comments were built on the
system IDs of internal systems. They now use the placeholder system IDs
AAA, BBB and CCC with the rest of each number unchanged, so every number
keeps its shape and stays distinct. Logon IDs in captured CTS messages,
a cookie name and a lock-key system ID derived from internal systems,
and an object name from a live E071 capture are replaced with synthetic
values of the same shape.
```

---

### Task 2: Namespaces and system aliases in unit tests, code comments and docs

**Files:**
- Modify: `adt/abapsql.go`, `adt/abapsql_test.go`, `adt/classrun.go`, `adt/classrun_test.go`, `adt/client.go`, `adt/custexport/writer_test.go`, `adt/errorkind_transport_test.go`, `adt/repository_test.go`, `adt/namespace_path_internal_test.go`, `adt/object_test.go`
- Modify (prose only): `adt/classrun_integration_test.go`, `adt/transport_syst_cust_investigation_integration_test.go`, `adt/completions_namespace_integration_test.go`, `adt/discovery_accept_header_test.go`, `adt/source_test.go`
- Modify: `docs/superpowers/plans/2026-07-23-classrun-endpoint.md`, `docs/superpowers/specs/2026-07-22-classrun-endpoint-design.md`, `docs/superpowers/specs/2026-07-27-issue-106-classrun-defect1-design.md`

**Interfaces:**
- Produces: placeholder namespaces `/ABC/` and `/XYZ/` (and their encoded and lower-case forms) — Task 5's allowlist relies on exactly these.

- [ ] **Step 1: Namespaces, all three forms**

Find hits with:
- raw: `MSYS_NO_PATHCONV=1 git grep -n -E '/[A-Z][A-Z0-9]{1,9}/[A-Z0-9_]'`
- encoded: `git grep -n -i -E '%2f[a-z][a-z0-9]{1,9}%2f'`
- lower-case: review `MSYS_NO_PATHCONV=1 git grep -h -o -E '/[a-z][a-z0-9]{1,9}/' | sort | uniq -c` by eye for the two namespaces (most hits are ordinary URL path segments).

Replace the own namespace with `/ABC/` and the partner namespace with `/XYZ/` in every form, in all files of this task. `adt/vit_integration_test.go` and `adt/custexport/export_integration_test.go` are Tasks 3 and 4 — leave them. `adt/namespace_path_internal_test.go:27-41` asserts the encoded output of `encodeNamespacePath`; replace input and expected output consistently. Leave SAP's own upper-case path segments and existing placeholders alone, and list every prefix you left in the report.

- [ ] **Step 2: Names derived from a namespaced name**

Keep derived values consistent:
- `adt/custexport/writer_test.go:232`: the JSON file name is derived from the table name (`/` → `#`).
- `adt/errorkind_transport_test.go:16-21`: the class-pool include name is the class name padded with `=` to 30 characters, followed by `CCIMP`; rebuild it from the new class name.
- `adt/repository_test.go:181-236`: the object names (some hash-like) become `/ABC/` names of the same length and character class; the encoded URIs at `:181,193,205,217,229` follow them.

- [ ] **Step 3: System aliases**

Replace aliases used as prose with "the ECC system" / "the S/4 system" (a proxy variant: "a second ECC entry"): `adt/classrun_integration_test.go:32-33,138,141,182`, `adt/transport_syst_cust_investigation_integration_test.go:14,28` and its command line at `:30` (→ `SAP_INTEGRATION_SYSTEMS=<s4-key>`), `adt/completions_namespace_integration_test.go:42`, `adt/discovery_accept_header_test.go:13-21`, `adt/source_test.go:692`, the description texts at `adt/repository_test.go:185-233` (→ "Example …"), and the three docs. The transport layer built from an alias at `adt/object_test.go:164` becomes `ZLAY`. Find any remainder with `git grep -n -i -E '<alias1>|<alias2>'` (the two keys of the local `systems.json`, without `-w`, so that hits inside identifiers are found). The only allowed remaining hits: the config-value examples in `README.md`, and `adt/object_integration_test.go:79` (Task 4).

- [ ] **Step 4: Full checks and commit**

Global Constraints checks; `go test ./adt/... -run 'SQL|Classrun|RunClass|Repository|ErrorKind|Writer|Export|Namespace|CreatePackage|Discovery|Source'` passes.

```
chore(#143): replace namespaced object names and system aliases with placeholders

Unit-test fixtures, code comments and design docs named objects in the
company's and a partner's registered namespace, in raw, URL-encoded and
lower-case form, and used internal system aliases as prose or inside
fixture values. Namespaced names now use /ABC/ and /XYZ/ with derived
names (include names, encoded URIs, export file names) rebuilt from them,
and systems are named by type.
```

---

### Task 3: Customizing-export integration tests discover their tables

**Files:**
- Modify: `adt/custexport/export_integration_test.go` (`TestExportCustomizing_IncludeTables`, `TestExportCustomizing_LongKeyPagination`, one discovery helper)

**Interfaces:**
- Consumes: `adt.Client.RunQuery(ctx, sql string, maxRows int) (*adt.QueryResult, error)` (`adt/client.go:164`; `QueryResult.Rows [][]string`), `adt.BuildExportSQL(table string, allKeys, paginateKeys, lastValues []string) (string, error)` and `adt.FilterNonMandtKeys` (`adt/abapsql.go:27,88`), `custexport.RunExport`, the file's existing `newClient(t)` (single system from `SAP_INTEGRATION_HOST`/`_USER`/`_PASSWORD`/`_CLIENT`).
- Produces: `discoverTables(t *testing.T, client adt.Client, sql string, max int) []string` — runs a discovery query, returns the first column of up to `max` rows.

- [ ] **Step 1: Read both tests and the code they exercise**

Read the two tests and `adt/custexport/export.go`: `fetchTableKeys` (`:153`, reads key fields only, `KEYFLAG = 'X'`), the key-shortening loop (`:200-210`), and how empty tables are counted (`EmptyTables`, `:390`). Since #183, `RunQuery` wraps long SQL lines (`adt/query.go:43`, `adt/query_wrap.go`), so the key-shortening may no longer be needed for the request to succeed.

- [ ] **Step 2: `TestExportCustomizing_IncludeTables`**

Discover up to two active, transparent customizing tables (DD02L `TABCLASS = 'TRANSP'`, `CONTFLAG` in `'C'`, `'G'`, `AS4LOCAL = 'A'`) that have a DD03L row with `FIELDNAME = '.INCLUDE'`, `KEYFLAG = 'X'` and `AS4LOCAL = 'A'` — a key-level include is what `fetchTableKeys` sees and what the original fix was about. Keep candidates with at least 1 and fewer than 50 000 rows (`SELECT COUNT(*)` each). Write the SQL in the ABAP SQL the data preview accepts; establish the syntax live, not by guessing. If none qualifies, `t.Skip("no customizing table with a key-level .INCLUDE found")`. Run the export as before and assert `summary.ExportedTables == len(tables)` and `len(summary.Errors) == 0` (messages carry counts, not names).

- [ ] **Step 3: `TestExportCustomizing_LongKeyPagination`**

Discover one active, transparent customizing table with at least four key fields excluding the client field and `.INCLUDE` rows, and a row count above the test's page size (1000) and below 200 000. Among candidates, keep one where pagination SQL would exceed the export's length limit: fetch one row of the candidate's key fields, call `adt.BuildExportSQL(table, keys, adt.FilterNonMandtKeys(keys), <values of that row>)` and keep the candidate if the result is longer than the export's `maxSQLLength`. Skip with a count-only message if none qualifies.

Replace the current `rowCount > 1000` check by the stronger one the test is really about: the SQLite row count equals the source `SELECT COUNT(*)`. If that fails live, report it as a finding (shortened pagination keys can skip rows that share a key prefix across a page boundary) — do not loosen the check; the controller decides.

- [ ] **Step 4: Run live on both systems, and see each test fail**

The tests read one system from environment variables. For each of the two entries in `~/.config/sap-mcp/systems.json`, export `SAP_INTEGRATION_HOST`, `_USER`, `_PASSWORD`, `_CLIENT` from that entry in the shell (never echo them, never write them into the repository) and run:

`go test -tags=integration -count=1 -v -run 'TestExportCustomizing_(IncludeTables|LongKeyPagination)$' ./adt/custexport/`

Expected: PASS or a deliberate count-only skip per system. Then, on one system where each test passed:
- Include test: temporarily revert the `.INCLUDE` handling in `fetchTableKeys` (stop filtering `.INCLUDE` rows out of the key list) and confirm the include test fails; restore.
- Long-key test: temporarily make the export stop after its first page and confirm the long-key test fails on the row-count equality; restore.

Report per system: PASS/SKIP, candidate counts, whether the long-key table triggered the key reduction (the export logs `reduced pagination keys`).

- [ ] **Step 5: Full checks and commit**

```
test(#143): discover customizing-export fixtures at runtime instead of naming them

TestExportCustomizing_IncludeTables and TestExportCustomizing_LongKeyPagination
pinned namespaced tables of installed add-ons. They now query DD02L and
DD03L for a customizing table with a key-level .INCLUDE, and for one
whose pagination SQL exceeds the export's length limit, and skip when
the system has none. The include test now asserts that every table
exported without errors, and the long-key test that every source row
arrived, instead of only logging counts.
```

---

### Task 4: VIT and package-creation integration tests discover their objects

**Files:**
- Modify: `adt/vit_integration_test.go`
- Modify: `adt/object_integration_test.go` (transport layer at `:79`)

**Interfaces:**
- Consumes: `adt.Client.RunQuery`, `GetObjectInfo`, `eachSystem(t)` (`adt/integration_helpers_test.go`), `*adt.ADTError` (`errors.As`) with its `StatusCode`.

- [ ] **Step 1: VIT objects**

The test pins five objects in the own namespace, one per TADIR type `UIAC`, `UIAD`, `ADVC`, `LRCC`, `WDCC`, with VIT URI type segments `uiac`, `uiad`, `advclrp`, `lrcclrp`, `wdcc`. For each type, discover one object per system: `SELECT obj_name FROM tadir WHERE pgmid = 'R3TR' AND object = '<type>' AND delflag = ' '` (one row; establish the syntax live). Build the URI as `/sap/bc/adt/vit/wb/object_type/<segment>/object_name/<name>` with every `/` in the name percent-encoded as `%2f`. Skip the sub-test when the system has no object of that type. Log the type and whether name/type came back non-empty — never the name, description or package.

Error handling: today the test skips on any error (`:59-62`), so it cannot catch the 406 regression it was written for (#72, `readWithAcceptFallback` in `adt/repository.go`). Skip only when `errors.As` yields an `*adt.ADTError` with status 404; fail on anything else.

- [ ] **Step 2: Transport layer for `CreatePackage`**

`adt/object_integration_test.go:79` passes a real transport layer derived from a system alias. Discover it instead: the transport layer of the existing test package, `SELECT pdevclass FROM tdevc WHERE devclass = 'Z_ADT_MCP_TEST'`; skip if empty. Do NOT run this test live (it creates a package, which adtler cannot delete, #150). Validate the discovery query on both systems with a throwaway program in the scratchpad that only calls `RunQuery`, and report the row count per system.

- [ ] **Step 3: Run live on both systems, and see the VIT test fail**

`SAP_INTEGRATION_SYSTEM=__none__ SAP_INTEGRATION_SYSTEMS=<ecc-key>,<s4-key> go test -tags=integration -count=1 -v -run 'TestGetObjectInfo_VIT' ./adt/` (use the test's real name; `SAP_INTEGRATION_SYSTEM=__none__` keeps the shared `TestMain` from creating a transport request). Expected: PASS for the types the system has, count-only skips for the rest. Then temporarily make `readWithAcceptFallback` skip its `*/*` retry, rerun, and confirm at least one sub-test fails with 406 instead of skipping; restore. Report per system and type: PASS/SKIP/FAIL.

- [ ] **Step 4: Full checks and commit**

```
test(#143): discover VIT objects and the transport layer at runtime

The VIT integration test pinned five objects in the company's namespace
and skipped on any error, so it could not catch the 406 regression it
guards. It now finds one object per TADIR type through the data preview,
skips only on 404, and logs types instead of names. The package-creation
test reads the transport layer of the public test package instead of
naming one.
```

---

### Task 5: CI check against reintroduced identifiers

**Files:**
- Create: `tools/datacheck/main.go`, `tools/datacheck/main_test.go`
- Create: `.github/workflows/internal-data.yml`
- Modify: `CLAUDE.md` ("Before pushing" and "Exceptions" paragraphs)

**Interfaces:**
- Consumes: placeholder system IDs `AAA`, `BBB`, `CCC` (Task 1) and namespaces `ABC`, `XYZ` (Task 2).

- [ ] **Step 1: Write the failing tests**

`tools/datacheck/main_test.go` (package `main`). Build every non-placeholder sample by concatenation so this file never contains a flagged literal:

```go
package main

import (
	"strings"
	"testing"
)

func TestScan(t *testing.T) {
	foreignNumber := "QQQ" + "K9" + "00001"
	foreignNS := "/" + "QQQ" + "/CL_THING"
	foreignEncoded := "%2f" + "qqq" + "%2fcl_thing"
	cases := []struct {
		name  string
		line  string
		kinds int
	}{
		{"placeholder transport", "request AAAK900001 of user USERA", 0},
		{"foreign transport", "request " + foreignNumber, 1},
		{"foreign K000000 number", "QQQ" + "K000000", 1},
		{"placeholder namespace", "class /ABC/CL_EXAMPLE and /XYZ/CL_OTHER", 0},
		{"foreign namespace", "class " + foreignNS, 1},
		{"placeholder encoded", "/sap/bc/adt/oo/classes/%2fabc%2fcl_example", 0},
		{"foreign encoded", "/sap/bc/adt/oo/classes/" + foreignEncoded, 1},
		{"lower-case path", "/sap/bc/adt/oo/classes/zcl_test", 0},
		{"two in one line", foreignNumber + " " + foreignNS, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(scan([]byte(c.line))); got != c.kinds {
				t.Errorf("scan(%q) found %d, want %d", c.line, got, c.kinds)
			}
		})
	}
}

// TestFormat_OmitsValue pins that a printed finding names file, line and
// kind but never the matched value: the CI log is public, so printing the
// value would republish it.
func TestFormat_OmitsValue(t *testing.T) {
	sid := "QQQ"
	findings := scan([]byte("x\nrequest " + sid + "K9" + "00001\n"))
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want one", findings)
	}
	out := format("adt/x_test.go", findings[0])
	if !strings.HasPrefix(out, "adt/x_test.go:2: ") {
		t.Errorf("format = %q, want file:line prefix", out)
	}
	if strings.Contains(out, sid) {
		t.Errorf("format = %q contains the matched system ID", out)
	}
}
```

Run `go test ./tools/datacheck/` — expected: compile failure, `scan` undefined.

- [ ] **Step 2: Implement**

`tools/datacheck/main.go`:

```go
// Command datacheck scans the files named on the command line for
// identifiers of internal SAP systems that must not be published in this
// repository: transport and task numbers on a non-placeholder system ID,
// and names in a /X/ namespace outside an allowlist, raw or URL-encoded.
// It prints file, line and kind of each finding, never the matched value,
// so the public CI log does not republish what it found. It cannot detect
// host names, logon IDs, system aliases, or lower-case namespace forms;
// see "Before pushing" in CLAUDE.md.
//
// Usage: git ls-files -z | xargs -0 go run ./tools/datacheck
package main

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
)

var (
	transportRe = regexp.MustCompile(`\b([A-Z][A-Z0-9]{2})K[0-9]{6}\b`)
	namespaceRe = regexp.MustCompile(`/([A-Z][A-Z0-9]{1,9})/[A-Z0-9_]`)
	encodedRe   = regexp.MustCompile(`(?i)%2f([a-z][a-z0-9]{1,9})%2f`)
)

// placeholderSIDs are system IDs that identify no internal system: the
// synthetic IDs of test fixtures.
var placeholderSIDs = map[string]bool{"AAA": true, "BBB": true, "CCC": true}

// allowedPrefixes are /X/ prefixes (compared upper-case) that identify
// nobody: the placeholder namespaces, and upper-case segments that only
// look like a namespace.
var allowedPrefixes = map[string]bool{
	"ABC": true, // placeholder namespace
	"XYZ": true, // placeholder namespace
}

type finding struct {
	line int
	kind string
}

func scan(content []byte) []finding {
	var out []finding
	for i, line := range strings.Split(string(content), "\n") {
		for _, m := range transportRe.FindAllStringSubmatch(line, -1) {
			if !placeholderSIDs[m[1]] {
				out = append(out, finding{i + 1, "transport or task number on a non-placeholder system ID"})
			}
		}
		for _, re := range []*regexp.Regexp{namespaceRe, encodedRe} {
			for _, m := range re.FindAllStringSubmatch(line, -1) {
				if !allowedPrefixes[strings.ToUpper(m[1])] {
					out = append(out, finding{i + 1, "name in a non-allowlisted /X/ namespace"})
				}
			}
		}
	}
	return out
}

// format renders a finding for the log: file, line and kind, never the value.
func format(name string, f finding) string {
	return fmt.Sprintf("%s:%d: %s", name, f.line, f.kind)
}

func main() {
	failed := false
	for _, name := range os.Args[1:] {
		content, err := os.ReadFile(name)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
			failed = true
			continue
		}
		if bytes.IndexByte(content, 0) >= 0 {
			continue // binary file
		}
		for _, f := range scan(content) {
			fmt.Println(format(name, f))
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
```

Run `go test ./tools/datacheck/` — expected: PASS.

- [ ] **Step 3: Build the allowlists from the tree**

Run `git ls-files -z | xargs -0 go run ./tools/datacheck`. With Tasks 1–4 done, every remaining finding is either a leftover (fix it in the file, in this task's commit, and say which) or a value that identifies nobody: an upper-case ADT type or path segment that is not a namespace (the dry run during plan review expects about nine, e.g. object-type and path segments), a placeholder namespace already used in docs, or SAP's public demo system ID in `adt/registry_test.go`. Add only those, each with a short comment saying what it is, and list them in the report. Re-run until the command exits 0. Never allowlist a real namespace or a real system ID.

- [ ] **Step 4: Workflow**

`.github/workflows/internal-data.yml`:

```yaml
name: internal-data
on: [push, pull_request]
jobs:
  datacheck:
    runs-on: ubuntu-latest
    name: datacheck
    steps:
      - name: Install Go
        uses: actions/setup-go@v7
        with:
          go-version: "1.27.x"
      - uses: actions/checkout@v7
      - name: Scan tracked files for internal SAP identifiers
        run: git ls-files -z | xargs -0 go run ./tools/datacheck
```

- [ ] **Step 5: CLAUDE.md**

In "### Before pushing", replace the first paragraph with:

```markdown
CI runs `tools/datacheck` over every tracked file. It fails on a transport or task number whose
system ID is not a placeholder (`AAA`, `BBB`, `CCC`), and on a `/X/`-prefixed name, raw or
URL-encoded, whose prefix is not allowlisted in `tools/datacheck/main.go`. Run it before pushing:
`git ls-files -z | xargs -0 go run ./tools/datacheck`. It prints file and line, never the value.
It cannot see host names, logon IDs, system aliases (also inside identifiers), lower-case namespace
forms, or customer object names outside a namespace — grep the diff for those yourself, by shape
rather than by value, so the guard does not leak them.
```

In "### Exceptions", extend the sentence about unit-test fixture strings: after "use `sysA` / `sysB` for system keys and generic placeholders for object names" add ", `AAA`/`BBB`/`CCC` as the system ID of transport and task numbers, `USERA`… for logon IDs, and `/ABC/` / `/XYZ/` as namespaces".

- [ ] **Step 6: Full checks and commit**

Global Constraints checks, plus the datacheck command from Step 3 exits 0, and `GOOS=js GOARCH=wasm go build ./tools/...` succeeds (the wasm workflow builds every package except `adt/custexport`).

```
ci(#135): fail on transport numbers and namespaced names outside the placeholders

tools/datacheck scans tracked files for transport or task numbers whose
system ID is not a placeholder, and for /X/-prefixed names, raw or
URL-encoded, outside an allowlist. It reports file and line only, so the
public CI log does not repeat what it found. A workflow runs it on every
push and pull request, and CLAUDE.md documents it under "Before pushing".
```

---

## Finish (controller, after the final review)

- PR title `chore(#135, #143): redact internal environment data and guard against its return`; body `Closes #135`, `Closes #143`; Hochfrequenz/aibap.mcp#523 as `Related:` only.
- The PR body states: history is not rewritten, and why; what `datacheck` cannot detect; the live results of Tasks 3 and 4 per system type; any export finding from Task 3 Step 3.
- Label `needs:integration-test`.
- Copilot review, then a separate integration-test run with a result comment.

---

### Task 6: Export pagination loses rows after key reduction (found by Task 3)

Revision 3. Task 3's row-count check found that the customizing export silently drops rows: on the S/4 system a table with 13 non-client key fields and 33 171 rows arrived in SQLite with 6 000. Cause: when the keyset-pagination SQL exceeds `maxSQLLength` (250), `fetchTableData` (`adt/custexport/export.go:200-218`) drops trailing pagination keys and continues with a key *prefix*; every row that shares the last row's prefix and sorts after it is skipped at each page boundary. The limit predates #183: it was measured as the data preview truncating a *line* after 255 characters (`export.go:63-66`), which `RunQuery` now handles by re-wrapping long lines (`adt/query.go:43`, `adt/query_wrap.go`). The reduction is therefore unnecessary as long as the data preview accepts the full-key SQL as a whole.

This task also carries the open findings of Task 3's review, since they touch the same test.

**Files:**
- Modify: `adt/custexport/export.go` (`fetchTableData`, constants)
- Test: `adt/custexport/export_test.go` (unit), `adt/custexport/export_integration_test.go` (long-key test)

- [ ] **Step 1: Measure before changing anything**

On the S/4 system, build the full-key pagination SQL for the long-key table the test discovers (all non-client keys, values from a real row, via `adt.BuildExportSQL`) and run it through `RunQuery` with a throwaway program outside the repository. Record: total SQL length, whether SAP accepts it, and whether it returns the expected next page (compare with an ordered `SELECT` of the same keys). If SAP rejects it for its total length, STOP and report NEEDS_CONTEXT with the measured limit — the fix then needs a different design.

- [ ] **Step 2: Failing unit test**

In `adt/custexport/export_test.go`, with the existing mock-client pattern, export a table whose keys make the pagination SQL longer than 250 characters over at least three pages, where page boundaries fall inside groups of rows sharing the first key. Assert that every row arrives (count and content). Run it and watch it fail on the current code for the stated reason (rows missing).

- [ ] **Step 3: Fix**

Remove the key reduction: paginate on all non-client keys always. Remove `maxSQLLength` and the two log lines that print the table name (`export.go:212`, `:216`) together with the reduction loop; a SQL that SAP rejects surfaces as the `RunQuery` error it already is. Update the comment at `export.go:63-66` to state why no length limit applies any more (#183).

- [ ] **Step 4: Long-key integration test, plus the Task 3 review findings**

- Selection criterion: keep only candidates whose full-key pagination SQL exceeds 255 characters (a line `RunQuery` must wrap), so the test still exercises long pagination SQL; state in a comment that 255 is the data preview's line limit from #183. This replaces the duplicated `exportSQLLimit = 250`.
- Bound discovery: wrap the candidate search in `context.WithTimeout` (3 minutes) and skip with a count-only message on expiry; fetch each candidate's key fields once.
- `paginationSQLTooLong` (or its replacement): a `RunQuery` error is a `t.Fatalf`, only an empty table counts as "not long".
- `sourceRowCount` / `tableKeyFields`: fixed `t.Fatalf` messages without the SAP error text, which may name the table.

- [ ] **Step 5: Run live on both systems and see it fail without the fix**

Both custexport integration tests on both systems: PASS (the long-key test now on S/4 too, all rows). Then re-insert the key reduction temporarily and confirm the long-key test fails on S/4 again; restore. Also confirm the unit test from Step 2 fails with the reduction restored.

- [ ] **Step 6: Full checks and commit**

```
fix(custexport): paginate on all key fields instead of a key prefix

When the keyset-pagination SQL grew past 250 characters, the export
dropped trailing pagination keys and paged on a key prefix, skipping
every row that shared the prefix of a page's last row. On a table with
13 key fields this lost most of the table. The limit was the data
preview's per-line truncation, which RunQuery handles since #183, so
the reduction is removed and pagination always uses the full key.
```
