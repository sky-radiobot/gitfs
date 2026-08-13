# AGENTS.md — gitfs

`gitfs` exposes the tree of a git commit as a read-only
[`io/fs`](https://pkg.go.dev/io/fs) filesystem. It reads from bare and
non-bare repositories and is pinned to an immutable commit — no working-tree
reads, ever.

## Repository layout

- `gitfs.go` — `GitFS` type, `Open` constructor, functional options, sparse filtering
- `backend.go` — the `backend` interface, `entry` type, git-mode → `fs.FileMode` mapping
- `backend_gogit.go` — default backend, pure Go via [go-git](https://github.com/go-git/go-git)
- `backend_exec.go` — shell-out backend, used only when `WithGitBinary` is set
- `file.go` — in-memory `fs.File` implementation
- `cmd/gitfs/` — minimal CLI (`cat`/`ls`/`stat`/`glob`); the executable surface for the integration tests
- `tests/integration/` — bash integration tests (`testsh.inc` runner, from radiospiel/critic)
- `simple-go/` — git submodule, [radiospiel/simple-go](https://github.com/radiospiel/simple-go); only `src/assert` is consumed

## Commands

- `make build` — build `bin/gitfs`
- `make unit-tests` — Go unit tests
- `make integration-tests` — bash integration tests (builds `bin/gitfs` first)
- `make test` — unit + integration
- `make vet` — `go vet ./...`
- `make sync-submodules` — init/update submodules, push back local in-submodule changes

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
  `WithSparse`), never as new constructors.
- Public names are validated with `fs.ValidPath`; failures are returned as
  `*fs.PathError` (`fs.ErrNotExist` / `fs.ErrInvalid`).
- Symlinks are reported (`fs.ModeSymlink`) but never followed; blob content
  is the link target. Submodules (`160000`) appear in listings but error on
  `Open`/`ReadDir`/`ReadFile`.
- `WithSparse(paths...)` restricts visibility to the given repo-relative
  subtrees: outside paths behave exactly as if they do not exist; ancestor
  directories stay traversable.

## Testing

Two layers, both run in CI:

1. **Go unit tests** — fixtures are real git repos built with the git CLI in
   `t.TempDir()`. `testing/fstest.TestFS` runs against both backends.
   Assertions use `github.com/radiospiel/simple-go/src/assert`.
2. **Bash integration tests** — `tests/integration/test_gitfs.sh` drives the
   compiled `bin/gitfs` through both backends and cross-checks output against
   the real git CLI as ground truth. Runner conventions come from
   `testsh.inc` (`test_*` functions, `assert_*` helpers, `run_tests`).

## Submodules

`simple-go` is consumed as a submodule with a `replace` directive in
`go.mod`. Always build/test via `make` (it inits submodules at parse time).
CI fails if the submodule pin drifts from `origin/main` — run
`make sync-submodules` and commit the updated pin.

## Commits

Commit meaningful steps individually with concise imperative subjects; stage
explicit paths (no `git add -A`). Every commit must leave
`make vet test` green.
