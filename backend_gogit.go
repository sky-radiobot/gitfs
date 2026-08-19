package gitfs

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// gogitBackend reads git objects via the pure-Go go-git library. It is the
// default backend; nothing spawns external processes.
type gogitBackend struct {
	repo   *git.Repository
	tree   *object.Tree
	commit *object.Commit
}

func (b *gogitBackend) open(path string) error {
	repo, err := git.PlainOpen(path)
	if err != nil {
		return err
	}
	b.repo = repo
	return nil
}

func (b *gogitBackend) pin(sha string) (time.Time, error) {
	commit, err := b.repo.CommitObject(plumbing.NewHash(sha))
	if err != nil {
		return time.Time{}, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return time.Time{}, err
	}
	b.tree = tree
	b.commit = commit
	return commit.Committer.When, nil
}

func (b *gogitBackend) lookup(p string) (entry, error) {
	te, err := b.tree.FindEntry(p)
	if err != nil {
		if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
			return entry{}, fs.ErrNotExist
		}
		return entry{}, err
	}
	e := entry{
		name: path.Base(p),
		mode: modeFromGit(uint32(te.Mode)),
		hash: te.Hash.String(),
	}
	if err := b.fillSize(&e, te.Hash); err != nil {
		return entry{}, err
	}
	return e, nil
}

func (b *gogitBackend) list(p string) ([]entry, error) {
	tree := b.tree
	if p != "" {
		sub, err := b.tree.Tree(p)
		if err != nil {
			if errors.Is(err, object.ErrDirectoryNotFound) {
				return nil, fs.ErrNotExist
			}
			return nil, err
		}
		tree = sub
	}
	entries := make([]entry, 0, len(tree.Entries))
	for _, te := range tree.Entries {
		e := entry{
			name: te.Name,
			mode: modeFromGit(uint32(te.Mode)),
			hash: te.Hash.String(),
		}
		if err := b.fillSize(&e, te.Hash); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func (b *gogitBackend) readBlob(hash string) ([]byte, error) {
	blob, err := b.repo.BlobObject(plumbing.NewHash(hash))
	if err != nil {
		return nil, err
	}
	r, err := blob.Reader()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// lastCommit walks first-parent history from the pinned commit, comparing
// the tree entry at p between each commit and its first parent, stopping
// at the first commit where it differs (added, changed, or — for the
// root, p == "" — the tree as a whole differs). This is a first-parent
// simplification, not full history simplification with merge handling
// like execBackend's `git log -- path` does; the two backends can
// disagree on merge-heavy histories, though not on the linear histories
// gitfs's own tests use.
func (b *gogitBackend) lastCommit(p string, maxCommits int) (commitInfo, error) {
	if p == "" {
		// The root has no meaningful "last touched" commit of its own;
		// report the pinned commit directly rather than defining one.
		return commitFromObject(b.commit), nil
	}

	iter, err := b.repo.Log(&git.LogOptions{From: b.commit.Hash, Order: git.LogOrderCommitterTime})
	if err != nil {
		return commitInfo{}, err
	}
	defer iter.Close()

	examined := 0
	for maxCommits < 0 || examined < maxCommits {
		c, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return commitInfo{}, err
		}
		examined++
		touched, err := commitTouchesPath(c, p)
		if err != nil {
			return commitInfo{}, err
		}
		if touched {
			return commitFromObject(c), nil
		}
	}
	return commitFromObject(b.commit), nil
}

// commitTouchesPath reports whether the tree entry at p differs between c
// and its first parent (or, for a root commit with no parents, whether p
// exists in c at all).
func commitTouchesPath(c *object.Commit, p string) (bool, error) {
	tree, err := c.Tree()
	if err != nil {
		return false, err
	}
	curHash, err := treeEntryHash(tree, p)
	if err != nil {
		return false, err
	}
	if curHash == "" {
		return false, nil // p doesn't exist at c; not the commit we want.
	}

	parents := c.Parents()
	parent, err := parents.Next()
	if err == io.EOF {
		return true, nil // root commit: p exists here, nothing to compare against.
	}
	if err != nil {
		return false, err
	}
	parentTree, err := parent.Tree()
	if err != nil {
		return false, err
	}
	parentHash, err := treeEntryHash(parentTree, p)
	if err != nil {
		return false, err
	}
	return curHash != parentHash, nil
}

// treeEntryHash returns the hash of the tree entry at p, or "" if p
// doesn't exist in tree.
func treeEntryHash(tree *object.Tree, p string) (string, error) {
	if p == "" {
		return tree.Hash.String(), nil
	}
	te, err := tree.FindEntry(p)
	if err != nil {
		if errors.Is(err, object.ErrEntryNotFound) || errors.Is(err, object.ErrDirectoryNotFound) {
			return "", nil
		}
		return "", err
	}
	return te.Hash.String(), nil
}

func commitFromObject(c *object.Commit) commitInfo {
	return commitInfo{sha: c.Hash.String(), date: c.Author.When, author: c.Author.Name, email: c.Author.Email}
}

// fillSize resolves the blob size for blob-like entries (regular files and
// symlinks).
func (b *gogitBackend) fillSize(e *entry, hash plumbing.Hash) error {
	if !e.mode.IsRegular() && e.mode&fs.ModeSymlink == 0 {
		return nil
	}
	blob, err := b.repo.BlobObject(hash)
	if err != nil {
		return err
	}
	e.size = blob.Size
	return nil
}
