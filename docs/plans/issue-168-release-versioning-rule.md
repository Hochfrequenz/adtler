# Issue 168: write down the release versioning rule

Goal: document how to choose the next version number, as a "Releases" section in `CLAUDE.md`, and point to it from the README note on the release sweep (the issue calls that note "existing" in `CLAUDE.md`, but it lives in `README.md`, under "Running the heavy regression sweep").

## Task 1: Releases section in CLAUDE.md (documentation only)

No failing test first: the task changes only prose documentation that no compiler, test runner or program parses. Existing tests that pin documentation wording: none found by searching the tests for `CLAUDE.md` and `README.md`.

Content, in the language of the repository (English):

- The rule: minor when the exported API surface grows a new symbol or when consumers break; patch for everything else, including behaviour changes and features that add no exported symbol.
- The deciding question: can this change alter the outcome of a call that previously succeeded? Yes: a break, so minor, even if nothing was added. No: patch (v0.5.4 as the example: a 406 retry and an error where the old code returned a zero-valued struct with a nil error).
- The table of the four minor bumps of v0.2.0 to v0.5.0 and their causes (checked against `git log` between the tags).
- A choice on record: the rule is stricter than the semver 0.x norm; it is kept on purpose, the 1.x rule applied early; a tag cannot be re-cut because the Go module proxy caches it.
- Place it after "Labels (workflow-relevant)", before "Public Repository".

## Task 2: pointer in README.md (documentation only)

One sentence under "Running the heavy regression sweep": the version number is chosen by the rule in the "Releases" section of `CLAUDE.md`.

## Verification

- `git diff` shows only the three prose files (plan, `CLAUDE.md`, `README.md`).
- Datacheck gate (`git ls-files -z | xargs -0 go run ./tools/datacheck`) and the full gate pass; nothing in the text names an internal host, system alias or object.
