# gitfs — initial implementation

Summarized from the plan-mode plan executed in the initial build (August 2026).

## What

`gitfs` exposes the tree of a single git commit as a read-only `io/fs`
filesystem: `fs.FS`, `ReadDirFS`, `ReadFileFS`, `StatFS`, `GlobFS`, `SubFS`.
It reads from bare and non-bare repositories. A minimal CLI (`cmd/gitfs`)
provides `cat`/`ls`/`stat`/`glob`, primarily as the executable surface for
the bash integration tests.

## Why the constructor is fallible

`Open(repoPath, sha, opts...) (*GitFS, error)` verifies at construction that
`sha` names a commit. That buys three things: a bad SHA fails at the call
site instead of surfacing as a confusing `*fs.PathError` on the first `Open`;
the FS is pinned to an immutable snapshot; and no read ever repeats a
lookup. Without eager verification the constructor could just return a value.

`sha` must be a **full 40-hex commit SHA** — branch names, tags, and short
SHAs are rejected. There is no ref resolution anywhere in the codebase;
pinning to a SHA (rather than accepting a `WORKTREE`-style sentinel) also
structurally excludes working-tree reads, which is what makes bare repos a
first-class case.

## The backend interface

The only difference between "pure Go" and "shell out" modes is how git
objects are read, so exactly that sits behind an internal interface:

```go
type backend interface {
    open(path string) error
    pin(sha string) (time.Time, error)
    lookup(p string) (entry, error)
    list(p string) ([]entry, error)
    readBlob(hash string) ([]byte, error)
}
```

- `gogitBackend` (default) uses go-git v5: `plain.Open` (handles bare and
  non-bare), tree walking via `object.Tree`, blobs via `BlobObject`.
- `execBackend` (only via `WithGitBinary(path)`) runs
  `git --git-dir=<abs>` plumbing: `ls-tree --long -z`, `cat-file blob`.
  `--git-dir` (resolved once via `rev-parse --absolute-git-dir`) is the
  unambiguous form that behaves identically for bare and non-bare repos.

All fs semantics — name validation, `*fs.PathError` mapping, mode mapping,
sparse filtering, sorting — are written once, above the interface.

## Options

Functional options (`WithGitBinary`, `WithSparse`) keep the constructor
single; future tunables (max blob size, symlink policy, context) are new
options, never new constructors.

## Sparse semantics

`WithSparse(paths...)` restricts the FS to repo-relative subtrees: a path is
visible iff it is inside a sparse path (readable) or a strict ancestor of one
(traversable only). Non-visible paths are indistinguishable from absent
(`fs.ErrNotExist`); `ReadDir` filters; `Glob` delegates to the generic
`fs.Glob` over `ReadDir` and inherits the filter.

## Other decisions

- Symlinks are reported (`fs.ModeSymlink`) but never followed; blob content
  is the link target. Submodules (`160000`) are listed but error on open.
- `Sub(dir)` returns a copy with `path.Join(prefix, dir)` — no wrapper, no
  `fs.Sub` fallback.
- `ModTime()` is the pinned commit's committer time, captured at construction
  (zero times break some `http.FileServer`-style consumers).
- Directory handles implement `fs.ReadDirFile`; file handles support
  `Seek`/`ReadAt` — both required by `testing/fstest.TestFS`, which runs
  against both backends.
- Integration tests are bash, following radiospiel/critic's `testsh`
  conventions; every assertion runs both backends and cross-checks the real
  git CLI as ground truth.
- `simple-go` is a git submodule with a `replace` directive (per its README);
  only `src/assert` is consumed. CI fails on pin drift from its `main`.
