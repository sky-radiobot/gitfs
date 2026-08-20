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

Functional options tune how a `GitFS` reads:

- `gitfs.WithGitBinary(path)` — shell out to the `git` binary at `path`
  instead of the default pure-Go [go-git](https://github.com/go-git/go-git)
  backend.
- `gitfs.WithSparse(paths...)` — restrict visibility to the given
  repo-relative subtrees; everything outside them behaves exactly as if it
  doesn't exist, while ancestor directories stay traversable so the sparse
  paths remain reachable.
- `gitfs.WithExtendedStats(maxCommits)` — make every `fs.FileInfo`'s `Sys()`
  return a `*gitfs.ExtendedStat` (`Commit`, `Author`, `AuthorEmail`, `Date`,
  `Err`): the last commit, at or before the pinned one, that touched that
  entry's path — computed lazily, on first `Sys()` call, not up front.
  `maxCommits` bounds how many ancestor commits are searched before falling
  back to the pinned commit's own info (0 examines only the pinned commit
  itself; negative is unbounded, which can be slow on a large history); the
  result is never a commit newer than the pinned one, though it may be
  imprecise under a tight bound.
- `gitfs.WithBlameFallback(path)` — for the default go-git backend's
  `WithExtendedStats` lookups only: every search first probes with the
  cheap pure-Go walk (capped at 150 ancestor commits), and only shells out
  to the git binary at `path` if that probe comes up empty — so a shallow
  answer stays on the fast path even under an unbounded/large `maxCommits`,
  while a search that's actually deep escalates instead of walking
  hundreds of commits in pure Go. Benchmarked against a real 800+ commit
  repo in [`benchmarks/`](benchmarks/RESULTS.md). Has no effect when
  `WithGitBinary` is also set.

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

`ls` defaults to plain names, one per line; `-l` switches to space-aligned
columns like plain `ls -l` — mode, size (right-aligned), date, name — with
no tabs; column widths are computed per listing (each directory argument
gets its own aligned block). Date is REF's commit date, the same for every
row, formatted like plain `ls -l` — `Aug 19 22:03`, or `Oct 18  2024` for
anything more than ~6 months old or in the future. Both subcommands
accept multiple paths, and any of them may be a
`path.Match`-style glob (e.g. `gitfs ls main 'sc*'`) — matched against the
tree at REF, not your local filesystem, so the glob typically needs to be
quoted/escaped (`'sc*'`, not `sc*`) to stop your shell from expanding it
against local files before gitfs ever sees it.

`ls --blame[=LIMIT]` (implies `-l`) adds, per file, the last commit at or
before REF that touched it — its author's email, date, and short (7-char)
SHA — as columns mode, author email, size, date, commit, name (owner
placed right after mode, matching plain `ls -l`'s column order). It's a
thin wrapper over
`gitfs.WithExtendedStats` (see above): one commit per file, not full
per-line blame. Without a `LIMIT`, the search walks REF's entire ancestry,
which can be slow on a large history; `--blame=LIMIT` bounds it to `LIMIT`
ancestor commits, falling back to REF's own commit if nothing turns up
within that window — always at or before REF, but possibly imprecise.

When `origin` is a GitHub remote and the blamed commit is reachable from
one of its `origin/*` branches (checked locally, so only as fresh as your
last `fetch`), the commit column is rendered as a clickable OSC 8 terminal
hyperlink (`https://github.com/<owner>/<repo>/commit/<sha>`) with the
short SHA as its visible text; otherwise it's a plain short SHA. Likewise,
an author email in GitHub's `<id>+<username>@users.noreply.github.com` (or
legacy `<username>@users.noreply.github.com`) form is rendered as a link
to `https://github.com/<username>`, showing just the username — this
doesn't depend on `origin` being GitHub, since it's a GitHub account
identity, not a repo one. Links only render when stdout looks like a
terminal that supports them (a real TTY, `TERM` not `dumb`/unset); set
`GITFS_FORCE_HYPERLINKS=1` to force them regardless — e.g. when piping
through `less -R`, which preserves escape sequences despite output no
longer being a TTY.

- `--sparse p1,p2` — restrict the filesystem to the given repo-relative
  subtrees.
- `GIT_BINARY=path` — use the shell-out backend at `path` instead of
  go-git (also used for the CLI's own ref resolution and repo discovery,
  regardless of backend).

Built on [cobra](https://github.com/spf13/cobra): `cat`/`ls` are ordinary
cobra subcommands with `REF` as their first positional argument, so e.g.
`gitfs cat --help` works and documents `REF` correctly.

Shell completion (`gitfs completion bash|zsh|fish|powershell`) completes
subcommands out of the box, and `REF` against local branches, tags,
remote-tracking refs, and `HEAD` — e.g. `gitfs cat ma<TAB>` completes to
`main`. Typing 4+ hex digits (e.g. `919<TAB>` → nothing, `9195<TAB>` → the
full SHA) also completes commit-SHA prefixes, via git's own indexed object
lookup rather than a history scan, so it stays fast regardless of repo
size — 4 is a hard floor of git's own lookup (`rev-parse --disambiguate`
silently returns nothing below that), not just a tuning choice.
`PATH` arguments fall back to the shell's normal file completion.

### `git ls`

`cmd/git-ls` builds a `git-ls` binary that repackages `ls` as a `git`
subcommand pinned to the current checkout — with the binary on your
`PATH` (e.g. via `make install`), `git ls` works like any other git
command:

```sh
git ls [PATH...]           # ≡  gitfs ls HEAD [PATH...]
git ls -l|--long [PATH...] # ≡  gitfs ls HEAD --blame=N [PATH...]
```

Plain `git ls` prints names only; `-l` switches to the blame listing. Its
search depth `N` defaults to 1000 ancestor commits and is configurable via
`git config gitls.blameLimit` (e.g. `git config gitls.blameLimit 500`); an
explicit `--blame[=LIMIT]` argument overrides it. Everything else — paths,
globs, `--sparse` — behaves exactly as in `gitfs ls HEAD`. The binary
contains a deliberate copy of the gitfs `ls` subcommand's implementation
rather than sharing a package; keep the two in sync.

## Development

- `make build` — build all packages
- `make install` — install the `gitfs` and `git-ls` CLIs to
  `GOBIN`/`GOPATH/bin`
- `make unit-tests` — Go unit tests
- `make integration-tests` — bash integration tests against the compiled
  CLI, cross-checked against the real `git` CLI, on both backends
- `make test` — unit + integration
- `make vet` — `go vet ./...`
- `scripts/develop ARGS...` — rebuild and run the CLI against whatever repo
  you're currently in (e.g. `scripts/develop cat main README.md`), without
  needing `make build`/`make install` first

See [CLAUDE.md](CLAUDE.md) for repository layout and design conventions.
