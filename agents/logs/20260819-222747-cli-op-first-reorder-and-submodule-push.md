# Task: Reorder gitfs CLI to op-first, GIT_BINARY env var; push simple-go docs

**Started:** 2026-08-19 22:15:36
**Ended:** 2026-08-19 22:27:47
**Strategy:** Refactoring
**Status:** Completed
**Complexity:** Medium
**Used Models:** Sonnet 5
**Token usage (Estimated):** n/a

## Objective
Two related pieces of work landed in the same session:

1. Extend `simple-go` (a separate GitHub repo, `radiospiel/simple-go`,
   consumed as a git submodule) with its own `CLAUDE.md` collecting the
   Go-toolchain setup previously duplicated in `gitfs/CLAUDE.md`
   ("Session Setup: verify Go LSP"), plus a `README.md` section
   explaining how consuming repos should reference it.
2. Reorder the `gitfs` CLI so the subcommand comes before `REF`
   (`gitfs cat REF PATH...` / `gitfs ls REF [-l] [PATH...]`, previously
   `gitfs REF cat|ls ...`), and replace `--git-binary` with a
   `GIT_BINARY` env var, since it no longer has a natural place in the
   args once the subcommand moved first.

## Progress
- [x] Wrote `simple-go/CLAUDE.md` (Go LSP setup, generalized from
      gitfs/CLAUDE.md) and extended `simple-go/README.md`
- [x] Replaced the LSP section in `gitfs/CLAUDE.md` with a pointer to
      `simple-go/CLAUDE.md` (human chose to dedupe rather than keep two
      copies)
- [x] Pushed `simple-go`'s changes to `radiospiel/simple-go` origin/main,
      bumped the submodule pin in gitfs, committed both (`41f9d41`)
- [x] Rewrote `cmd/gitfs/main.go`: `cat`/`ls` are now ordinary cobra
      subcommands with `REF` as `args[0]`, opening the GitFS themselves
      via a shared `openFS(ref, sparse)` helper; `--sparse` is a
      persistent flag; `GIT_BINARY` env var replaces `--git-binary`
- [x] Updated `tests/integration/test_gitfs.sh` (op-first everywhere,
      `GIT_BINARY=...` instead of `--git-binary`) and `README.md`
- [x] `make vet test` green (46/46 integration tests)
- [x] `make install` refreshed the installed binary

## Obstacles
- **Issue:** `make sync-submodules` (the documented way to push local
  `simple-go/` edits upstream) failed: `fatal: could not read Username
  for 'https://github.com': Device not configured`. simple-go's `origin`
  remote uses `https://github.com/...`, and this environment has no
  stored HTTPS credential / non-interactive prompt available, unlike
  gitfs's own `origin` which is already `git@github.com:...` (SSH, works
  via the loaded SSH agent identity).
  **Resolution:** Confirmed via `gh auth status` that the account's git
  protocol is actually SSH. Pushed with an explicit SSH remote
  (`git push git@github.com:radiospiel/simple-go.git main`), which
  succeeded, then ran `git remote set-url origin
  git@github.com:radiospiel/simple-go.git` inside the submodule so
  future syncs (including `scripts/sync-submodules`) don't hit the same
  wall.
- **Issue (more serious):** Because the push failed, `scripts/sync-submodules`
  aborted (under `set -e`) before reaching its final `git add
  simple-go` step in the outer gitfs repo, leaving the submodule
  committed (branch `main`, commit `839055e`, containing the new
  `CLAUDE.md`/`README.md`) but with gitfs's *index* still pointing at
  the old pin. Some time later — most likely a `make` invocation, since
  the Makefile runs `git submodule update --init` unconditionally at
  *parse time*, on every single invocation, regardless of target — the
  submodule got checked out back to the old pinned SHA, **detaching
  HEAD and making the new commit invisible** in the working tree (though
  the `main` branch ref itself was untouched).
  **Resolution:** `git reflog` inside `simple-go` showed the sequence
  clearly (checkout to main → commit 839055e → checkout back to the old
  pinned SHA), and `git branch -a -v` confirmed `main` still pointed at
  `839055e`. `git checkout main` recovered it instantly; nothing was
  lost. Pushed immediately afterward in the same turn to close the
  window before another `make` could reset it again, *then* committed
  the pin bump in gitfs (`git add simple-go`) before running `make`
  again.

## Outcome
`simple-go/CLAUDE.md` and the `README.md` addition are live on
`radiospiel/simple-go`'s `main` branch; gitfs's submodule pin and
`CLAUDE.md` pointer are committed (`41f9d41`). `cmd/gitfs`'s CLI shape is
now `gitfs cat|ls REF [ARGS]` with `GIT_BINARY` replacing `--git-binary`
(`a1797e0`); all tests, README, and the installed binary are up to date.

## Insights
- This Makefile's `$(shell git submodule update --init > /dev/null
  2>&1)` parse-time hook is a footgun when a submodule has local commits
  the outer repo hasn't pinned yet: *any* `make` invocation (not just
  `sync-submodules`) will silently detach the submodule's HEAD back to
  the stale pin. After committing inside a submodule but before the
  outer repo's pin is committed, avoid running `make` in the outer repo
  — use plain `go build`/`go vet`/the compiled binary directly instead —
  until the push + pin bump are both done.
- When a repo's remote is `https://github.com/...` but `gh auth status`
  reports the account's git protocol as `ssh`, non-interactive pushes to
  that remote will fail on credential prompts even though the account is
  otherwise authenticated; switching the remote URL to the `git@`
  form (matching the account's actual configured protocol) fixes it for
  good, not just for one push.
