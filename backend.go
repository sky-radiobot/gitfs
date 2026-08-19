package gitfs

import (
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// entry describes a single node in the pinned commit's tree.
type entry struct {
	name string      // base name
	mode fs.FileMode // type bits + permissions
	hash string      // git object id (blob or tree); "" for the synthesized root
	size int64       // blob size; 0 for non-blobs
}

// backend abstracts how git objects are read. gogitBackend uses the pure-Go
// go-git library; execBackend shells out to a git binary. Everything else —
// fs semantics, mode mapping, sparse filtering — lives above this interface.
type backend interface {
	// open locates the repository at path (bare or non-bare).
	open(path string) error
	// pin verifies sha names a commit, pins all subsequent reads to it, and
	// returns the committer time.
	pin(sha string) (time.Time, error)
	// lookup returns the entry at repo-relative path p; p is never "" (the
	// root is synthesized by GitFS). Missing paths report fs.ErrNotExist.
	lookup(p string) (entry, error)
	// list returns the entries of the directory at repo-relative path p
	// ("" is the root), in git tree order.
	list(p string) ([]entry, error)
	// readBlob returns the content of the blob with the given hash.
	readBlob(hash string) ([]byte, error)
	// lastCommit returns the last commit, within maxCommits ancestors of
	// the pinned commit (unbounded if maxCommits is negative; 0 examines
	// only the pinned commit itself), that changed the entry at
	// repo-relative path p ("" is the root). If none is found
	// within that bound, it returns the pinned commit's own info instead
	// — the result is never a commit newer than the pinned one, though it
	// may be imprecise under a tight bound. Used only when WithExtendedStats
	// is set.
	lastCommit(p string, maxCommits int) (commitInfo, error)
}

// commitInfo is the identity of a single commit, as returned by
// backend.lastCommit.
type commitInfo struct {
	sha    string
	author string
	email  string
	date   time.Time
}

// logOneCommit runs `git log --max-count=1` over revOrRange (optionally
// filtered to path; path == "" means no pathspec) via run, and parses the
// result. Shared by execBackend.lastCommit and gogitBackend's
// WithBlameFallback path, since only how git gets invoked differs between
// them (--git-dir=X vs -C repoPath), not what's asked of it or how the
// answer is parsed. ok is false, not an error, when nothing matches.
func logOneCommit(run func(args ...string) ([]byte, error), revOrRange, path string) (ci commitInfo, ok bool, err error) {
	args := []string{"log", "--max-count=1", "--format=%H%x09%aI%x09%an%x09%ae", revOrRange}
	if path != "" {
		args = append(args, "--", path)
	}
	out, err := run(args...)
	if err != nil {
		return commitInfo{}, false, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return commitInfo{}, false, nil
	}
	fields := strings.Split(line, "\t")
	if len(fields) != 4 {
		return commitInfo{}, false, fmt.Errorf("gitfs: unexpected git log output: %q", line)
	}
	date, err := time.Parse(time.RFC3339, fields[1])
	if err != nil {
		return commitInfo{}, false, err
	}
	return commitInfo{sha: fields[0], date: date, author: fields[2], email: fields[3]}, true, nil
}

// mustLogOneCommit is logOneCommit without a pathspec, for the case where
// rev is known to resolve to a commit (it always should: it's either the
// pinned commit itself, or one already found by an earlier logOneCommit
// call), so "no match" is treated as an error rather than ok=false.
func mustLogOneCommit(run func(args ...string) ([]byte, error), rev string) (commitInfo, error) {
	ci, ok, err := logOneCommit(run, rev, "")
	if err != nil {
		return commitInfo{}, err
	}
	if !ok {
		return commitInfo{}, fmt.Errorf("gitfs: no commit found for %s", rev)
	}
	return ci, nil
}

// modeFromGit maps a git tree entry mode to fs.FileMode.
func modeFromGit(m uint32) fs.FileMode {
	switch m {
	case 0o040000: // tree
		return fs.ModeDir | 0o755
	case 0o100644, 0o100664: // blob (0664 is the deprecated regular variant)
		return 0o644
	case 0o100755: // executable blob
		return 0o755
	case 0o120000: // symlink
		return fs.ModeSymlink | 0o777
	default: // 0o160000 gitlink (submodule) and anything else
		return fs.ModeIrregular
	}
}
