// Package gitfs exposes the tree of a single git commit as a read-only
// fs.FS.
//
// A GitFS reads from a bare or non-bare repository and is pinned to one
// immutable commit: Open verifies the commit, and every subsequent read goes
// against that snapshot. There are no working-tree reads.
package gitfs

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// GitFS is a read-only filesystem backed by the tree of a git commit.
type GitFS struct {
	be      backend
	modTime time.Time
	prefix  string   // repo-relative root of this (sub)filesystem
	sparse  []string // repo-relative paths visible through this FS; nil = everything
}

var (
	_ fs.FS         = (*GitFS)(nil)
	_ fs.ReadDirFS  = (*GitFS)(nil)
	_ fs.ReadFileFS = (*GitFS)(nil)
	_ fs.StatFS     = (*GitFS)(nil)
	_ fs.GlobFS     = (*GitFS)(nil)
	_ fs.SubFS      = (*GitFS)(nil)
)

// Option configures a GitFS.
type Option func(*config)

type config struct {
	gitBinary string
	sparse    []string
}

// WithGitBinary makes the FS read by shelling out to the git binary at
// path. Without it, gitfs uses the pure-Go go-git library and never spawns
// processes.
func WithGitBinary(path string) Option {
	return func(c *config) { c.gitBinary = path }
}

// WithSparse restricts the FS to the given repo-relative paths: anything
// outside them behaves exactly as if it did not exist, while ancestor
// directories stay traversable so the sparse paths remain reachable.
func WithSparse(paths ...string) Option {
	return func(c *config) { c.sparse = append(c.sparse, paths...) }
}

// Open opens the repository at repoPath (bare or non-bare) pinned to the
// commit sha. sha must be a full 40-character hex commit SHA — branch names,
// tags, and short SHAs are rejected. Open verifies that sha names a commit,
// so a bad SHA fails here rather than on first use.
func Open(repoPath string, sha string, opts ...Option) (*GitFS, error) {
	var cfg config
	for _, opt := range opts {
		opt(&cfg)
	}
	if !isCommitSHA(sha) {
		return nil, fmt.Errorf("gitfs: %q is not a full commit SHA", sha)
	}
	sparse, err := cleanSparse(cfg.sparse)
	if err != nil {
		return nil, err
	}

	var be backend
	if cfg.gitBinary != "" {
		be = &execBackend{binary: cfg.gitBinary}
	} else {
		be = &gogitBackend{}
	}
	if err := be.open(repoPath); err != nil {
		return nil, fmt.Errorf("gitfs: cannot open repository at %s: %w", repoPath, err)
	}
	modTime, err := be.pin(sha)
	if err != nil {
		return nil, fmt.Errorf("gitfs: %s: %w", sha, err)
	}
	return &GitFS{be: be, modTime: modTime, sparse: sparse}, nil
}

// Open opens name for reading and returns the file handle. Directory handles
// additionally implement fs.ReadDirFile.
func (g *GitFS) Open(name string) (fs.File, error) {
	p, err := g.resolve("open", name)
	if err != nil {
		return nil, err
	}
	if !g.visible(p) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	e, err := g.lookupEntry(p)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	info := g.info(e)
	if e.mode.IsDir() {
		return &file{info: info, g: g, path: p}, nil
	}
	if !g.readable(p) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	if e.mode.IsRegular() || e.mode&fs.ModeSymlink != 0 {
		data, err := g.be.readBlob(e.hash)
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
		return &file{info: info, g: g, path: p, content: bytes.NewReader(data)}, nil
	}
	// Submodules (gitlinks) and unknown entry types are listed but not openable.
	return nil, &fs.PathError{Op: "open", Path: name, Err: errUnsupported}
}

// ReadDir returns the visible entries of the directory name, sorted by name.
func (g *GitFS) ReadDir(name string) ([]fs.DirEntry, error) {
	p, err := g.resolve("readdir", name)
	if err != nil {
		return nil, err
	}
	if !g.visible(p) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	e, err := g.lookupEntry(p)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	if !e.mode.IsDir() {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errNotDir}
	}
	entries, err := g.dirEntries(p)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	return entries, nil
}

