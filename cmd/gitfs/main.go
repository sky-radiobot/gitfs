// Command gitfs is a minimal CLI frontend to the gitfs package. It mainly
// exists as the executable surface for the bash integration tests, and is
// handy for debugging.
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"gitfs"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gitfs:", err)
	os.Exit(1)
}

func main() {
	var sparse string

	root := &cobra.Command{
		Use:   "gitfs",
		Short: "Read a git commit's tree without checking it out",
		Long: "REF is a full commit SHA or anything the git CLI can resolve to one\n" +
			"(branch, tag, short SHA, HEAD, ...). The repository is discovered\n" +
			"from the current directory, the same way git itself does.\n\n" +
			"Set GIT_BINARY to shell out to a specific git binary instead of using\n" +
			"the built-in go-git backend.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&sparse, "sparse", "", "comma-separated repo-relative paths to restrict the filesystem to")
	root.AddCommand(catCommand(&sparse), lsCommand(&sparse))

	if err := root.Execute(); err != nil {
		fatal(err)
	}
}

func catCommand(sparse *string) *cobra.Command {
	return &cobra.Command{
		Use:   "cat REF PATH [PATH...]",
		Short: "print the content of one or more files at REF",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			fsys, prefix, err := openFS(args[0], *sparse)
			if err != nil {
				return err
			}
			for _, p := range args[1:] {
				data, err := fsys.ReadFile(path.Join(prefix, p))
				if err != nil {
					return err
				}
				if _, err := os.Stdout.Write(data); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func lsCommand(sparse *string) *cobra.Command {
	var long bool
	cmd := &cobra.Command{
		Use:   "ls REF [PATH...]",
		Short: "list directory entries at REF",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			fsys, prefix, err := openFS(args[0], *sparse)
			if err != nil {
				return err
			}
			paths := args[1:]
			if len(paths) == 0 {
				paths = []string{"."}
			}
			for i, p := range paths {
				entries, err := fsys.ReadDir(path.Join(prefix, p))
				if err != nil {
					return err
				}
				if len(paths) > 1 {
					if i > 0 {
						fmt.Println()
					}
					fmt.Printf("%s:\n", p)
				}
				for _, e := range entries {
					if long {
						info, err := e.Info()
						if err != nil {
							return err
						}
						printInfo(info)
					} else {
						fmt.Println(e.Name())
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVarP(&long, "long", "l", false, "long format: mode, size, name")
	return cmd
}

// printInfo prints "<mode>\t<size>\t<name>".
func printInfo(fi fs.FileInfo) {
	fmt.Printf("%s\t%d\t%s\n", fi.Mode(), fi.Size(), fi.Name())
}

// openFS resolves ref against the repository discovered from the current
// directory and opens the pinned GitFS, returning it along with the
// repo-root-relative prefix for the current directory (see repoPrefix).
// GIT_BINARY, if set, selects the shell-out backend; otherwise gitfs reads
// via the pure-Go go-git backend, though GIT_BINARY (or "git" on PATH) is
// still used for the CLI's own ref resolution and repo discovery.
func openFS(ref, sparse string) (*gitfs.GitFS, string, error) {
	gitBinaryEnv := os.Getenv("GIT_BINARY")
	gitBin := gitBinaryEnv
	if gitBin == "" {
		gitBin = "git"
	}

	repoPath, bare, err := discoverRepo(gitBin)
	if err != nil {
		return nil, "", err
	}
	sha, err := resolveSHA(gitBin, repoPath, ref)
	if err != nil {
		return nil, "", err
	}
	prefix, err := repoPrefix(gitBin, bare)
	if err != nil {
		return nil, "", err
	}

	var opts []gitfs.Option
	if gitBinaryEnv != "" {
		opts = append(opts, gitfs.WithGitBinary(gitBinaryEnv))
	}
	if sparse != "" {
		opts = append(opts, gitfs.WithSparse(strings.Split(sparse, ",")...))
	}

	fsys, err := gitfs.Open(repoPath, sha, opts...)
	if err != nil {
		return nil, "", err
	}
	return fsys, prefix, nil
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
