# Task: Fix glob expansion in cat/ls; add ls --blame

**Started:** 2026-08-19 22:55:00
**Ended:** 2026-08-19 23:10:58
**Strategy:** Bug Fix (glob), Feature (TDD) (--blame)
**Status:** Completed
**Complexity:** Medium
**Used Models:** Sonnet 5
**Token usage (Estimated):** n/a

## Objective
Two requests from the human:
1. Bug: `gitfs ls -l main 'sc*'` failed with "file does not exist" — PATH
   arguments were resolved as literal paths only, never glob-expanded,
   even though the library already has a working `fsys.Glob`.
2. Feature: richer `ls -l` metadata — per-file owner/size/date and "the
   commit it came from", gated behind `--blame[=<limit>]` since it
   requires history search, with the constraint that it must return the
   listed commit or an older one, never something newer than REF even if
   the file changed since.

## Progress
- [x] Fixed glob expansion: new `expandGlobs` helper routes every PATH
      arg through `fsys.Glob` before use; a pattern with no metacharacters
      is just an existence check (backward compatible with literal paths)
- [x] `ls` also gained handling for a glob/path resolving to a plain file
      (previously only directories worked at all — ReadDir on a file
      errored); headers only print ahead of directory listings when
      there's more than one resolved target, matching real `ls`
- [x] Added `displayPath` so headers/names stay cwd-relative (what the
      user typed) rather than Glob's repo-root-relative match form
- [x] Clarified --blame design with human before implementing: per-file
      last-touch (not per-line blame), plain `-l` gains a date column
      unconditionally (REF's own commit date, free — already available
      via GitFS's uniform modTime), `--blame` adds date/author/short-SHA
      of the actual last-touching commit
- [x] Implemented via `git log --max-count=1 -- path`, bounded to
      `sha~limit..sha` when a limit is given, falling back to `sha`'s own
      commit info if nothing found in that window (handles both "no match
      in a short window" and "history shorter than limit" the same way)
- [x] `--blame` implies `-l`; invalid (non-numeric or negative) limits
      rejected with a clear error
- [x] Refactored `openFS` to return a `*target` struct (fsys, prefix,
      gitBin, repoPath, sha) instead of two bare returns, since --blame's
      git-log lookups need gitBin/repoPath/sha alongside the GitFS itself
- [x] 17 new integration tests (glob: directory/file/mixed/no-match
      matches, cat-with-glob; blame: finds last-touch, limit fallback,
      implies -l, rejects invalid limit; plus fixing 2 existing tests for
      the new date column). `make vet test`: 73/73 passing
- [x] README updated; `make install` refreshed the binary

## Obstacles
- **Issue:** First `--blame` flag design used `IntVar` with
  `NoOptDefVal="-1"` (a magic sentinel for "unbounded") so `--blame` alone
  vs `--blame=N` could be distinguished. Functionally correct, but the
  auto-generated `--help` text leaked the sentinel: `--blame int[=-1]`
  and `(default -1)`, confusing without reading the source.
  **Resolution:** Switched the flag itself to a `StringVar` with
  `NoOptDefVal="unbounded"`, translating to the internal `-1` sentinel
  only inside `RunE` (parsed via `strconv.Atoi`, validated non-negative).
  Help now reads `--blame string[="unbounded"]`, self-explanatory without
  digging into the code.
- **Issue:** An `Edit` call meant to insert a `return`+closing-brace
  ahead of a new `displayPath` helper (inserted mid-file, splitting
  `expandGlobs` from what followed it) matched only part of the original
  function body, leaving the *old* `return targets, nil` trailing
  dangling after `displayPath`'s own closing brace — a straightforward
  syntax error, caught immediately by `go build`.
  **Resolution:** Read the surrounding lines and removed the orphaned
  leftover text.

## Outcome
`gitfs cat`/`ls` PATH arguments now support `path.Match` globs
(`gitfs ls -l main 'sc*'`), matching real `ls` semantics for mixed
file/directory results. `gitfs ls -l` always shows a date column now;
`gitfs ls -l --blame[=LIMIT] REF PATH...` adds per-file last-touching
commit (date, author, short SHA), bounded and falling back to REF's own
commit when the bound is hit. All committed, tests green.

## Insights
`git log --max-count=1 <rev-or-range> -- <path>` is the right tool for
"which commit last touched this specific file" — much cheaper than real
`git blame`, and its cost can be bounded up front via a `sha~N..sha`
range rather than needing to interrupt/cap the walk after the fact.
