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
	repo *git.Repository
	tree *object.Tree
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
