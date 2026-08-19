# Benchmark results

Recorded 2026-08-20, Apple M4 (darwin/arm64), against `~/projects/critic`
(816 commits, 91MB `.git`, 486 tracked files as of that commit).

## Reproducing

```sh
go test -run=^$ -bench=. -benchmem -benchtime=1s ./benchmarks/...
```

Every benchmark is skipped (not failed) if `~/projects/critic` isn't
present on the machine running the tests, or if `git` isn't on `PATH`.

## Raw output

```
goos: darwin
goarch: arm64
pkg: gitfs/benchmarks
cpu: Apple M4
BenchmarkReadDirRoot/gogit-10                          6241     191213 ns/op    121618 B/op      883 allocs/op
BenchmarkReadDirRoot/exec-10                             144    8683046 ns/op     63809 B/op      148 allocs/op
BenchmarkReadDirDeep/gogit-10                            596    1763277 ns/op   1242316 B/op     8950 allocs/op
BenchmarkReadDirDeep/exec-10                              73   17454225 ns/op    230638 B/op      799 allocs/op
BenchmarkReadFileLarge/gogit-10                        12542      95645 ns/op    914131 B/op      142 allocs/op
BenchmarkReadFileLarge/exec-10                            67   16341752 ns/op   1067701 B/op      142 allocs/op
BenchmarkGlob/gogit-10                                   956    1120458 ns/op    662904 B/op     6520 allocs/op
BenchmarkGlob/exec-10                                      5  219413392 ns/op   1302232 B/op     2083 allocs/op
BenchmarkExtendedStatsDeepUnbounded/gogit-10              19   60095007 ns/op  54580753 B/op   493418 allocs/op
BenchmarkExtendedStatsDeepUnbounded/exec-10               54   19490379 ns/op     96222 B/op      119 allocs/op
BenchmarkExtendedStatsDeepUnbounded/gogit+fallback-10     46   24111038 ns/op  11388796 B/op   102109 allocs/op
BenchmarkExtendedStatsDeepBounded/gogit-10                690    1664095 ns/op   1732234 B/op    14431 allocs/op
BenchmarkExtendedStatsDeepBounded/exec-10                  36   32676108 ns/op    190858 B/op      235 allocs/op
BenchmarkExtendedStatsDeepBounded/gogit+fallback-10       690    1702644 ns/op   1732105 B/op    14431 allocs/op
BenchmarkExtendedStatsShallow/gogit-10                   1491     737049 ns/op    820880 B/op     7001 allocs/op
BenchmarkExtendedStatsShallow/exec-10                      70   16411121 ns/op     96144 B/op      119 allocs/op
BenchmarkExtendedStatsShallow/gogit+fallback-10          1510     715480 ns/op    820934 B/op     7001 allocs/op
PASS
ok      gitfs/benchmarks        26.667s
```

## Summary

| Benchmark | gogit | exec | gogit+fallback |
| --- | --- | --- | --- |
| ReadDir, root (27 entries) | 0.19ms | 8.7ms | — |
| ReadDir, deep (167 entries) | 1.8ms | 17.5ms | — |
| ReadFile, large (~188KiB) | 0.10ms | 16.3ms | — |
| Glob (`src/*/*.go`) | 1.1ms | 219ms | — |
| Blame, deep+unbounded (778 commits back) | 60.1ms | 19.5ms | 24.1ms |
| Blame, deep+bounded=20 (true answer 778 back) | 1.7ms | 32.7ms | 1.7ms |
| Blame, shallow+unbounded (13 commits back) | 0.74ms | 16.4ms | 0.72ms |

## Interpretation

- **Every normal filesystem op** (`ReadDir`, `ReadFile`, `Glob`) and every
  **shallow or explicitly-bounded blame lookup**: `gogit` wins by 1-2
  orders of magnitude. `exec` pays a near-fixed ~15-20ms subprocess-startup
  tax per call regardless of what's actually being asked, which dominates
  at this scale.
- **A genuinely deep, unbounded blame lookup flips that**: real `git log`'s
  C implementation walks hundreds of commits faster than go-git's
  pure-Go, per-commit object decoding (`gogit`: 60ms / ~493K allocations
  for one call, vs. `exec`'s single subprocess at 19.5ms).
- **`gogit+fallback`** (`gitfs.WithBlameFallback`) gets close to the best
  of both: every lookup first probes with the cheap pure-Go walk (capped
  at 150 ancestor commits — see `blameFallbackThreshold` in `gitfs.go`),
  and only pays for a git subprocess if that probe comes up empty.
  - Shallow-unbounded (0.72ms) matches plain `gogit` exactly — the probe
    finds the answer immediately, never touching the subprocess.
  - Deep-bounded=20 (1.7ms) is unaffected — the requested bound never
    exceeds the probe cap, so this is pure-Go start to finish.
  - Deep-unbounded (24.1ms) pays for a wasted 150-commit probe before
    escalating, so it's slower than an "always escalate" design would be
    (measured ~10.6ms in an earlier iteration), but still 2.4x faster than
    plain `gogit` and roughly on par with plain `exec`.

  This trade — a small, bounded probe cost on the rare deep case, in
  exchange for zero regression on the common shallow case — is the
  intended design; see `WithBlameFallback`'s doc comment in `gitfs.go`.
