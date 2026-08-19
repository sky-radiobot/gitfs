// Command gitfs is a minimal CLI frontend to the gitfs package. It mainly
// exists as the executable surface for the bash integration tests, and is
// handy for debugging.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strings"

	"gitfs"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gitfs [-git-binary PATH] [-sparse p1,p2] REF OP [ARGS]")
	fmt.Fprintln(os.Stderr, "ops:   cat PATH [PATH...] | ls [-l] [PATH...]")
	fmt.Fprintln(os.Stderr, "REF is a full commit SHA or anything the git CLI can resolve to one")
	fmt.Fprintln(os.Stderr, "(branch, tag, short SHA, HEAD, ...). The repository is discovered")
	fmt.Fprintln(os.Stderr, "from the current directory, the same way git itself does.")
	os.Exit(2)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gitfs:", err)
	os.Exit(1)
}

func main() {
	gitBinaryFlag := flag.String("git-binary", "", "shell out to the git binary at PATH instead of using go-git")
	sparse := flag.String("sparse", "", "comma-separated repo-relative paths to restrict the filesystem to")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) < 2 {
		usage()
	}
	ref, op, opArgs := args[0], args[1], args[2:]

	gitBin := *gitBinaryFlag
	if gitBin == "" {
		gitBin = "git"
	}

	repoPath, bare, err := discoverRepo(gitBin)
	if err != nil {
		fatal(err)
	}
	sha, err := resolveSHA(gitBin, repoPath, ref)
	if err != nil {
		fatal(err)
	}
	prefix, err := repoPrefix(gitBin, bare)
	if err != nil {
		fatal(err)
	}

	var opts []gitfs.Option
	if *gitBinaryFlag != "" {
		opts = append(opts, gitfs.WithGitBinary(*gitBinaryFlag))
	}
	if *sparse != "" {
		opts = append(opts, gitfs.WithSparse(strings.Split(*sparse, ",")...))
	}

	fsys, err := gitfs.Open(repoPath, sha, opts...)
	if err != nil {
		fatal(err)
	}

	switch op {
	case "cat":
		runCat(fsys, prefix, opArgs)
	case "ls":
		runLs(fsys, prefix, opArgs)
	default:
		usage()
	}
}

func runCat(fsys *gitfs.GitFS, prefix string, paths []string) {
	if len(paths) == 0 {
		usage()
	}
	for _, p := range paths {
		data, err := fsys.ReadFile(path.Join(prefix, p))
		if err != nil {
			fatal(err)
		}
		if _, err := os.Stdout.Write(data); err != nil {
			fatal(err)
		}
	}
}

func runLs(fsys *gitfs.GitFS, prefix string, args []string) {
	lsFlags := flag.NewFlagSet("ls", flag.ExitOnError)
	long := lsFlags.Bool("l", false, "long format: mode, size, name")
	lsFlags.Usage = usage
	lsFlags.Parse(args)

	paths := lsFlags.Args()
	if len(paths) == 0 {
		paths = []string{"."}
	}

	for i, p := range paths {
		entries, err := fsys.ReadDir(path.Join(prefix, p))
		if err != nil {
			fatal(err)
		}
		if len(paths) > 1 {
			if i > 0 {
				fmt.Println()
			}
			fmt.Printf("%s:\n", p)
		}
		for _, e := range entries {
			if *long {
				info, err := e.Info()
				if err != nil {
					fatal(err)
				}
				printInfo(info)
			} else {
				fmt.Println(e.Name())
			}
		}
	}
}

// printInfo prints "<mode>\t<size>\t<name>".
func printInfo(fi fs.FileInfo) {
	fmt.Printf("%s\t%d\t%s\n", fi.Mode(), fi.Size(), fi.Name())
}

// discoverRepo locates the repository containing the current directory, the
// same way git itself does: the working-tree root, or the bare repository
// directory itself when run from inside one.
func discoverRepo(gitBin string) (repoPath string, bare bool, err error) {
	out, err := runGit(gitBin, ".", "rev-parse", "--is-bare-repository")
	if err != nil {
		return "", false, fmt.Errorf("not a git repository: %s", err)
	}
	bare = out == "true"
	if bare {
		repoPath, err = runGit(gitBin, ".", "rev-parse", "--absolute-git-dir")
	} else {
		repoPath, err = runGit(gitBin, ".", "rev-parse", "--show-toplevel")
	}
	return repoPath, bare, err
}

// repoPrefix returns the current directory's path relative to the repo
// root, in git's own pathspec-prefix form (e.g. "src/app/", or "" at the
// root), so user-given paths (relative to cwd, like plain cat/ls) can be
// translated into repo-root-relative gitfs paths. Bare repos have no
// working tree, so there is no meaningful cwd-relative prefix.
func repoPrefix(gitBin string, bare bool) (string, error) {
	if bare {
		return "", nil
	}
	return runGit(gitBin, ".", "rev-parse", "--show-prefix")
}

// resolveSHA returns ref as-is if it already looks like a full commit SHA;
// otherwise it asks the git CLI to resolve it against repoPath, failing if
// ref cannot be resolved to a commit.
func resolveSHA(gitBin, repoPath, ref string) (string, error) {
	if looksLikeFullSHA(ref) {
		return ref, nil
	}
	sha, err := runGit(gitBin, repoPath, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve %q to a commit: %s", ref, err)
	}
	return sha, nil
}

// looksLikeFullSHA reports whether s is a full 40-hex object id.
func looksLikeFullSHA(s string) bool {
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

// runGit runs the git binary with -C dir plus args, returning trimmed
// stdout. On failure the error carries git's stderr.
func runGit(gitBin, dir string, args ...string) (string, error) {
	cmd := exec.Command(gitBin, append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}
