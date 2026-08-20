// Command git-ls provides `git ls`: the gitfs ls subcommand pinned to the
// current checkout (HEAD). With the git-ls binary on PATH, git dispatches
// `git ls` to it.
//
//	 git ls [PATH...]           ≡  gitfs ls HEAD [PATH...]
//	 git ls -l|--long [PATH...] ≡  gitfs ls HEAD --blame=N [PATH...]
//
// N is the blame search depth from `git config gitls.blameLimit`
// (default 1000); an explicit --blame[=LIMIT] overrides it.
//
// The ls implementation below is a deliberate copy of cmd/gitfs's ls
// subcommand (the two binaries share no package by decision); keep them in
// sync.
package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"gitfs"
)

func fatal(cmd *cobra.Command, err error) {
	fmt.Fprintln(os.Stderr, err)
	fmt.Fprintf(os.Stderr, "run %q for more information\n", cmd.CommandPath()+" --help")
	os.Exit(1)
}

func main() {
	var sparse string
	var long bool
	var blameLimitFlag string

	root := &cobra.Command{
		Use:   "git-ls [PATH...]",
		Short: "list directory entries of the current checkout (HEAD)",
		Long: "list directory entries of the current checkout (HEAD)\n\n" +
			"Plain output is names only; -l/--long adds, for each file, the last\n" +
			"commit (at or before HEAD) that touched it: its author's email, date,\n" +
			"and short SHA. The search depth defaults to 1000 ancestor commits and\n" +
			"is configurable via `git config gitls.blameLimit`; an explicit\n" +
			"--blame[=LIMIT] overrides it (unbounded if given with no value),\n" +
			"falling back to HEAD's own commit if no match turns up within the\n" +
			"window -- never a commit newer than HEAD, but possibly an imprecise\n" +
			"one.\n\n" +
			"The repository is discovered from the current directory, the same way\n" +
			"git itself does.\n\n" +
			"Set GIT_BINARY to shell out to a specific git binary instead of using\n" +
			"the built-in go-git backend.",
		Args:          cobra.ArbitraryArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			blame := cmd.Flags().Changed("blame")
			blameLimit := unboundedBlame
			switch {
			case blame && blameLimitFlag == "unbounded":
				// unbounded ancestor search
			case blame:
				var err error
				blameLimit, err = strconv.Atoi(blameLimitFlag)
				if err != nil || blameLimit < 0 {
					return fmt.Errorf("invalid --blame limit %q: must be a non-negative integer", blameLimitFlag)
				}
			case long:
				// -l implies the blame listing, with the search depth from
				// `git config gitls.blameLimit`.
				var err error
				blameLimit, err = configuredBlameLimit()
				if err != nil {
					return err
				}
				blame = true
			}
			if blame {
				long = true
			}

			var extraOpts []gitfs.Option
			if blame {
				extraOpts = append(extraOpts, gitfs.WithExtendedStats(blameLimit))
			}
			tg, err := openFS("HEAD", sparse, extraOpts...)
			if err != nil {
				return err
			}

			paths := args
			if len(paths) == 0 {
				paths = []string{"."}
			}
			targets, err := expandGlobs(tg.fsys, tg.prefix, paths)
			if err != nil {
				return err
			}

			printedAny := false
			printHeader := func(name string) {
				if len(targets) > 1 {
					if printedAny {
						fmt.Println()
					}
					fmt.Printf("%s:\n", name)
				}
			}
			// sizeCol is the row index of the size column, right-aligned by
			// printTable; -1 (plain, name-only rows) means no such column.
			sizeCol := -1
			switch {
			case blame:
				sizeCol = 2
			case long:
				sizeCol = 1
			}
			var ghRepo string
			var hasGH, canHyperlink bool
			if blame {
				canHyperlink = terminalSupportsHyperlinks()
				if canHyperlink {
					ghRepo, hasGH = githubRepo(tg.gitBin, tg.repoPath)
				}
			}
			row := func(displayName string, info fs.FileInfo) ([]tableCell, error) {
				switch {
				case !long:
					return []tableCell{cell(displayName)}, nil
				case !blame:
					return []tableCell{cell(info.Mode().String()), cell(strconv.FormatInt(info.Size(), 10)), cell(lsDate(info.ModTime())), cell(displayName)}, nil
				default:
					es, _ := info.Sys().(*gitfs.ExtendedStat)
					if es == nil {
						return nil, fmt.Errorf("gitfs: no extended stats for %s", displayName)
					}
					if es.Err != nil {
						return nil, es.Err
					}
					short := shortSHA(es.Commit)
					commitCell := cell(short)
					if hasGH && reachableFromOrigin(tg.gitBin, tg.repoPath, es.Commit) {
						commitCell = hyperlink(githubCommitURL(ghRepo, es.Commit), short)
					}
					authorCell := cell(es.AuthorEmail)
					if canHyperlink {
						if username, ok := githubUsernameFromNoreplyEmail(es.AuthorEmail); ok {
							authorCell = hyperlink(githubProfileURL(username), username)
						}
					}
					return []tableCell{cell(info.Mode().String()), authorCell, cell(strconv.FormatInt(info.Size(), 10)), cell(lsDate(es.Date)), commitCell, cell(displayName)}, nil
				}
			}

			for _, t := range targets {
				info, err := tg.fsys.Stat(t)
				if err != nil {
					return err
				}
				if !info.IsDir() {
					r, err := row(displayPath(tg.prefix, t), info)
					if err != nil {
						return err
					}
					printTable([][]tableCell{r}, sizeCol)
					printedAny = true
					continue
				}
				entries, err := tg.fsys.ReadDir(t)
				if err != nil {
					return err
				}
				printHeader(displayPath(tg.prefix, t))
				rows := make([][]tableCell, 0, len(entries))
				for _, e := range entries {
					entryInfo, err := e.Info()
					if err != nil {
						return err
					}
					r, err := row(e.Name(), entryInfo)
					if err != nil {
						return err
					}
					rows = append(rows, r)
				}
				printTable(rows, sizeCol)
				printedAny = true
			}
			return nil
		},
	}
	root.Flags().BoolVarP(&long, "long", "l", false, "long format with blame columns: mode, author email, size, date, commit, name (search depth from git config gitls.blameLimit, default 1000)")
	root.Flags().StringVar(&blameLimitFlag, "blame", "", "show the last commit that touched each file; optional LIMIT bounds the ancestor-commit search depth (unbounded if given with no value)")
	root.Flags().Lookup("blame").NoOptDefVal = "unbounded"
	root.Flags().StringVar(&sparse, "sparse", "", "comma-separated repo-relative paths to restrict the filesystem to")

	executedCmd, err := root.ExecuteC()
	if err != nil {
		fatal(executedCmd, err)
	}
}

