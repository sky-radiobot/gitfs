package gitfs

import (
	"io/fs"
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
