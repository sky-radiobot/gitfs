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
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sky-radiobot/gitfs"
)

func fatal(cmd *cobra.Command, err error) {
	fmt.Fprintln(os.Stderr, err)
	fmt.Fprintf(os.Stderr, "run %q for more information\n", cmd.CommandPath()+" --help")
	os.Exit(1)
}

// cobra's own defaults (github.com/spf13/cobra@v1.10.2, command.go), copied
// verbatim so cat/ls can opt back into them explicitly: root below installs
// a customized template, which children otherwise inherit.
const (
	cobraDefaultUsageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
	cobraDefaultHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`
)

// rootUsageTemplate lists cat/ls under "Available Commands:" (via the
// "gitfs" command group below) ahead of cobra's built-in completion/help
// under "Other Commands:", and moves the GIT_BINARY note (root.Long) below
// the command list rather than above it, next to the flags it explains.
const rootUsageTemplate = `Usage:
  {{.CommandPath}} [command]{{$cmds := .Commands}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Other Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{with .Long}}

{{. | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableLocalFlags}}

Global Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}

Use "{{.CommandPath}} [command] --help" for more information about a command.
`

func main() {
	var sparse string

	root := &cobra.Command{
		Use:           "gitfs",
		Short:         "Read a git commit's tree without checking it out",
		Long:          "Set GIT_BINARY to shell out to a specific git binary instead of using\nthe built-in go-git backend.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&sparse, "sparse", "", "comma-separated repo-relative paths to restrict the filesystem to")
	root.AddGroup(&cobra.Group{ID: "gitfs", Title: "Available Commands:"})
	root.SetUsageTemplate(rootUsageTemplate)
	root.SetHelpTemplate("{{.UsageString}}\n")

	catCmd, lsCmd := catCommand(&sparse), lsCommand(&sparse)
	catCmd.GroupID, lsCmd.GroupID = "gitfs", "gitfs"
	for _, c := range []*cobra.Command{catCmd, lsCmd} {
		c.SetUsageTemplate(cobraDefaultUsageTemplate)
		c.SetHelpTemplate(cobraDefaultHelpTemplate)
	}
	root.AddCommand(catCmd, lsCmd)

	root.InitDefaultCompletionCmd()
	if completionCmd, _, err := root.Find([]string{"completion"}); err == nil {
		completionCmd.Long += "\nQuick start for bash, via the bash-completion package:\n\n" +
			"\tmkdir -p ~/.local/share/bash-completion/completions\n" +
			"\tgitfs completion bash > ~/.local/share/bash-completion/completions/gitfs\n"
	}

	executedCmd, err := root.ExecuteC()
	if err != nil {
		fatal(executedCmd, err)
	}
}

// refHelp explains REF and GIT_BINARY, shared by cat's and ls's Long help.
const refHelp = "REF is a full commit SHA or anything the git CLI can resolve to one\n" +
	"(branch, tag, short SHA, HEAD, ...). The repository is discovered\n" +
	"from the current directory, the same way git itself does.\n\n" +
	"Set GIT_BINARY to shell out to a specific git binary instead of using\n" +
	"the built-in go-git backend."

func catCommand(sparse *string) *cobra.Command {
	return &cobra.Command{
		Use:               "cat REF PATH [PATH...]",
		Short:             "print the content of one or more files at REF",
		Long:              "print the content of one or more files at REF\n\n" + refHelp,
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: completeRef,
		RunE: func(cmd *cobra.Command, args []string) error {
			tg, err := openFS(args[0], *sparse)
			if err != nil {
				return err
			}
			paths, err := expandGlobs(tg.fsys, tg.prefix, args[1:])
			if err != nil {
				return err
			}
			for _, p := range paths {
				data, err := tg.fsys.ReadFile(p)
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

func lsCommand(sparse *string) *cobra.Command {
	var long bool
	var blameLimitFlag string
	cmd := &cobra.Command{
		Use:   "ls REF [PATH...]",
		Short: "list directory entries at REF",
		Long: "list directory entries at REF\n\n" + refHelp + "\n\n" +
			"--blame adds, for each file, the last commit (at or before REF) that\n" +
			"touched it: its author's email, date, and short SHA. Without a LIMIT,\n" +
			"that search walks REF's full ancestry, which can be slow on a large\n" +
			"history; --blame=LIMIT bounds it to LIMIT ancestor commits, falling\n" +
			"back to REF's own commit if no match turns up within that window --\n" +
			"never a commit newer than REF, but possibly an imprecise one.",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: completeRef,
		RunE: func(cmd *cobra.Command, args []string) error {
			blame := cmd.Flags().Changed("blame")
			if blame {
				long = true
			}
			blameLimit := unboundedBlame
			if blame && blameLimitFlag != "unbounded" {
				var err error
				blameLimit, err = strconv.Atoi(blameLimitFlag)
				if err != nil || blameLimit < 0 {
					return fmt.Errorf("invalid --blame limit %q: must be a non-negative integer", blameLimitFlag)
				}
			}

			var extraOpts []gitfs.Option
			if blame {
				extraOpts = append(extraOpts, gitfs.WithExtendedStats(blameLimit))
			}
			tg, err := openFS(args[0], *sparse, extraOpts...)
			if err != nil {
				return err
			}

			paths := args[1:]
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
	cmd.Flags().BoolVarP(&long, "long", "l", false, "long format: mode, size, date, name (mode, author email, size, date, commit, name with --blame)")
	cmd.Flags().StringVar(&blameLimitFlag, "blame", "", "show the last commit that touched each file; optional LIMIT bounds the ancestor-commit search depth (unbounded if given with no value)")
	cmd.Flags().Lookup("blame").NoOptDefVal = "unbounded"
	return cmd
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

// completeRef suggests REF completions (local branches, tags,
// remote-tracking refs such as origin/main or the origin/HEAD symref
// shortened to just "origin", and HEAD) for the first positional argument
// of cat/ls; later arguments (PATH...) fall back to the shell's default
// file completion, since they're resolved against the current directory
// just like plain cat/ls.
func completeRef(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 0 {
		return nil, cobra.ShellCompDirectiveDefault
	}

	gitBin := os.Getenv("GIT_BINARY")
	if gitBin == "" {
		gitBin = "git"
	}
	repoPath, _, err := discoverRepo(gitBin)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	out, err := runGit(gitBin, repoPath, "for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/tags", "refs/remotes")
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	refs := []string{"HEAD"}
	if out != "" {
		refs = append(refs, strings.Split(out, "\n")...)
	}
	var matches []string
	for _, r := range refs {
		if strings.HasPrefix(r, toComplete) {
			matches = append(matches, r)
		}
	}
	matches = append(matches, completeSHAPrefix(gitBin, repoPath, toComplete)...)
	return matches, cobra.ShellCompDirectiveNoFileComp
}

// completeSHAPrefix suggests full commit SHAs matching a hex prefix of at
// least 4 digits, using git's own indexed object lookup
// (rev-parse --disambiguate) rather than a linear history scan: loose
// objects are bucketed into 256 directories by their first byte, and
// packed objects are found via a sorted, binary-searchable index, so this
// stays fast (single-digit milliseconds, measured) regardless of repo
// history size. 4 is a hard floor, not just a tuning choice: git's own
// --disambiguate silently returns nothing below 4 hex digits (verified),
// so there is no efficient path below that.
func completeSHAPrefix(gitBin, repoPath, prefix string) []string {
	if len(prefix) < 4 || !looksLikeHexPrefix(prefix) {
		return nil
	}
	out, err := runGit(gitBin, repoPath, "rev-parse", "--disambiguate="+prefix)
	if err != nil || out == "" {
		return nil
	}
	candidates := strings.Split(out, "\n")

	// --disambiguate matches any object type; keep only commits, checked
	// in one batch rather than one process per candidate.
	cmd := exec.Command(gitBin, "-C", repoPath, "cat-file", "--batch-check=%(objectname) %(objecttype)")
	cmd.Stdin = strings.NewReader(strings.Join(candidates, "\n") + "\n")
	checkOut, err := cmd.Output()
	if err != nil {
		return nil
	}

	var shas []string
	for _, line := range strings.Split(strings.TrimSpace(string(checkOut)), "\n") {
		if sha, kind, ok := strings.Cut(line, " "); ok && kind == "commit" {
			shas = append(shas, sha)
		}
	}
	return shas
}

// looksLikeHexPrefix reports whether s is a non-empty hex string.
func looksLikeHexPrefix(s string) bool {
	if s == "" {
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

// target bundles what cat/ls need once ref has been resolved: the pinned
// GitFS, the cwd's repo-root-relative prefix, and (for ls --blame's GitHub
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
