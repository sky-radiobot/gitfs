# Task: WithBlameFallback (probe-then-escalate), benchmarks, GitHub author links

**Started:** 2026-08-19 23:50:00
**Ended:** 2026-08-20 00:12:02
**Strategy:** Feature (TDD) + Performance
**Status:** Completed
**Complexity:** Complex
**Used Models:** Sonnet 5
**Token usage (Estimated):** n/a

## Objective
Several related pieces:
1. Add a real benchmark suite (`gitfs_bench_test.go`) comparing gogit vs.
   exec backends against a real, sizeable repo (`~/projects/critic`: 816
   commits, 91MB `.git`).
2. Add `gitfs.WithBlameFallback(path)`: let the go-git backend's
   `WithExtendedStats` lookups shell out to a git binary for deep
   searches, since the benchmark showed a real crossover (pure-Go loses
   badly past a few hundred commits of first-parent history walking).
3. Iterate the design based on benchmark feedback: naive "escalate once
   maxCommits > 150 or unbounded" regressed the common shallow-but-
   unbounded case (0.7ms -> 8.7ms); human proposed probe-then-escalate
   instead (try pure-Go bounded to 150 first; only shell out if that
   comes up empty) — implemented and re-benchmarked, confirming the fix.
4. `cmd/gitfs ls --blame`: render `<id>+<username>@users.noreply.github.com`
   (and the legacy `<username>@...` form) author emails as clickable
   hyperlinks to the GitHub profile, username-only as visible text —
   independent of origin/reachability, since it's a GitHub account
   identity rather than a repo one.
5. Answered (without code changes): whether ls -l --blame runs one git
   process per file — confirmed yes for both backends today, flagged as
   a real inefficiency worth a future batch-lookup API, not fixed in this
   session.

## Progress
- [x] `gitfs_bench_test.go`: ReadDir/ReadFile/Glob + three ExtendedStats
      benchmarks (deep-unbounded via `plans/refactor.md`, 778 commits
      back; deep-bounded=20; shallow via `go.mod`, 13 back), all
      `b.Skip`-gated on `~/projects/critic` being present
- [x] First benchmark run (before WithBlameFallback existed) established
      the baseline crossover: gogit wins every normal op and shallow/
      bounded blame by 1-2 orders of magnitude (subprocess-startup tax),
      but loses the deep-unbounded blame to plain exec (59.6ms vs 18.8ms)
- [x] `gitfs.WithBlameFallback(path)` + `blameFallbackThreshold = 150`:
      first version escalated immediately whenever maxCommits was
      negative or > 150
- [x] Deduped `logOneCommit`/`mustLogOneCommit` (parse `git log
      --max-count=1` output) into `backend.go`, shared by
      execBackend.lastCommit and gogitBackend's new git-fallback path —
      only the invocation style (`--git-dir=X` vs `-C repoPath`) differs
      between backends now, not the "run git log, parse 4 tab fields"
      logic
- [x] Re-benchmarked with fallback: confirmed the shallow-unbounded
      regression (0.72ms -> 8.7ms), which the human caught from the
      numbers and proposed the probe-then-escalate redesign for
