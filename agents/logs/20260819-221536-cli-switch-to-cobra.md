# Task: Switch cmd/gitfs from stdlib flag to cobra

**Started:** 2026-08-19 22:15:36
**Ended:** 2026-08-19 22:15:36
**Strategy:** Refactoring
**Status:** Completed
**Complexity:** Medium
**Used Models:** Sonnet 5
**Token usage (Estimated):** n/a

## Objective
Replace stdlib `flag` in `cmd/gitfs` with `github.com/spf13/cobra` (the
library used across the human's other Go projects: critic, jobcenter,
lair), without changing the CLI's observable shape
(`gitfs [--git-binary PATH] [--sparse p1,p2] REF cat|ls [ARGS]`) beyond the
expected switch from single-dash (`-git-binary`) to double-dash
(`--git-binary`) long flags that cobra/pflag conventions require.

## Progress
- [x] Confirmed cobra v1.10.2 is the human's standard choice (checked
      go.mod across ~/projects/{critic,critic-2,jobcenter,lair})
- [x] Clarified arg-order handling: REF still precedes the op, extracted
      manually ahead of cobra rather than making REF a cobra positional
      arg on the root command
- [x] Rewrote `cmd/gitfs/main.go`
- [x] Updated `tests/integration/test_gitfs.sh` flag syntax
      (`-git-binary`/`-sparse` -> `--git-binary`/`--sparse`)
- [x] `go mod tidy`; README and CLAUDE.md updated
- [x] `make install` to refresh the installed binary
- [x] `make vet test` green (46/46 integration tests)

## Obstacles
- **Issue:** First attempt wrapped the whole CLI in one outer
  `cobra.Command` (global flags + `RunE` extracting REF from `args[0]`,
  then building a nested `sub` cobra.Command for `cat`/`ls`). This failed
  with `unknown shorthand flag: 'l' in -l` on `gitfs SHA ls -l` — the
  OUTER root command eagerly parses the *entire* argv against its own
  registered flags (only `--git-binary`/`--sparse`) before `RunE` ever
  runs, so it choked on `-l`, which was meant for the inner `ls`
  subcommand.
  **Resolution:** Per human's suggestion, dropped the outer cobra.Command
  entirely. Used a plain `pflag.FlagSet` with `SetInterspersed(false)` to
  parse only `--git-binary`/`--sparse` and stop at the first positional
  arg (REF). Everything after REF (the op and its own args/flags) is
  handed untouched to a `cobra.Command` with `cat`/`ls` as subcommands,
  which does its own normal flag parsing (e.g. `-l`/`--long` on `ls`).
  This also gives free `--help` text per subcommand.

## Outcome
`cmd/gitfs` now uses cobra for op dispatch (`cat`/`ls`, each with
per-command `--help`), while a small `pflag.FlagSet` (not a full
cobra.Command) handles the pre-REF global flags — since REF sits between
the global flags and the op, it doesn't fit cobra's normal
`cmd <subcmd> <args>` positional model. CLI behavior is unchanged except
`-git-binary`/`-sparse` are now `--git-binary`/`--sparse` (pflag/cobra
long-flag convention); `-l` for `ls` is unchanged (still a valid pflag
shorthand). All 46 integration tests updated and passing; unit tests and
`go vet` unaffected.

## Insights
When mixing a hand-rolled positional argument (REF) between global flags
and a cobra-dispatched subcommand tree, don't wrap the whole thing in one
outer cobra.Command — it parses the full argv against its own flag set
first and will reject flags meant for the inner subcommands. Split it:
pflag.FlagSet (interspersion off) for the part before the manual
positional arg, then a separate cobra.Command.Execute() for the remainder.
