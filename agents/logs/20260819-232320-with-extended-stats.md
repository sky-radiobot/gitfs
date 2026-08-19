# Task: Add gitfs.WithExtendedStats, move ls --blame's logic into the library

**Started:** 2026-08-19 23:15:00
**Ended:** 2026-08-19 23:23:20
**Strategy:** Feature (TDD)
**Status:** Completed
**Complexity:** Medium
**Used Models:** Sonnet 5
**Token usage (Estimated):** n/a

## Objective
The human asked whether `io/fs`'s stat interface has a place for owner,
timestamp, and other extra information (yes: `FileInfo.Sys() any` is
exactly that escape hatch), and to expose the "last commit that touched
this file" lookup — until now implemented only inside `cmd/gitfs`'s `ls
--blame` — as a library-level `gitfs.WithExtendedStats(maxCommits)`
option, then refactor `ls` to use it instead of its own duplicate
git-log-shelling logic.

## Progress
- [x] Confirmed `Sys()` is the intended hook (already existed, returning
      `nil` unconditionally) — no interface change needed on `fs.FileInfo`
      itself
- [x] Added `backend.lastCommit(path, maxCommits) (commitInfo, error)` to
      the `backend` interface (backend.go), implemented independently in
      both backends per repo convention:
      - `backend_exec.go`: shells out to `git log --max-count=1`, bounded
        via a `sha~maxCommits..sha` range when maxCommits is
        non-negative, falling back to the pinned commit's own info
      - `backend_gogit.go`: walks first-parent history via `go-git`'s
        `repo.Log`, comparing tree-entry hashes at path between each
        commit and its first parent (documented as a first-parent
        simplification, not full merge-aware history simplification —
        the two backends can disagree on merge-heavy histories, though
        not on the linear histories gitfs's own tests use)
- [x] Added `gitfs.WithExtendedStats(maxCommits)` option and `ExtendedStat`
      type (`Commit`, `Author`, `AuthorEmail`, `Date`, `Err`); `fileInfo`
      gained `g *GitFS` + `path string` fields so `Sys()` can compute this
      lazily, only when actually called, not up front during ReadDir/Stat
- [x] `info()`'s call sites (Open/Stat/dirEntries) updated to pass the
      full repo-relative path through, needed for the lastCommit lookup
- [x] Refactored `cmd/gitfs/ls --blame`: removed the CLI's own
      `blameInfo`/`lastTouch`/`logOneCommit` (now redundant), `ls` passes
      `gitfs.WithExtendedStats(blameLimit)` to `openFS`/`gitfs.Open` and
      reads `info.Sys().(*gitfs.ExtendedStat)` instead; `openFS`/`target`
      simplified back down (no longer need to carry gitBin/repoPath/sha
      for the CLI's own git-log calls, since that responsibility moved
      into the library)
- [x] 2 new unit tests (`TestExtendedStats`, `TestExtendedStatsMaxCommitsFallback`)
      against both backends; `make vet test`: unit tests + 73/73 integration
      tests green
- [x] README/CLAUDE.md updated; `make install` refreshed the binary

## Obstacles
- **Issue:** First `execBackend.lastCommit` implementation's fallback step
  (when the bounded range found no match) called
  `logOneCommitPath(b.sha, p)` — still path-filtered, and unbounded. That
  defeats the whole point of "give up and report the pinned commit" (it
  instead did an unbounded search anyway, landing on some other, wrong
  commit far from the requested one). Caught immediately by the fallback
  unit test failing with a mismatched SHA; fixed by calling the
  no-pathspec `logOneCommit(b.sha)` instead, matching what
  `gogitBackend.lastCommit` already did correctly.
- **Issue (more subtle):** Both original implementations treated
  `maxCommits <= 0` as unbounded — but the CLI's `--blame=0` (a real,
  meaningful "search zero commits, always fall back" case, distinct from
  "no limit given at all") relies on 0 being a genuine bound, not folded
  into "unbounded". This surfaced as an integration-test failure
  (`test_ls_blame_limit_falls_back_to_ref_commit`, which uses
  `--blame=0`) once the CLI's own duplicate blame code (which correctly
  used `limit >= 0` for "bounded") was replaced by the library call.
  **Resolution:** Tightened both backends' — and the library's — contract
  to "negative means unbounded, 0 means only the pinned commit itself is
  examined" (changed `<= 0` to `< 0`/`>= 0` in the loop/range conditions),
  and updated the one unit test that had relied on the old `0 =
  unbounded` behavior to use `-1` instead. This is exactly the kind of
  boundary mismatch worth re-testing carefully after moving logic across
  a library/CLI boundary — the CLI's own hand-rolled version and the
  library's fresh implementation had silently disagreed on what "0"
  meant.

## Outcome
`gitfs.WithExtendedStats(maxCommits)` is now a first-class library option;
`fs.FileInfo.Sys()` returns `*gitfs.ExtendedStat` when it's set. `cmd/gitfs
ls --blame` is a thin wrapper over it (bounds, fallback semantics, and
both-backend agreement all verified). All committed, tests green.

## Insights
When moving logic that was working correctly at one layer (the CLI) down
into a library layer it now depends on, don't assume boundary semantics
(what does 0 mean? negative? unbounded?) carry over unchanged just because
the *behavior* was already tested at the old layer — re-verify the exact
edge values across the new boundary, since a silent renegotiation of "what
zero means" is easy to introduce and easy to miss until a specific
boundary-value test exercises it.
