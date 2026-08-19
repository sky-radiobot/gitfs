package gitfs

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/radiospiel/simple-go/src/assert"
)

const (
	fixtureHello = "hello\n"
	fixtureGo    = "package main\n"
	fixtureTool  = "#!/bin/sh\necho hi\n"
)

// gitIn runs git in dir and returns trimmed stdout.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test User",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test User",
		"GIT_COMMITTER_EMAIL=test@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// buildFixture creates a git repo with a regular file, an executable, a
// symlink, and a nested directory, plus a bare clone of it. It returns the
// repo path, the bare repo path, and the commit SHA.
func buildFixture(t *testing.T) (repo, bare, sha string) {
	t.Helper()
	repo = t.TempDir()
	gitIn(t, repo, "init", "--quiet", "--initial-branch=main")

	write := func(name, content string, mode fs.FileMode) {
		t.Helper()
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("hello.txt", fixtureHello, 0o644)
	write("sub/deep/file.go", fixtureGo, 0o644)
	write("tool.sh", fixtureTool, 0o755)
	if err := os.Symlink("hello.txt", filepath.Join(repo, "link")); err != nil {
		t.Fatal(err)
	}

	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "--quiet", "-m", "initial")
	sha = gitIn(t, repo, "rev-parse", "HEAD")

	bare = filepath.Join(t.TempDir(), "repo.git")
	gitIn(t, repo, "clone", "--quiet", "--bare", repo, bare)
	return repo, bare, sha
}

type backendCase struct {
	name string
	opts []Option
}

// backends returns both backends: the default go-git one, and the shell-out
// one using the git binary from PATH.
func backends(t *testing.T) []backendCase {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary not found")
	}
	return []backendCase{
		{"gogit", nil},
		{"exec", []Option{WithGitBinary(gitPath)}},
	}
}

func entryNames(entries []fs.DirEntry) string {
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

// TestConformance runs the stdlib fs conformance suite against both backends.
func TestConformance(t *testing.T) {
	repo, _, sha := buildFixture(t)
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			fsys, err := Open(repo, sha, be.opts...)
			assert.NoError(t, err)
			err = fstest.TestFS(fsys, "hello.txt", "link", "sub/deep/file.go", "tool.sh")
			assert.NoError(t, err)
		})
	}
}

// TestOps exercises the fs operations against both backends, on bare and
// non-bare repositories.
func TestOps(t *testing.T) {
	repo, bare, sha := buildFixture(t)
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			fsys, err := Open(repo, sha, be.opts...)
			assert.NoError(t, err)

			data, err := fsys.ReadFile("hello.txt")
			assert.NoError(t, err)
			assert.Equals(t, string(data), fixtureHello)

			data, err = fsys.ReadFile("sub/deep/file.go")
			assert.NoError(t, err)
			assert.Equals(t, string(data), fixtureGo)

			fi, err := fsys.Stat("tool.sh")
			assert.NoError(t, err)
			assert.Equals(t, fi.Mode().String(), "-rwxr-xr-x")

			fi, err = fsys.Stat("hello.txt")
			assert.NoError(t, err)
			assert.Equals(t, fi.Mode().String(), "-rw-r--r--")
			assert.Equals(t, fi.Size(), int64(len(fixtureHello)))
			assert.False(t, fi.ModTime().IsZero(), "ModTime should be the commit time")

			fi, err = fsys.Stat("sub")
			assert.NoError(t, err)
			assert.True(t, fi.IsDir())

			// Symlinks are reported but never followed; the blob content is
			// the link target.
			fi, err = fsys.Stat("link")
			assert.NoError(t, err)
			assert.True(t, fi.Mode()&fs.ModeSymlink != 0)
			data, err = fsys.ReadFile("link")
			assert.NoError(t, err)
			assert.Equals(t, string(data), "hello.txt")

			entries, err := fsys.ReadDir(".")
			assert.NoError(t, err)
			assert.Equals(t, entryNames(entries), "hello.txt,link,sub,tool.sh")

			matches, err := fsys.Glob("sub/*")
			assert.NoError(t, err)
			assert.Equals(t, strings.Join(matches, ","), "sub/deep")

			sub, err := fsys.Sub("sub/deep")
			assert.NoError(t, err)
			data, err = fs.ReadFile(sub, "file.go")
			assert.NoError(t, err)
			assert.Equals(t, string(data), fixtureGo)

			// Missing paths report ErrNotExist.
			_, err = fsys.Open("nope.txt")
			assert.True(t, errors.Is(err, fs.ErrNotExist))
			_, err = fsys.Stat("nope.txt")
			assert.True(t, errors.Is(err, fs.ErrNotExist))
			_, err = fsys.ReadDir("nope")
			assert.True(t, errors.Is(err, fs.ErrNotExist))

			// Invalid names are rejected.
			_, err = fsys.Open("../hello.txt")
			assert.True(t, errors.Is(err, fs.ErrInvalid))

			// A bare repo reads the same content.
			bareFS, err := Open(bare, sha, be.opts...)
			assert.NoError(t, err)
			data, err = bareFS.ReadFile("hello.txt")
			assert.NoError(t, err)
			assert.Equals(t, string(data), fixtureHello)
		})
	}
}