// configuredBlameLimit reads the -l blame search depth from
// `git config gitls.blameLimit` (default 1000). --type=int makes git itself
// reject non-numeric values; --default applies only when the key is unset.
func configuredBlameLimit() (int, error) {
	gitBin := os.Getenv("GIT_BINARY")
	if gitBin == "" {
		gitBin = "git"
	}
	out, err := runGit(gitBin, ".", "config", "--type=int", "--get", "--default=1000", "gitls.blameLimit")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(out)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid gitls.blameLimit value %q: must be a non-negative integer", out)
	}
	return n, nil
}

// unboundedBlame is the NoOptDefVal for --blame: given with no "=LIMIT",
// the ancestor-commit search is unbounded.
const unboundedBlame = -1

// lsDate formats t the way plain `ls -l` does: "Aug 19 22:03" for
// timestamps within the last ~6 months, or "Oct 18  2024" (year instead of
// time, note the extra space keeping columns aligned) for anything older,
// or in the future.
func lsDate(t time.Time) string {
	const sixMonths = 6 * 30 * 24 * time.Hour
	now := time.Now()
	if t.After(now.Add(-sixMonths)) && !t.After(now) {
		return t.Format("Jan _2 15:04")
	}
	return t.Format("Jan _2  2006")
}

// tableCell is one column's value for printTable: text is what's printed,
// width is what's used for column alignment. These differ for values like
// terminal hyperlinks (see hyperlink), whose printed bytes include
// invisible escape sequences that must not count toward column width.
type tableCell struct {
	text  string
	width int
}

