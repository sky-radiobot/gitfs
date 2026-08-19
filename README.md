# gitfs

`gitfs` is a Go module that exposes the tree of a git commit as a read-only
[`io/fs.FS`](https://pkg.go.dev/io/fs). It reads git objects directly — from
bare or non-bare repositories — so you can read files at a given commit
without checking anything out to disk: no working-tree copy, no temporary
clone, no `git checkout`.

A `GitFS` is pinned to one immutable commit at construction: every
subsequent read goes against that snapshot, and there are no working-tree
reads at all.

## Library

```go
import "gitfs"

fsys, err := gitfs.Open(repoPath, sha) // sha: full 40-hex commit SHA
if err != nil {
    log.Fatal(err)
}

data, err := fsys.ReadFile("src/app/main.go")
entries, err := fsys.ReadDir("src/app")
matches, err := fsys.Glob("src/**/*.go")
```

`Open` accepts full 40-character hex commit SHAs only — no branch names,
tags, short SHAs, or other ref resolution; the SHA is verified to name a
commit at construction, so a bad SHA fails at the call site rather than on
first use.

`GitFS` implements `fs.FS`, `fs.ReadDirFS`, `fs.ReadFileFS`, `fs.StatFS`,
`fs.GlobFS`, and `fs.SubFS`. Symlinks are reported (`fs.ModeSymlink`) but
never followed — reading one returns the link target text. Submodules
(`160000` entries) appear in listings but error on open.

Two functional options tune how a `GitFS` reads:

- `gitfs.WithGitBinary(path)` — shell out to the `git` binary at `path`
  instead of the default pure-Go [go-git](https://github.com/go-git/go-git)
  backend.
- `gitfs.WithSparse(paths...)` — restrict visibility to the given
  repo-relative subtrees; everything outside them behaves exactly as if it
  doesn't exist, while ancestor directories stay traversable so the sparse
  paths remain reachable.

## CLI

`cmd/gitfs` builds a small `gitfs` command-line tool around the library:

```sh
gitfs cat REF PATH [PATH...]
gitfs ls REF [-l] [PATH...]
```

`REF` is a full commit SHA, or anything the `git` CLI can resolve to one —
a branch, a tag, a short SHA, `HEAD`, etc.; unresolvable refs fail with an
error. The repository itself isn't passed on the command line — it's
discovered from the current directory the same way `git` itself does
(working-tree root or bare repo dir, searching upward), and `PATH`
arguments are resolved relative to the current directory, just like plain
`cat`/`ls`.

```sh
$ cd myrepo
$ gitfs ls HEAD~2 src
main.go
util.go

$ gitfs cat main README.md
...
```

`ls` defaults to plain names, one per line; `-l` switches to
`mode\tsize\tname`. Both subcommands accept multiple paths.

- `--sparse p1,p2` — restrict the filesystem to the given repo-relative
  subtrees.
- `GIT_BINARY=path` — use the shell-out backend at `path` instead of
  go-git (also used for the CLI's own ref resolution and repo discovery,
  regardless of backend).

Built on [cobra](https://github.com/spf13/cobra): `cat`/`ls` are ordinary
cobra subcommands with `REF` as their first positional argument, so e.g.
`gitfs cat --help` works and documents `REF` correctly.

## Development

- `make build` — build all packages
- `make install` — install the `gitfs` CLI to `GOBIN`/`GOPATH/bin`
- `make unit-tests` — Go unit tests
- `make integration-tests` — bash integration tests against the compiled
  CLI, cross-checked against the real `git` CLI, on both backends
- `make test` — unit + integration
- `make vet` — `go vet ./...`
- `scripts/develop ARGS...` — rebuild and run the CLI against whatever repo
  you're currently in (e.g. `scripts/develop cat main README.md`), without
  needing `make build`/`make install` first

See [CLAUDE.md](CLAUDE.md) for repository layout and design conventions.