// TestPinnedSnapshot verifies that reads keep serving the commit captured at
// Open even after the branch moves on.
func TestPinnedSnapshot(t *testing.T) {
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			// Each subtest mutates its own fixture.
			repo, _, sha := buildFixture(t)
			fsys, err := Open(repo, sha, be.opts...)
			assert.NoError(t, err)

			if err := os.WriteFile(filepath.Join(repo, "hello.txt"), []byte("changed\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			gitIn(t, repo, "add", "-A")
			gitIn(t, repo, "commit", "--quiet", "-m", "change")

			data, err := fsys.ReadFile("hello.txt")
			assert.NoError(t, err)
			assert.Equals(t, string(data), fixtureHello)
		})
	}
}

func TestOpenErrors(t *testing.T) {
	repo, _, sha := buildFixture(t)

	_, err := Open(repo, "main")
	assert.Error(t, err, "not a full commit SHA")

	_, err = Open(repo, sha[:10])
	assert.Error(t, err, "not a full commit SHA")

	_, err = Open(repo, strings.Repeat("0", 40))
	assert.Error(t, err, "")

	_, err = Open(filepath.Join(t.TempDir(), "missing"), sha)
	assert.Error(t, err, "")

	_, err = Open(repo, sha, WithSparse("../etc"))
	assert.Error(t, err, "invalid sparse path")
}

// TestSparse exercises the sparse visibility filter. It lives above the
// backend interface, so one backend suffices here; the integration tests
// cover both.
func TestSparse(t *testing.T) {
	repo, _, sha := buildFixture(t)
	fsys, err := Open(repo, sha, WithSparse("sub/deep"))
	assert.NoError(t, err)

	// Only ancestors and the sparse subtree itself are visible.
	entries, err := fsys.ReadDir(".")
	assert.NoError(t, err)
	assert.Equals(t, entryNames(entries), "sub")

	entries, err = fsys.ReadDir("sub")
	assert.NoError(t, err)
	assert.Equals(t, entryNames(entries), "deep")

	entries, err = fsys.ReadDir("sub/deep")
	assert.NoError(t, err)
	assert.Equals(t, entryNames(entries), "file.go")

	// Paths outside the sparse set behave as if absent.
	_, err = fsys.Open("hello.txt")
	assert.True(t, errors.Is(err, fs.ErrNotExist))
	_, err = fsys.Stat("hello.txt")
	assert.True(t, errors.Is(err, fs.ErrNotExist))

	// Paths inside the sparse set work.
	data, err := fsys.ReadFile("sub/deep/file.go")
	assert.NoError(t, err)
	assert.Equals(t, string(data), fixtureGo)

	// Sub into a hidden path fails; Sub into an ancestor keeps filtering.
	_, err = fsys.Sub("hello.txt")
	assert.True(t, errors.Is(err, fs.ErrNotExist))
	sub, err := fsys.Sub("sub")
	assert.NoError(t, err)
	entries, err = fs.ReadDir(sub, ".")
	assert.NoError(t, err)
	assert.Equals(t, entryNames(entries), "deep")

	// Glob inherits the filter through ReadDir.
	matches, err := fsys.Glob("*")
	assert.NoError(t, err)
	assert.Equals(t, strings.Join(matches, ","), "sub")
	matches, err = fsys.Glob("sub/deep/*")
	assert.NoError(t, err)
	assert.Equals(t, strings.Join(matches, ","), "sub/deep/file.go")
}

