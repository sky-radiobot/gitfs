#!/usr/bin/env bash
# Integration tests for the gitfs CLI, following the testsh conventions from
# github.com/radiospiel/critic. Every assertion runs against both backends —
# the default go-git one and the shell-out one (GIT_BINARY) — and checks
# output against ground truth from the real git CLI.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/testsh.inc"

GITFS_BIN="${GITFS_BIN:?GITFS_BIN must point at the compiled gitfs binary}"
GIT_BIN="$(command -v git)"

TAB=$'\t'
REPO=""
BARE=""
SHA=""
ORIG_PWD="$PWD"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME="Test User" GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME="Test User" GIT_COMMITTER_EMAIL=test@example.com

setup() {
  REPO=$(mktemp -d)
  REPO=$(cd "$REPO" && pwd -P)
  git -C "$REPO" init --quiet --initial-branch=main
  mkdir -p "$REPO/src/app" "$REPO/docs"
  echo "hello world" >"$REPO/README.md"
  printf 'package main\n' >"$REPO/src/app/main.go"
  printf '# docs\n' >"$REPO/docs/index.md"
  printf '#!/bin/sh\necho hi\n' >"$REPO/run.sh"
  chmod +x "$REPO/run.sh"
  ln -s README.md "$REPO/link"
  git -C "$REPO" add -A
  git -C "$REPO" commit --quiet -m initial
  SHA=$(git -C "$REPO" rev-parse HEAD)
  git -C "$REPO" tag v1
  BARE=$(mktemp -d)
  git clone --quiet --bare "$REPO" "$BARE/repo.git"
  cd "$REPO"
}

teardown() {
  cd "$ORIG_PWD"
  [[ -n "$REPO" ]] && rm -rf "$REPO" && REPO=""
  [[ -n "$BARE" ]] && rm -rf "$BARE" && BARE=""
}

# both asserts that both backends print the expected stdout for the given
# CLI arguments (op REF [ARGS]), run from the current directory.
both() {
  local expected="$1"
  shift
  assert_eq "$expected" "$("$GITFS_BIN" "$@")"
  assert_eq "$expected" "$(GIT_BINARY="$GIT_BIN" "$GITFS_BIN" "$@")"
}

# both_fail asserts that both backends reject the given CLI arguments.
both_fail() {
  if "$GITFS_BIN" "$@" >/dev/null 2>&1; then
    fail "expected failure (gogit backend): gitfs $*"
  else
    pass
  fi
  if GIT_BINARY="$GIT_BIN" "$GITFS_BIN" "$@" >/dev/null 2>&1; then
    fail "expected failure (exec backend): gitfs $*"
  else
    pass
  fi
}

# -- tests --------------------------------------------------------------------

test_cat_matches_git() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" cat "$SHA" README.md

  expected=$(git -C "$REPO" show "$SHA:src/app/main.go")
  both "$expected" cat "$SHA" src/app/main.go
}

test_cat_multiple_paths_concatenates() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md"; git -C "$REPO" show "$SHA:docs/index.md")
  both "$expected" cat "$SHA" README.md docs/index.md
}

test_cat_symlink_returns_target() {
  both "README.md" cat "$SHA" link
}

test_ls_root_default_is_plain_names() {
  local expected
  expected=$(git -C "$REPO" ls-tree --name-only "$SHA" | LC_ALL=C sort)
  both "$expected" ls "$SHA"
}

test_ls_long_matches_git() {
  local expected
  expected=$(git -C "$REPO" ls-tree --name-only "$SHA" | LC_ALL=C sort)
  assert_eq "$expected" "$("$GITFS_BIN" ls "$SHA" -l | cut -f3-)"
  assert_eq "$expected" "$(GIT_BINARY="$GIT_BIN" "$GITFS_BIN" ls "$SHA" -l | cut -f3-)"
}

test_ls_subdirectory() {
  both "app" ls "$SHA" src
  both "drwxr-xr-x${TAB}0${TAB}app" ls "$SHA" -l src
}

