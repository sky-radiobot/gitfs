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

	"github.com/spf13/cobra"

	"gitfs"
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
			printEntry := func(displayName string, info fs.FileInfo) error {
				switch {
				case !long:
					fmt.Println(displayName)
				case !blame:
					fmt.Printf("%s\t%d\t%s\t%s\n", info.Mode(), info.Size(), info.ModTime().Format("2006-01-02"), displayName)
				default:
					es, _ := info.Sys().(*gitfs.ExtendedStat)
					if es == nil {
						return fmt.Errorf("gitfs: no extended stats for %s", displayName)
					}
					if es.Err != nil {
						return es.Err
					}
					fmt.Printf("%s\t%s\t%d\t%s\t%s\t%s\n", info.Mode(), es.AuthorEmail, info.Size(), es.Date.Format("2006-01-02"), shortSHA(es.Commit), displayName)
				}
				return nil
			}

			for _, t := range targets {
				info, err := tg.fsys.Stat(t)
				if err != nil {
					return err
				}
				if !info.IsDir() {
					if err := printEntry(displayPath(tg.prefix, t), info); err != nil {
						return err
					}
					printedAny = true
					continue
				}
				entries, err := tg.fsys.ReadDir(t)
				if err != nil {
					return err
				}
				printHeader(displayPath(tg.prefix, t))
				for _, e := range entries {
					entryInfo, err := e.Info()
					if err != nil {
						return err
					}
					if err := printEntry(e.Name(), entryInfo); err != nil {
						return err
					}
				}
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
// GitFS and the cwd's repo-root-relative prefix.
type target struct {
	fsys   *gitfs.GitFS
	prefix string
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
	return &target{fsys: fsys, prefix: prefix}, nil
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