// ReadFile returns the content of the blob at name. For a symlink that is
// the link target; symlinks are never followed.
func (g *GitFS) ReadFile(name string) ([]byte, error) {
	f, err := g.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Stat returns the fs.FileInfo for name.
func (g *GitFS) Stat(name string) (fs.FileInfo, error) {
	p, err := g.resolve("stat", name)
	if err != nil {
		return nil, err
	}
	if !g.visible(p) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	e, err := g.lookupEntry(p)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	if !e.mode.IsDir() && !g.readable(p) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return g.info(e), nil
}

// Glob returns the names of all visible files matching pattern, per
// path.Match syntax. It delegates to the generic fs.Glob implementation
// (built on ReadDir), so sparse filtering applies.
func (g *GitFS) Glob(pattern string) ([]string, error) {
	// The wrapper hides GlobFS so fs.Glob does not dispatch back into this
	// method; the generic implementation drives Open/ReadDir instead.
	return fs.Glob(struct{ fs.FS }{g}, pattern)
}

// Sub returns a GitFS rooted at the directory dir — a copy sharing the
// backend, with dir joined onto the prefix.
func (g *GitFS) Sub(dir string) (fs.FS, error) {
	p, err := g.resolve("sub", dir)
	if err != nil {
		return nil, err
	}
	if !g.visible(p) {
		return nil, &fs.PathError{Op: "sub", Path: dir, Err: fs.ErrNotExist}
	}
	e, err := g.lookupEntry(p)
	if err != nil {
		return nil, &fs.PathError{Op: "sub", Path: dir, Err: err}
	}
	if !e.mode.IsDir() {
		return nil, &fs.PathError{Op: "sub", Path: dir, Err: errNotDir}
	}
	sub := *g
	sub.prefix = p
	return &sub, nil
}

// resolve validates an fs name and maps it to a repo-relative path.
func (g *GitFS) resolve(op, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	return joinPath(g.prefix, name), nil
}

// lookupEntry returns the entry at repo-relative path p, synthesizing the
// root directory.
func (g *GitFS) lookupEntry(p string) (entry, error) {
	if p == "" {
		return entry{name: ".", mode: fs.ModeDir | 0o755}, nil
	}
	return g.be.lookup(p)
}

// dirEntries returns the visible entries of the directory at repo-relative
// path p, sorted by name.
func (g *GitFS) dirEntries(p string) ([]fs.DirEntry, error) {
	entries, err := g.be.list(p)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if !g.visible(joinPath(p, e.name)) {
			continue
		}
		out = append(out, dirEntry{g.info(e)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func (g *GitFS) info(e entry) fileInfo {
	return fileInfo{name: e.name, size: e.size, mode: e.mode, modTime: g.modTime}
}

// visible reports whether repo-relative path p may be seen through this FS:
// with a sparse set, p must be inside a sparse path or an ancestor of one.
func (g *GitFS) visible(p string) bool {
	if len(g.sparse) == 0 || p == "" {
		return true
	}
	for _, s := range g.sparse {
		if p == s || strings.HasPrefix(p, s+"/") || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	return false
}

// readable reports whether p may be opened for reading: with a sparse set,
// p must be inside a sparse path (mere ancestors are only traversable).
func (g *GitFS) readable(p string) bool {
	if len(g.sparse) == 0 {
		return true
	}
	for _, s := range g.sparse {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

// joinPath joins a repo-relative prefix and an fs name; the root is "".
func joinPath(prefix, name string) string {
	if name == "." {
		return prefix
	}
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

// isCommitSHA reports whether s is a full 40-hex (SHA-1) object id.
func isCommitSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		isDigit := '0' <= c && c <= '9'
		isHex := ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
		if !isDigit && !isHex {
			return false
		}
	}
	return true
}

func cleanSparse(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if !fs.ValidPath(p) || p == "." {
			return nil, fmt.Errorf("gitfs: invalid sparse path %q", p)
		}
		out = append(out, p)
	}
	return out, nil
}