// cell wraps a plain string as a tableCell whose alignment width is its
// own length.
func cell(s string) tableCell { return tableCell{text: s, width: len(s)} }

// hyperlink wraps text in an OSC 8 terminal hyperlink escape sequence
// pointing at url, so supporting terminals render it as a clickable link
// while displaying only text. Its alignment width is text's length, not
// the escaped string's byte length.
func hyperlink(url, text string) tableCell {
	return tableCell{text: "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\", width: len(text)}
}

// terminalSupportsHyperlinks reports whether stdout looks like a terminal
// that would render OSC 8 hyperlinks usefully. Piped/redirected output
// (not a TTY) never gets them, since the raw escape bytes would leak into
// whatever consumes the output; TERM=dumb (or unset) doesn't either. Most
// modern terminals either render OSC 8 as a clickable link or silently
// ignore the unrecognized escape sequence, so this errs toward enabling
// it rather than maintaining an exhaustive allowlist of known-good
// terminals. GITFS_FORCE_HYPERLINKS=1 overrides all of the above, for
// cases like piping through `less -R` (which preserves escape sequences)
// where output genuinely isn't a TTY but should still get them.
func terminalSupportsHyperlinks() bool {
	if os.Getenv("GITFS_FORCE_HYPERLINKS") == "1" {
		return true
	}
	info, err := os.Stdout.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	term := os.Getenv("TERM")
	return term != "" && term != "dumb"
}

// printTable prints rows as space-aligned columns (no tabs): each column
// is padded to its max width across rows, plus one space of separation.
// sizeCol, if >= 0, is right-aligned (like ls -l's size column); every
// other column is left-aligned. The last column (name) is never padded,
// since it has nothing after it to align with.
func printTable(rows [][]tableCell, sizeCol int) {
	if len(rows) == 0 {
		return
	}
	numCols := len(rows[0])
	widths := make([]int, numCols)
	for _, r := range rows {
		for i, c := range r {
			if c.width > widths[i] {
				widths[i] = c.width
			}
		}
	}
	for _, r := range rows {
		var b strings.Builder
		for i, c := range r {
			pad := strings.Repeat(" ", widths[i]-c.width)
			switch {
			case i == numCols-1:
				b.WriteString(c.text)
			case i == sizeCol:
				b.WriteString(pad)
				b.WriteString(c.text)
				b.WriteByte(' ')
			default:
				b.WriteString(c.text)
				b.WriteString(pad)
				b.WriteByte(' ')
			}
		}
		fmt.Println(b.String())
	}
}

// expandGlobs resolves each of paths (repo-root-relative once joined with
// prefix) via fs.Glob, so path.Match-style patterns like "sc*" work
// alongside plain literal paths; Glob treats a pattern with no metachars as
// a plain existence check, so this is backward compatible with exact
// paths. Each pattern must match at least one entry.
func expandGlobs(fsys *gitfs.GitFS, prefix string, paths []string) ([]string, error) {
	var targets []string
	for _, p := range paths {
		matches, err := fsys.Glob(path.Join(prefix, p))
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, &fs.PathError{Op: "glob", Path: p, Err: fs.ErrNotExist}
		}
		targets = append(targets, matches...)
	}
	return targets, nil
}

// displayPath renders a repo-root-relative path back into cwd-relative
// form (undoing the path.Join(prefix, ...) expandGlobs did), so ls's
// headers and single-file names match what the user typed rather than the
// full repo-relative path glob matches resolve to. Falls back to the
// repo-relative form for matches outside prefix's subtree (e.g. a ".."
// pattern), since there's no shorter cwd-relative spelling for those.
func displayPath(prefix, repoRelative string) string {
	switch {
	case prefix == "":
		return repoRelative
	case repoRelative == prefix:
		return "."
	default:
		if rest, ok := strings.CutPrefix(repoRelative, prefix+"/"); ok {
			return rest
		}
		return repoRelative
	}
}

// target bundles what ls needs once ref has been resolved: the pinned
// GitFS, the cwd's repo-root-relative prefix, and (for --blame's GitHub
// link rendering, which needs its own git calls alongside the GitFS reads)
// the git binary and repo path.
type target struct {
	fsys     *gitfs.GitFS
	prefix   string
	gitBin   string
	repoPath string
}

