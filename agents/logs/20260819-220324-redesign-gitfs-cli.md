# Task: Redesign gitfs CLI to drop explicit REPO arg, add ref resolution

**Started:** 2026-08-19 22:03:24
**Ended:** 2026-08-19 22:35:00
**Strategy:** Feature (TDD)
**Status:** Completed
**Complexity:** Medium
**Used Models:** Sonnet 5
**Token usage (Estimated):** n/a

## Objective
Redesign `cmd/gitfs` to:
- `gitfs REF cat PATH [PATH...]`
- `gitfs REF ls [-l] [PATH...]`
- REF may be a full commit SHA or anything `git` can resolve (branch, tag,
  short SHA, `HEAD`, ...); non-SHA refs are resolved via the `git` CLI,
  failing if resolution is not possible.
- The repo path is no longer a positional CLI arg; it is discovered from the
  current working directory the same way `git` itself does (works from bare
  repos and from any subdirectory of a working tree).
- `stat`/`glob` subcommands are dropped (confirmed with human).

## Progress
- [x] Clarified requirements with human (repo discovery from cwd, drop
      stat/glob, ls: plain names by default / -l for mode+size+name)
- [x] Rewrite `cmd/gitfs/main.go`
- [x] Rewrite `tests/integration/test_gitfs.sh` (cd into repo instead of
      passing REPO; drop stat/glob tests; add tests for multi-path cat/ls,
      -l, ref resolution, bare-repo discovery from cwd)
- [x] `make vet test` green (46/46 integration tests, unit tests pass)

## Obstacles
- **Issue:** Once REPO became implicit (discovered from cwd), a path given
  as `main.go` from within `src/app/` needs to resolve to
  `src/app/main.go` relative to the repo root, not fail as not-found.
  **Resolution:** Added a `prefix` computed via `git rev-parse
  --show-prefix` (git's own cwd-relative-to-root primitive) and
  `path.Join(prefix, p)` for every path arg to `cat`/`ls`. Bare repos have
  no working tree, so prefix is always "" there.
- **Issue:** First attempt computed the prefix manually with
  `filepath.Rel(repoPath, os.Getwd())`. Failed under `make integration-tests`
  because macOS's `/tmp` is a symlink to `/private/tmp`: `git
  rev-parse --absolute-git-dir` returns the resolved physical path while
  `os.Getwd()` returned the logical one, producing a bogus long `../../..`
  relative path.
  **Resolution:** Switched to `git rev-parse --show-prefix`, sidestepping
  path resolution entirely — git already tracks this internally for its own
  pathspec handling.
- **Issue:** First multi-path `cat` integration test's expected-output
  computation stripped the newline between the two concatenated files
  (each `$(...)` substitution trims its own trailing newline before
  concatenation), producing a false failure.
  **Resolution:** Captured both `git show` calls inside one `$(...)` so
  only the final trailing newline is stripped, matching real `cat`
  semantics.

## Outcome
`cmd/gitfs` now supports:
- `gitfs REF cat PATH [PATH...]`
- `gitfs REF ls [-l] [PATH...]`

REF may be a full 40-hex commit SHA (used as-is) or anything `git
rev-parse --verify REF^{commit}` can resolve (branch, tag, short SHA,
`HEAD`, ...); unresolvable refs fail with a clear error. The repo is
discovered from cwd like `git` itself (working-tree root or bare repo dir),
and paths are resolved relative to cwd, not the repo root. `stat`/`glob`
subcommands were dropped per human decision. Integration tests fully
rewritten (46 tests, all passing); unit tests and `go vet` unaffected.

## Insights
`git rev-parse --show-prefix` / `--show-toplevel` / `--is-bare-repository`
/ `--absolute-git-dir` cover all the repo-discovery and cwd-relative-path
plumbing a CLI needs — no manual path-resolution logic (which is a symlink
footgun on macOS) required.
