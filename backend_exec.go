package gitfs

import (
	"bytes"
	"fmt"
	"io/fs"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"
)

// execBackend reads git objects by shelling out to a git binary. It is used
// only when WithGitBinary is set. All commands run with --git-dir, so bare
// and non-bare repositories behave identically and the working tree is
// never consulted.
type execBackend struct {
	binary string
	gitDir string
	sha    string
}

func (b *execBackend) open(repoPath string) error {
	out, err := b.run("-C", repoPath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	b.gitDir = strings.TrimSpace(string(out))
	return nil
}

func (b *execBackend) pin(sha string) (time.Time, error) {
	// <sha>^{commit} fails for anything that is not, or does not peel to, a
	// commit; the output is the committer timestamp.
	out, err := b.git("show", "-s", "--format=%ct", sha+"^{commit}")
	if err != nil {
		return time.Time{}, err
	}
	sec, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	b.sha = sha
	return time.Unix(sec, 0), nil
}

func (b *execBackend) lookup(p string) (entry, error) {
	out, err := b.git("ls-tree", "--long", "-z", b.sha, "--", p)
	if err != nil {
		return entry{}, err
	}
	entries, err := parseLsTree(out)
	if err != nil {
		return entry{}, err
	}
	if len(entries) == 0 {
		return entry{}, fs.ErrNotExist
	}
	e := entries[0]
	e.name = path.Base(p)
	return e, nil
}

func (b *execBackend) list(p string) ([]entry, error) {
	treeish := b.sha
	if p != "" {
		treeish += ":" + p
	}
	out, err := b.git("ls-tree", "--long", "-z", treeish)
	if err != nil {
		return nil, err
	}
	return parseLsTree(out)
}

func (b *execBackend) readBlob(hash string) ([]byte, error) {
	return b.git("cat-file", "blob", hash)
}

func (b *execBackend) lastCommit(p string, maxCommits int) (commitInfo, error) {
	if p == "" {
		// The root has no meaningful "last touched" commit of its own;
		// report the pinned commit directly rather than defining one.
		return mustLogOneCommit(b.git, b.sha)
	}

	revRange := b.sha
	if maxCommits >= 0 {
		if _, err := b.git("rev-parse", "--verify", fmt.Sprintf("%s~%d", b.sha, maxCommits)); err == nil {
			revRange = fmt.Sprintf("%s~%d..%s", b.sha, maxCommits, b.sha)
		}
		// else: the pinned commit has fewer than maxCommits ancestors;
		// search all of them, still within budget.
	}
	ci, ok, err := logOneCommit(b.git, revRange, p)
	if err != nil {
		return commitInfo{}, err
	}
	if ok {
		return ci, nil
	}
	// Nothing found within the bound: report the pinned commit itself
	// (not a further, unbounded search), matching gogitBackend.
	return mustLogOneCommit(b.git, b.sha)
}

// git runs a command against the resolved git directory.
func (b *execBackend) git(args ...string) ([]byte, error) {
	return b.run(append([]string{"--git-dir=" + b.gitDir}, args...)...)
}

func (b *execBackend) run(args ...string) ([]byte, error) {
	cmd := exec.Command(b.binary, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

// parseLsTree parses NUL-terminated `git ls-tree --long -z` output:
// "<mode> <type> <hash> <size>\t<name>\x00" (size is "-" for non-blobs).
func parseLsTree(out []byte) ([]entry, error) {
	var entries []entry
	for len(out) > 0 {
		i := bytes.IndexByte(out, 0)
		if i < 0 {
			return nil, fmt.Errorf("gitfs: malformed ls-tree output")
		}
		rec := string(out[:i])
		out = out[i+1:]

		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("gitfs: malformed ls-tree record %q", rec)
		}
		meta := strings.Fields(rec[:tab])
		if len(meta) != 4 {
			return nil, fmt.Errorf("gitfs: malformed ls-tree record %q", rec)
		}
		m, err := strconv.ParseUint(meta[0], 8, 32)
		if err != nil {
			return nil, fmt.Errorf("gitfs: malformed ls-tree mode in %q", rec)
		}
		var size int64
		if meta[3] != "-" {
			size, err = strconv.ParseInt(meta[3], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("gitfs: malformed ls-tree size in %q", rec)
			}
		}
		entries = append(entries, entry{
			name: rec[tab+1:],
			mode: modeFromGit(uint32(m)),
			hash: meta[2],
			size: size,
		})
	}
	return entries, nil
}
