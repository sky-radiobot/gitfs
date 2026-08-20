# CLAUDE.md — gitfs

`gitfs` exposes the tree of a git commit as a read-only
[`io/fs`](https://pkg.go.dev/io/fs) filesystem. It reads from bare and
non-bare repositories and is pinned to an immutable commit — no working-tree
reads, ever.

## Repository layout

- `gitfs.go` — `GitFS` type, `Open` constructor, functional options, sparse filtering
- `backend.go` — the `backend` interface (including `lastCommit`, for `WithExtendedStats`), `entry`/`commitInfo` types, git-mode → `fs.FileMode` mapping
- `backend_gogit.go` — default backend, pure Go via [go-git](https://github.com/go-git/go-git)
- `backend_exec.go` — shell-out backend, used only when `WithGitBinary` is set
- `file.go` — in-memory `fs.File` implementation
- `cmd/gitfs/` — minimal CLI (`cat`/`ls`, built on [cobra](https://github.com/spf13/cobra)); the executable surface for the integration tests
- `cmd/git-ls/` — `git ls` subcommand binary: a deliberate copy of the gitfs `ls` subcommand pinned to HEAD (no shared package, by decision)
- `tests/integration/` — bash integration tests (`testsh.inc` runner, from radiospiel/critic)
- `benchmarks/` — Go benchmarks needing a real, sizeable repo (`~/projects/critic`) to be meaningful; skip-gated when it's absent. `RESULTS.md` records the latest numbers.
- `simple-go/` — git submodule, [radiospiel/simple-go](https://github.com/radiospiel/simple-go); only `src/assert` is consumed. Its own `CLAUDE.md` holds the Go-toolchain (gopls LSP) setup shared with this repo.
- `agents/` — task strategy guide, log template, and per-task progress logs

## Commands

- `make build` — build all packages
- `make install` — install the `gitfs` and `git-ls` CLIs to `GOBIN`/`GOPATH/bin`
- `make unit-tests` — Go unit tests
- `make integration-tests` — bash integration tests (builds `bin/gitfs` first)
- `make test` — unit + integration
- `make vet` — `go vet ./...`
- `make sync-submodules` — init/update submodules, push back local in-submodule changes
- `scripts/develop ARGS...` — rebuild and exec the CLI against the caller's
  cwd (not gitfs's own repo), for quick manual testing without `make build`
- `cmd/git-ls` — `git ls` subcommand binary: a deliberate copy of the gitfs
  `ls` subcommand pinned to HEAD; `-l` adds `--blame` with search depth from
  `git config gitls.blameLimit` (default 1000). Keep it in sync with
  `cmd/gitfs/main.go`'s ls.

## Design conventions

- `Open(path, sha, opts...)` accepts **full 40-hex commit SHAs only** — no
  branch names, tags, short SHAs, or any other ref resolution. The SHA is
  verified to be a commit at construction, so a bad SHA fails at the call
  site.
- The gogit-vs-shell-out difference lives entirely behind the `backend`
  interface. All FS operations, mode mapping, error mapping, and sparse
  filtering are written once, above it. Never special-case a backend outside
  its own file.
- New tunables are added as functional options (`WithGitBinary`,
  `WithSparse`, `WithExtendedStats`), never as new constructors.
- `WithExtendedStats` surfaces per-file last-commit info through
  `fs.FileInfo.Sys()` (returning `*ExtendedStat`) rather than a new method
  on `GitFS` — computed lazily on `Sys()` call, not up front, and backed by
  a `backend.lastCommit` method each backend implements independently
  (gogit walks first-parent history via `go-git`; exec shells out to
  `git log`).
- Public names are validated with `fs.ValidPath`; failures are returned as
  `*fs.PathError` (`fs.ErrNotExist` / `fs.ErrInvalid`).
- Symlinks are reported (`fs.ModeSymlink`) but never followed; blob content
  is the link target. Submodules (`160000`) appear in listings but error on
  `Open`/`ReadDir`/`ReadFile`.
- `WithSparse(paths...)` restricts visibility to the given repo-relative
  subtrees: outside paths behave exactly as if they do not exist; ancestor
  directories stay traversable.

## Session Setup: verify Go LSP

See [simple-go/CLAUDE.md](simple-go/CLAUDE.md#session-setup-verify-go-lsp)
for the full verification/installation procedure (gopls LSP plugin). It's
kept there as the single source of truth so it doesn't drift between this
repo and other consumers of the `simple-go` submodule.

## Task Strategy Selection

Before starting any task, identify which strategy applies from [agents/strategy-guide.md](agents/strategy-guide.md):

- **Bug Fix**: Something is broken, unexpected behavior, errors
- **Feature (TDD)**: New functionality, "add X" requests
- **Refactoring**: Code quality improvements, restructuring
- **Performance**: Optimization, speed/memory issues

**Required workflow:**
1. State which strategy you're following and why
2. Follow that strategy's workflow from the guide
3. If uncertain, ask the human before proceeding
4. For mixed tasks, decompose and apply strategies separately

## Task Progress Logging

Maintain a progress log in `agents/logs/` for each significant task. This provides visibility into agent work and captures insights.

Use the file "agents/logs-template.md" as a template

**Log file naming:** `YYYYMMDD-HHMMSS-short-description.md` (e.g., `20250115-143022-fix-scroll-crash.md`)

**To estimate complexity**, use the following guidance:
- Simple: Task could be completed without any critical human feedback
- Medium: A planning stage was necessary, with important human feedback. Human feedback after the planning stage was mostly cosmetic.
- Complex: The initial plan was not sufficient to guide the agent to a successful outcome, repeated human interventions had been necessary.

Note that timestamps **must always** have the time of day! It is important to always update the "Ended" timestamp when committing work.

**When to log:**
- Create the log when starting a non-trivial task
- Update progress as you complete steps
- Always document obstacles, even if resolved quickly
- when task completes:
	- Finalize with outcome
	- update the header section.

**Why obstacles matter:** Documenting obstacles helps identify recurring issues, improves future estimates, and provides context if the task is handed off or revisited.

## Stacked Pull Requests

Use **stacked PRs** by default. Unless the task at hand is obviously a single concern, split it into semantically meaningful, independently reviewable units that land as an ordered chain of small PRs rather than one large PR — e.g. a refactor + a feature + a build pipeline becomes three stacked PRs. Each PR in the stack must leave the repo green (`make build && make test`).

The stack tooling lives in `scripts/` (with `scripts/` on PATH the commands also work as git subcommands):

- `scripts/git-stack-create [branch]` — create a new branch as a child of the currently checked-out branch (parent recorded in `branch.<name>.stack-parent` git config), push it, and open a PR targeting the parent branch through the GitHub API
- `scripts/git-stack-restack [branch-in-stack]` — sync the whole stack: every branch is rebased onto its freshly fetched parent and force-pushed; when a parent's branch tip is contained in the fetched default branch, the child is rebased onto the default branch instead and its PR is retargeted through the GitHub API. Run this after any PR in the stack merges or gains commits.

Both commands require `GITHUB_TOKEN` or `GH_TOKEN` for GitHub API calls. Restacking is fully non-interactive: merge commits are flattened away, non-checked-out branches are rebased in a temporary worktree, and the current branch is rebased after automatically stashing local changes and restoring them before exit.

**Pushing changes:** do not rewrite published stack branches for ordinary changes — push review fixes and follow-ups as new commits on top, with plain (non-force) pushes, so collaborators' local checkouts keep fast-forwarding. PRs are squash-merged, so commit-level tidiness comes from the merge, not from amending. Force pushes (`--force-with-lease`) are reserved for restacking after a PR below has merged or gained commits (`git stack-restack`).

**Always open a PR when running in Claude Code on the web.** When the session runs in the cloud sandbox (`CLAUDE_CODE_REMOTE_ENVIRONMENT_TYPE=cloud_default`), open a pull request for the branch once the job is done — don't just push and stop. Reuse the existing PR if the branch already has one. This does not apply to interactive local sessions, where you should only open a PR when explicitly asked.

**Local checkouts of stack branches:** run `git config pull.rebase true` in your clone if you haven't yet. Restacks force-push rewritten history; with `pull.rebase` set, `git pull` replays only your local commits onto the new tip (skipping already-applied ones) instead of merging the old and new histories together — and it behaves like a plain fast-forward when you have no local commits.

## Architectural Design Documents

When you execute a plan that was explicitly built in plan mode, summarize that plan into a markdown architectural document at `docs/design/<name>.md` as part of the implementation. The `<name>` should match the plan's subject (e.g., `gitfs.md` for the initial gitfs implementation).

The design doc captures the *what* and *why* of the implemented system — context, constraints, key decisions and their rationale, and the final shape of the code — so future readers can understand the architecture without replaying the planning conversation. Keep it concise and focused on decisions that aren't obvious from the code itself.

This applies only to plans deliberately built via plan mode, not to ad-hoc tasks.

## Testing

### Testing conventions

When writing tests, use the `assert` package
(`github.com/radiospiel/simple-go/src/assert`, consumed via the `simple-go`
submodule). If a function is missing in the package, generate one. For
example, this:

```go
if !contains(entries, "main.go") {
    t.Error("expected main.go in entries")
}
```

should use:

```go
assert.Contains(t, entries, "main.go", "expected main.go in entries %v", entries)
```

Go unit tests build fixture repositories with the git CLI in `t.TempDir()`
and run the shared suite against both backends. `testing/fstest.TestFS`
provides stdlib conformance coverage.

### Integration tests

Integration tests live in `tests/integration/` and are run via
`make integration-tests`. They follow the testsh conventions from
radiospiel/critic (`test_*` functions, `assert_*` helpers, `run_tests`) and
drive the compiled `bin/gitfs` CLI through **both** backends — default go-git
and `-git-binary` — cross-checking output against the real git CLI as ground
truth. When adding CLI operations or changing filesystem behavior, add or
update integration tests to cover the change.

## Submodules

`simple-go` is consumed as a submodule with a `replace` directive in
`go.mod`. Always build/test via `make` (it inits submodules at parse time).
CI fails if the submodule pin drifts from `origin/main` — run
`make sync-submodules` and commit the updated pin.

## Commits

Commit meaningful steps individually with concise imperative subjects; stage
explicit paths (no `git add -A`). Every commit must leave
`make vet test` green.