test_ls_multiple_paths_prints_headers() {
  both "$(printf 'src:\napp\n\ndocs:\nindex.md')" ls "$SHA" src docs
}

test_bare_repo_discovered_from_cwd() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  (
    cd "$BARE/repo.git"
    assert_eq "$expected" "$("$GITFS_BIN" cat "$SHA" README.md)"
    assert_eq "$expected" "$(GIT_BINARY="$GIT_BIN" "$GITFS_BIN" cat "$SHA" README.md)"
    assert_eq "app" "$("$GITFS_BIN" ls "$SHA" src)"
  )
}

test_repo_discovered_from_subdirectory() {
  (
    cd "$REPO/src/app"
    assert_eq "package main" "$("$GITFS_BIN" cat "$SHA" main.go)"
  )
}

test_sparse() {
  # Root listing is limited to the ancestors of the sparse paths.
  both "src" ls --sparse src/app "$SHA"
  both "app" ls --sparse src/app "$SHA" src

  # Inside the sparse set: readable.
  both "package main" cat --sparse src/app "$SHA" src/app/main.go

  # Outside the sparse set: indistinguishable from absent.
  both_fail cat --sparse src/app "$SHA" README.md
  both_fail ls --sparse src/app "$SHA" docs
}

test_resolves_branch_name() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" cat main README.md
}

test_resolves_tag() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" cat v1 README.md
}

test_resolves_head() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" cat HEAD README.md
}

test_resolves_short_sha() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" cat "${SHA:0:10}" README.md
}

test_rejects_unresolvable_ref() {
  both_fail cat no-such-ref README.md

  local err
  err=$("$GITFS_BIN" cat no-such-ref README.md 2>&1 || true)
  assert_contains "$err" "cannot resolve"
}

test_rejects_unresolvable_full_sha() {
  both_fail cat 0000000000000000000000000000000000000000 README.md
}

test_rejects_non_repo() {
  local outside
  outside=$(mktemp -d)
  (
    cd "$outside"
    if "$GITFS_BIN" cat "$SHA" README.md >/dev/null 2>&1; then
      fail "expected failure outside a git repository"
    else
      pass
    fi
  )
  rm -rf "$outside"
}

test_missing_git_binary_fails() {
  if GIT_BINARY=/nonexistent/git "$GITFS_BIN" cat "$SHA" README.md >/dev/null 2>&1; then
    fail "expected failure with a missing git binary"
  else
    pass
  fi
}

test_missing_path_fails() {
  both_fail cat "$SHA" nope.txt
  both_fail ls "$SHA" nope
}

# has_line asserts that needle appears as an exact line in haystack (as
# opposed to a mere substring match, which __complete's ":<directive>"
# footer could otherwise confuse).
has_line() {
  local haystack="$1" needle="$2"
  [[ $'\n'"$haystack"$'\n' == *$'\n'"$needle"$'\n'* ]]
}

test_completes_ref() {
  local out
  out=$("$GITFS_BIN" __complete cat "" 2>/dev/null)
  for ref in HEAD main v1; do
    if has_line "$out" "$ref"; then
      pass
    else
      fail "expected '$ref' in ref completions: $out"
    fi
  done
}

test_ref_completion_respects_prefix() {
  local out
  out=$("$GITFS_BIN" __complete cat "v" 2>/dev/null)
  if has_line "$out" v1; then
    pass
  else
    fail "expected v1 in ref completions for prefix 'v': $out"
  fi
  if has_line "$out" main; then
    fail "expected 'main' filtered out by prefix 'v': $out"
  else
    pass
  fi
}

test_path_arg_falls_back_to_default_completion() {
  local out
  out=$("$GITFS_BIN" __complete cat "$SHA" "" 2>/dev/null)
  if has_line "$out" ":0"; then
    pass
  else
    fail "expected ':0' (ShellCompDirectiveDefault) for PATH-arg completion: $out"
  fi
}

# -- run ----------------------------------------------------------------------

run_tests