// buildHistoryFixture creates a git repo with two commits: the first adds
// old.txt and touched.txt, the second changes only touched.txt. It returns
// the repo path and both commit SHAs.
func buildHistoryFixture(t *testing.T) (repo, sha1, sha2 string) {
	t.Helper()
	repo = t.TempDir()
	gitIn(t, repo, "init", "--quiet", "--initial-branch=main")

	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("old.txt", "old\n")
	write("touched.txt", "v1\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "--quiet", "-m", "first")
	sha1 = gitIn(t, repo, "rev-parse", "HEAD")

	write("touched.txt", "v2\n")
	gitIn(t, repo, "add", "-A")
	gitIn(t, repo, "commit", "--quiet", "-m", "second")
	sha2 = gitIn(t, repo, "rev-parse", "HEAD")

	return repo, sha1, sha2
}

// TestExtendedStats exercises WithExtendedStats against both backends.
func TestExtendedStats(t *testing.T) {
	repo, sha1, sha2 := buildHistoryFixture(t)
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			// Without WithExtendedStats, Sys() is nil.
			fsys, err := Open(repo, sha2, be.opts...)
			assert.NoError(t, err)
			fi, err := fsys.Stat("touched.txt")
			assert.NoError(t, err)
			assert.Nil(t, fi.Sys())

			opts := append(append([]Option{}, be.opts...), WithExtendedStats(-1))
			fsys, err = Open(repo, sha2, opts...)
			assert.NoError(t, err)

			// touched.txt was changed by sha2 itself.
			fi, err = fsys.Stat("touched.txt")
			assert.NoError(t, err)
			es, ok := fi.Sys().(*ExtendedStat)
			assert.True(t, ok, "Sys() should be *ExtendedStat")
			assert.NoError(t, es.Err)
			assert.Equals(t, es.Commit, sha2)
			assert.Equals(t, es.Author, "Test User")
			assert.Equals(t, es.AuthorEmail, "test@example.com")
			assert.False(t, es.Date.IsZero())

			// old.txt was last touched by sha1 and never changed again.
			fi, err = fsys.Stat("old.txt")
			assert.NoError(t, err)
			es = fi.Sys().(*ExtendedStat)
			assert.Equals(t, es.Commit, sha1)

			// The root has no history of its own; it reports the pinned
			// commit directly.
			fi, err = fsys.Stat(".")
			assert.NoError(t, err)
			es = fi.Sys().(*ExtendedStat)
			assert.Equals(t, es.Commit, sha2)

			// ReadDir entries carry it too, not just Stat/Open results.
			entries, err := fsys.ReadDir(".")
			assert.NoError(t, err)
			for _, e := range entries {
				info, err := e.Info()
				assert.NoError(t, err)
				_, ok := info.Sys().(*ExtendedStat)
				assert.True(t, ok, "ReadDir entry Sys() should be *ExtendedStat")
			}
		})
	}
}

// TestExtendedStatsMaxCommitsFallback verifies that a search window too
// small to reach the true last-touching commit falls back to the pinned
// commit itself, never something older found outside the window and never
// something newer than the pinned commit.
func TestExtendedStatsMaxCommitsFallback(t *testing.T) {
	repo, _, sha2 := buildHistoryFixture(t)
	for _, be := range backends(t) {
		t.Run(be.name, func(t *testing.T) {
			opts := append(append([]Option{}, be.opts...), WithExtendedStats(1))
			fsys, err := Open(repo, sha2, opts...)
			assert.NoError(t, err)

			// old.txt was last touched by the first commit, one commit
			// back from sha2; a 1-commit search window can't reach it.
			fi, err := fsys.Stat("old.txt")
			assert.NoError(t, err)
			es := fi.Sys().(*ExtendedStat)
			assert.Equals(t, es.Commit, sha2)
		})
	}
}

func TestModeFromGit(t *testing.T) {
	assert.Equals(t, modeFromGit(0o040000), fs.ModeDir|0o755)
	assert.Equals(t, modeFromGit(0o100644), fs.FileMode(0o644))
	assert.Equals(t, modeFromGit(0o100664), fs.FileMode(0o644))
	assert.Equals(t, modeFromGit(0o100755), fs.FileMode(0o755))
	assert.Equals(t, modeFromGit(0o120000), fs.ModeSymlink|0o777)
	assert.Equals(t, modeFromGit(0o160000), fs.ModeIrregular)
}