// openFS resolves ref against the repository discovered from the current
// directory and opens the pinned GitFS, with any extraOpts (e.g.
// gitfs.WithExtendedStats) applied alongside GIT_BINARY/--sparse.
// GIT_BINARY, if set, selects the shell-out backend; otherwise gitfs reads
// via the pure-Go go-git backend, though GIT_BINARY (or "git" on PATH) is
// still used for the CLI's own ref resolution and repo discovery.
func openFS(ref, sparse string, extraOpts ...gitfs.Option) (*target, error) {
	gitBinaryEnv := os.Getenv("GIT_BINARY")
	gitBin := gitBinaryEnv
	if gitBin == "" {
		gitBin = "git"
	}

	repoPath, bare, err := discoverRepo(gitBin)
	if err != nil {
		return nil, err
	}
	sha, err := resolveSHA(gitBin, repoPath, ref)
	if err != nil {
		return nil, err
	}
	prefix, err := repoPrefix(gitBin, bare)
	if err != nil {
		return nil, err
	}

	var opts []gitfs.Option
	if gitBinaryEnv != "" {
		opts = append(opts, gitfs.WithGitBinary(gitBinaryEnv))
	}
	if sparse != "" {
		opts = append(opts, gitfs.WithSparse(strings.Split(sparse, ",")...))
	}
	opts = append(opts, extraOpts...)

	fsys, err := gitfs.Open(repoPath, sha, opts...)
	if err != nil {
		return nil, err
	}
	return &target{fsys: fsys, prefix: prefix, gitBin: gitBin, repoPath: repoPath}, nil
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

// githubRepo returns "owner/repo" if the origin remote's URL points at
// github.com (SSH or HTTPS form), and ok=false otherwise — no origin
// remote, or a non-GitHub host.
func githubRepo(gitBin, repoPath string) (ownerRepo string, ok bool) {
	url, err := runGit(gitBin, repoPath, "remote", "get-url", "origin")
	if err != nil {
		return "", false
	}
	url = strings.TrimSuffix(url, ".git")
	for _, prefix := range []string{"git@github.com:", "ssh://git@github.com/", "https://github.com/"} {
		if rest, ok := strings.CutPrefix(url, prefix); ok && rest != "" {
			return rest, true
		}
	}
	return "", false
}

// reachableFromOrigin reports whether sha is, or is an ancestor of, the
// tip of any origin/* remote-tracking branch — i.e. whether it's actually
// present on the remote as of the last fetch (git has no way to ask the
// remote directly for an arbitrary commit; only ref tips are queryable).
func reachableFromOrigin(gitBin, repoPath, sha string) bool {
	out, err := runGit(gitBin, repoPath, "branch", "-r", "--contains", sha, "--list", "origin/*")
	return err == nil && out != ""
}

// githubCommitURL returns the GitHub web URL for sha in ownerRepo.
func githubCommitURL(ownerRepo, sha string) string {
	return "https://github.com/" + ownerRepo + "/commit/" + sha
}

// githubUsernameFromNoreplyEmail extracts the username from a GitHub
// "keep my email private" noreply address, recognizing both the current
// "<id>+<username>@users.noreply.github.com" form and the older, still
// valid "<username>@users.noreply.github.com" form. This is a GitHub
// account identity independent of any particular repo, so it doesn't
// depend on origin being GitHub or the commit being reachable from it —
// unlike the commit-URL hyperlink.
func githubUsernameFromNoreplyEmail(email string) (username string, ok bool) {
	local, ok := strings.CutSuffix(email, "@users.noreply.github.com")
	if !ok || local == "" {
		return "", false
	}
	if _, rest, found := strings.Cut(local, "+"); found {
		local = rest
	}
	if local == "" {
		return "", false
	}
	return local, true
}

// githubProfileURL returns the GitHub web URL for a user's profile.
func githubProfileURL(username string) string {
	return "https://github.com/" + username
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

// shortSHA truncates a full 40-hex commit SHA to a short, git-style
// display form.
func shortSHA(sha string) string {
	const n = 7
	if len(sha) < n {
		return sha
	}
	return sha[:n]
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
