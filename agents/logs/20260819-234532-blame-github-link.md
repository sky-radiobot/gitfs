# Task: ls -l column alignment + ls --blame GitHub commit hyperlinks

**Started:** 2026-08-19 23:30:00
**Ended:** 2026-08-19 23:45:32
**Strategy:** Feature (TDD)
**Status:** Completed
**Complexity:** Medium
**Used Models:** Sonnet 5
**Token usage (Estimated):** n/a

## Objective
Several incremental asks from the human, each building on the last:
1. `ls -l` output: no tabs, size right-aligned, columns tabulated to max
   width per column with one space of separation (matching plain `ls -l`).
2. Timestamps formatted like plain `ls -l` ("Aug 19 22:03", or
   "Oct 18  2024" — note the extra space — for anything older than ~6
   months or in the future).
3. Confirmed "reachable from origin/*" means the commit is, or is an
   ancestor of, one of origin's branch tips (checked locally, so only as
   fresh as the last fetch — git has no way to query a remote for an
   arbitrary commit directly).
4. When origin is GitHub and the blamed commit is reachable from it,
   render a `https://github.com/<owner>/<repo>/commit/<sha>` URL for that
   commit in `ls --blame`'s commit column.
5. Render that URL as a clickable OSC 8 terminal hyperlink (short SHA as
   visible text) rather than the full URL taking up column space.
6. Detect whether the terminal actually supports hyperlinks and skip
   rendering them otherwise (never break piped/non-TTY output, or
   terminals that plainly don't render OSC 8).

## Progress
- [x] `printTable` helper: rows collected per listing group (each
      directory argument, or a lone file target, aligns independently,
      same as the earlier tab-column work), size right-aligned, other
      columns left-aligned, name (last column) never padded
- [x] `lsDate`: matches coreutils `ls -l`'s dual format exactly (verified
      against a synthetic old/future timestamp, not just recent ones)
- [x] `githubRepo`/`reachableFromOrigin`/`githubCommitURL`: parse
      `origin`'s remote URL (SSH/HTTPS/ssh:// forms) for a GitHub
      owner/repo, and `git branch -r --contains <sha> --list 'origin/*'`
      for reachability
- [x] `tableCell{text, width}` + `hyperlink()`: OSC 8 escape sequences
      carry real bytes that must NOT count toward column alignment width,
      so cells now track a separate "visible width" from their printed
      text rather than measuring `len()` on the escaped string directly
- [x] `terminalSupportsHyperlinks()`: TTY check (`os.Stdout.Stat()` +
      `ModeCharDevice`) plus `TERM != "dumb"`; added
      `GITFS_FORCE_HYPERLINKS=1` override — both for real use (e.g.
      piping through `less -R`) and because the integration-test harness
      itself never has a real TTY, so the positive case was otherwise
      untestable end-to-end
- [x] 5 new integration tests (github URL forced-rendering,
      no-hyperlink-without-force, unreachable-falls-back, plus fixing 3
      existing tests' now-space-separated/multi-word-date expectations);
      `make vet test`: 78/78 integration tests, unit tests green
- [x] README updated; `make install` refreshed the binary

## Obstacles
- **Issue:** Existing integration test assertions relied on tab-delimited
  output (`cut -f4-`, `awk -F'\t'`) and single-token date strings
  (`2026-01-02`); switching to space-aligned columns with a 3-word date
  ("Aug 19 23:31") broke `cut -f`/naive whitespace-field-count parsing —
  the date column's own internal spaces make "count fields by whitespace"
  ambiguous (the same reason real `ls -l` output is notoriously hard to
  parse with awk).
  **Resolution:** Switched to `awk '{print $NF}'` (last field, robust
  regardless of how many space-separated tokens precede it, since names
  in the fixture never contain spaces) for extracting just the name, and
  to exact full-line string comparisons (computing expected padding by
  hand, since a lone-row group's columns aren't padded beyond their own
  natural width) elsewhere.
- **Issue:** First hyperlink implementation fed the full escaped string
  (`\x1b]8;;URL\x1b\\TEXT\x1b]8;;\x1b\\`) straight into `printTable`,
  which measured column width via plain `len()` — the invisible escape
  bytes inflated that row's measured width, forcing extra padding onto
  every other row in the same listing.
  **Resolution:** Changed `printTable`'s row type from `[]string` to
  `[]tableCell{text, width}`, so a cell's printed text and its
  alignment-width can differ; `hyperlink()` sets width to just the
  visible text's length. Verified by stripping the escape sequences from
  real output and confirming the remaining plain text lines up exactly
  the same as the non-hyperlink case.
- **Issue:** After adding the TTY/TERM=dumb gate, the just-written
  "renders GitHub URL" integration test started failing — the bash test
  harness captures command output via `$(...)`, which is inherently a
  pipe, never a real TTY, so the positive rendering path became
  permanently unreachable through that harness.
  **Resolution:** Added `GITFS_FORCE_HYPERLINKS=1` as an explicit
  override (also independently useful for real use, e.g. `less -R`),
  used only in the "forced" test; added a separate test confirming the
  *default* (unforced, piped) case never emits a URL or escape sequence
  even when origin/GitHub/reachability all say yes.

## Outcome
`gitfs ls -l` and `ls --blame` now render as properly space-aligned,
tab-free columns matching plain `ls -l`'s layout and date format;
`--blame`'s commit column becomes a clickable OSC 8 hyperlink to the
commit's GitHub page when origin is GitHub, the commit is confirmed
reachable from `origin/*`, and the terminal looks like it supports
hyperlinks (or `GITFS_FORCE_HYPERLINKS=1` is set) — falling back to a
plain short SHA otherwise. All committed, 78/78 integration tests +
unit tests green.

## Insights
- OSC 8 has no standardized tooltip/hover-text parameter — the only
  defined param is `id` (for grouping identical-link spans), and any
  hover text a terminal shows is just the URL itself, controlled by the
  terminal.
- When a feature's positive path is gated on an environment condition
  (TTY-ness) that a test harness structurally can never satisfy (piped
  command substitution), an explicit force-override isn't just a nice
  extra — it's often the *only* way to get end-to-end coverage of that
  path at all, not merely a convenience knob.
