// Package benchmarks holds gitfs performance benchmarks that need a real,
// sizeable repository to be meaningful — too heavy and too
// environment-dependent for the main package's fast unit test suite. See
// RESULTS.md for the latest recorded numbers and how to reproduce them.
package benchmarks

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sky-radiobot/gitfs"
)

// benchRepo returns a real, sizeable repo to benchmark against
// (~/projects/critic: 816 commits, 91MB .git, 486 tracked files) and its
// HEAD SHA, skipping if it isn't present on this machine.
func benchRepo(b *testing.B) (repo, sha string) {
	b.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		b.Skip(err)
	}
	repo = filepath.Join(home, "projects", "critic")
	if _, err := os.Stat(repo); err != nil {
		b.Skip("~/projects/critic not present")
	}
	out, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		b.Skip(err)
	}
	return repo, strings.TrimSpace(string(out))
}

// backendCase is a backend variant to run a benchmark under: a name for
// b.Run, and the gitfs.Option list that selects it.
type backendCase struct {
	name string
	opts []gitfs.Option
}

func benchBackends(b *testing.B) []backendCase {
	b.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		b.Skip("git binary not found")
	}
	return []backendCase{
		{"gogit", nil},
		{"exec", []gitfs.Option{gitfs.WithGitBinary(gitPath)}},
	}
}

// benchBlameBackends is benchBackends plus a third variant: the go-git
// backend with WithBlameFallback enabled, so extended-stats benchmarks
// can compare all three: pure-Go, plain shell-out, and go-git-with-
// git-binary-fallback-for-blame-only.
func benchBlameBackends(b *testing.B) []backendCase {
	b.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		b.Skip("git binary not found")
	}
	return append(benchBackends(b), backendCase{"gogit+fallback", []gitfs.Option{gitfs.WithBlameFallback(gitPath)}})
}

func BenchmarkReadDirRoot(b *testing.B) {
	repo, sha := benchRepo(b)
	for _, be := range benchBackends(b) {
		b.Run(be.name, func(b *testing.B) {
			fsys, err := gitfs.Open(repo, sha, be.opts...)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := fsys.ReadDir("."); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReadDirDeep(b *testing.B) {
	repo, sha := benchRepo(b)
	for _, be := range benchBackends(b) {
		b.Run(be.name, func(b *testing.B) {
			fsys, err := gitfs.Open(repo, sha, be.opts...)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 167 entries, the largest directory in the repo.
				if _, err := fsys.ReadDir("agents/logs"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReadFileLarge(b *testing.B) {
	repo, sha := benchRepo(b)
	for _, be := range benchBackends(b) {
		b.Run(be.name, func(b *testing.B) {
			fsys, err := gitfs.Open(repo, sha, be.opts...)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// The largest tracked file, ~188KiB.
				if _, err := fsys.ReadFile("src/api/critic.pb.go"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGlob(b *testing.B) {
	repo, sha := benchRepo(b)
	for _, be := range benchBackends(b) {
		b.Run(be.name, func(b *testing.B) {
			fsys, err := gitfs.Open(repo, sha, be.opts...)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := fsys.Glob("src/*/*.go"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// statExtended opens fsys with gitfs.WithExtendedStats(maxCommits) and
// stats path, failing the benchmark on any error.
func statExtended(b *testing.B, repo, sha, path string, maxCommits int, opts []gitfs.Option) {
	b.Helper()
	allOpts := append(append([]gitfs.Option{}, opts...), gitfs.WithExtendedStats(maxCommits))
	fsys, err := gitfs.Open(repo, sha, allOpts...)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		fi, err := fsys.Stat(path)
		if err != nil {
			b.Fatal(err)
		}
		es, _ := fi.Sys().(*gitfs.ExtendedStat)
		if es == nil || es.Err != nil {
			b.Fatalf("bad extended stat: %+v", es)
		}
	}
}

// BenchmarkExtendedStatsDeepUnbounded blames plans/refactor.md, whose
// last-touching commit is 778 commits back from HEAD (out of 816 total)
// -- a deliberately deep, unbounded search: gogitBackend must walk
// first-parent history commit by commit; execBackend runs one unbounded
// `git log -- plans/refactor.md`.
func BenchmarkExtendedStatsDeepUnbounded(b *testing.B) {
	repo, sha := benchRepo(b)
	for _, be := range benchBlameBackends(b) {
		b.Run(be.name, func(b *testing.B) {
			statExtended(b, repo, sha, "plans/refactor.md", -1, be.opts)
		})
	}
}

// BenchmarkExtendedStatsDeepBounded is the same file, but bounded well
// short of its true last-touching commit (20 < 778), exercising the
// fallback-to-pinned-commit path instead of a full walk.
func BenchmarkExtendedStatsDeepBounded(b *testing.B) {
	repo, sha := benchRepo(b)
	for _, be := range benchBlameBackends(b) {
		b.Run(be.name, func(b *testing.B) {
			statExtended(b, repo, sha, "plans/refactor.md", 20, be.opts)
		})
	}
}

// BenchmarkExtendedStatsShallow blames go.mod, last touched only 13
// commits back -- the fast path, found almost immediately regardless of
// backend.
func BenchmarkExtendedStatsShallow(b *testing.B) {
	repo, sha := benchRepo(b)
	for _, be := range benchBlameBackends(b) {
		b.Run(be.name, func(b *testing.B) {
			statExtended(b, repo, sha, "go.mod", -1, be.opts)
		})
	}
}