- [x] Redesigned: `gogitBackend.lastCommit` always probes with the pure-Go
      `walkFirstParent` capped at min(150, requested bound) first;
      escalates to `lastCommitViaGit` (honoring the *original* requested
      bound, not just another 150-sized probe) only if the probe finds
      nothing and a fallback binary is configured
      - split old `lastCommit` into `walkFirstParent` (returns
        `found bool` instead of silently defaulting to the pinned
        commit — needed so the caller can distinguish "found the pinned
        commit as the real answer" from "gave up")
- [x] `TestWithBlameFallbackEscalates`: real end-to-end test building a
      160-commit fixture (`buildDeepHistoryFixture`) so escalation is
      genuinely exercised, not just wired — confirms both a valid and a
      bogus fallback binary behave correctly when escalation is actually
      required
      - fixed `TestWithBlameFallback` (the original, 2-commit fixture):
        its "bogus binary surfaces as an error" assertion no longer holds
        under the new design, since a 2-commit history always resolves
        within the pure-Go probe and never touches the (bogus) binary —
        that's correct new behavior, not a bug; updated the test's intent
        accordingly
- [x] Re-benchmarked again: shallow-unbounded back to 0.72ms (matching
      plain gogit, fixed); deep-unbounded now 24.5ms (up from 10.6ms in
      the "always escalate" version, due to the wasted 150-commit probe
      before escalating) but still 2.4x faster than plain gogit;
      bounded<=150 unaffected throughout
- [x] `githubUsernameFromNoreplyEmail`/`githubProfileURL` in
      `cmd/gitfs/main.go`; wired into `ls --blame`'s author-email column,
      gated on the same `terminalSupportsHyperlinks()` check as the
      commit-URL hyperlink (hoisted once per `ls` invocation, not
      per-row)
- [x] Manually verified both noreply email forms render correctly
      (visible username only, full email hidden behind the OSC 8 escape)
      and non-GitHub emails stay plain text; confirmed column alignment
      holds with escape sequences stripped
- [x] 4 new integration tests (author-link forced-rendering, no-link-
      without-force, escalation-related unit tests above); `make vet
      test`: 82/82 integration tests, unit tests green
- [x] README updated (WithBlameFallback, author-link rendering);
      `make install` refreshed the binary

## Obstacles
- **Issue:** First `WithBlameFallback` design (immediate escalate once
  `maxCommits` negative/>150) benchmarked well for the deep case but
  regressed the common shallow-but-unbounded case by >10x — caught only
  because the human specifically asked to rerun the benchmark after the
  change, not something a plain "does it work" check would have
  surfaced.
  **Resolution:** Human proposed probe-then-escalate; re-implementing and
  re-benchmarking confirmed it as a strict improvement (shallow case
  fully recovered, deep case still 2.4x faster than pure gogit, just not
  as fast as the "always escalate" version's 10.6ms). This is the kind
  of trade-off that's invisible without the actual before/after numbers —
  reinforces why the human wanted the benchmark infrastructure built
  first.
- **Issue:** `walkFirstParent`'s old (pre-split) form couldn't
  distinguish "found the pinned commit as the genuinely correct answer"
  from "exhausted the search bound without finding anything, so
  defaulting to the pinned commit" — both returned the identical
  `commitInfo` value (`commitFromObject(b.commit)`). The probe-then-
  escalate design needs exactly this distinction (only escalate on
  genuine not-found, not on "found, and it happens to be the pinned
  commit itself").
  **Resolution:** Split into `walkFirstParent(p, maxCommits) (commitInfo,
  found bool, error)`, with the top-level `lastCommit` now owning the
  final "give up, report pinned commit" fallback instead of it being
  buried inside the walk loop.
- **Issue:** The original `TestWithBlameFallback` unit test's "bogus
  binary must error" assertion started failing after the redesign — not
  a regression, but a consequence of the fixture (2 total commits) being
  far too shallow to ever force escalation, so the bogus binary was
  never actually invoked (silently correct, but the test's own premise —
  "this proves the fallback path executes" — became false).
  **Resolution:** Rather than weakening the assertion, built a proper
  160-commit fixture (`buildDeepHistoryFixture`) in a new test
  specifically for exercising escalation, and repurposed the original
  test to document why its own bogus-binary case no longer applies
  (correctness-when-unneeded, not escalation itself).

## Outcome
`gitfs.WithBlameFallback(path)` lets the go-git backend hand off only
genuinely deep `WithExtendedStats` searches to a git subprocess, via a
cheap pure-Go probe-then-escalate strategy that adds no regression to the
common shallow case. `cmd/gitfs ls --blame` renders both GitHub commit
URLs (when reachable from `origin`) and GitHub noreply-email author
profiles as clickable OSC 8 hyperlinks, gated on terminal support (or
`GITFS_FORCE_HYPERLINKS=1`). `gitfs_bench_test.go` (skip-gated on
`~/projects/critic`) documents the actual measured trade-offs. All
committed, 82/82 integration tests + unit tests green.

## Insights
- A benchmark that only measures the "does the new thing help in its
  target case" scenario is not enough — re-running the *same* benchmark
  suite after a design change caught a real regression in an unrelated
  case (shallow-but-unbounded) that a narrower "does escalation work"
  check would have missed entirely. Keep the full benchmark suite, not
  just the case being optimized.
- When a fallback/escalation path's correctness can't be distinguished
  from its absence using only the return value (here: "found the pinned
  commit as the real answer" vs. "gave up and defaulted to it" produce
  identical output), any caller logic that needs to *react* to that
  distinction (e.g. "should I escalate?") needs an explicit signal
  (`found bool`), not just the value itself.
- `ls -l --blame` currently issues one git lookup per listed file, for
  both backends — flagged to the human as worth a batch-lookup API in a
  future session (e.g. a single `git log --name-status` walk resolving
  all requested paths' last-touch commits together), not addressed here.
