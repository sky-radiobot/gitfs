#!/usr/bin/env bash
# Integration tests for the gitfs CLI, following the testsh conventions from
# github.com/radiospiel/critic. Every assertion runs against both backends —
# the default go-git one and the shell-out one (-git-binary) — and checks
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
  BARE=$(mktemp -d)
  git clone --quiet --bare "$REPO" "$BARE/repo.git"
}

teardown() {
  [[ -n "$REPO" ]] && rm -rf "$REPO" && REPO=""
  [[ -n "$BARE" ]] && rm -rf "$BARE" && BARE=""
}

# both asserts that both backends print the expected stdout for the given
# CLI arguments.
both() {
  local expected="$1"
  shift
  assert_eq "$expected" "$("$GITFS_BIN" "$@")"
  assert_eq "$expected" "$("$GITFS_BIN" -git-binary "$GIT_BIN" "$@")"
}

# both_fail asserts that both backends reject the given CLI arguments.
both_fail() {
  if "$GITFS_BIN" "$@" >/dev/null 2>&1; then
    fail "expected failure (gogit backend): gitfs $*"
  else
    pass
  fi
  if "$GITFS_BIN" -git-binary "$GIT_BIN" "$@" >/dev/null 2>&1; then
    fail "expected failure (exec backend): gitfs $*"
  else
    pass
  fi
}

# -- tests --------------------------------------------------------------------

test_cat_matches_git() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" "$REPO" "$SHA" cat README.md

  expected=$(git -C "$REPO" show "$SHA:src/app/main.go")
  both "$expected" "$REPO" "$SHA" cat src/app/main.go
}

test_cat_symlink_returns_target() {
  both "README.md" "$REPO" "$SHA" cat link
}

test_ls_root_matches_git() {
  local expected
  expected=$(git -C "$REPO" ls-tree --name-only "$SHA" | LC_ALL=C sort)
  assert_eq "$expected" "$("$GITFS_BIN" "$REPO" "$SHA" ls | cut -f3-)"
  assert_eq "$expected" "$("$GITFS_BIN" -git-binary "$GIT_BIN" "$REPO" "$SHA" ls | cut -f3-)"
}

test_ls_subdirectory() {
  both "drwxr-xr-x${TAB}0${TAB}app" "$REPO" "$SHA" ls src
}

test_stat_modes_and_sizes() {
  local size
  size=$(git -C "$REPO" cat-file -s "$SHA:run.sh")
  both "-rwxr-xr-x${TAB}${size}${TAB}run.sh" "$REPO" "$SHA" stat run.sh

  size=$(git -C "$REPO" cat-file -s "$SHA:README.md")
  both "-rw-r--r--${TAB}${size}${TAB}README.md" "$REPO" "$SHA" stat README.md

  both "Lrwxrwxrwx${TAB}9${TAB}link" "$REPO" "$SHA" stat link
  both "drwxr-xr-x${TAB}0${TAB}src" "$REPO" "$SHA" stat src
}

test_glob() {
  both "src/app" "$REPO" "$SHA" glob "src/*"
  both "src/app/main.go" "$REPO" "$SHA" glob "src/*/*.go"
  both "" "$REPO" "$SHA" glob "nomatch/*"
}

test_bare_repo() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" "$BARE/repo.git" "$SHA" cat README.md
  both "drwxr-xr-x${TAB}0${TAB}app" "$BARE/repo.git" "$SHA" ls src
}

test_sparse() {
  # Root listing is limited to the ancestors of the sparse paths.
  both "drwxr-xr-x${TAB}0${TAB}src" -sparse src/app "$REPO" "$SHA" ls
  both "drwxr-xr-x${TAB}0${TAB}app" -sparse src/app "$REPO" "$SHA" ls src

  # Inside the sparse set: readable.
  both "package main" -sparse src/app "$REPO" "$SHA" cat src/app/main.go
  both "src/app/main.go" -sparse src/app "$REPO" "$SHA" glob "src/*/*.go"

  # Outside the sparse set: indistinguishable from absent.
  both_fail -sparse src/app "$REPO" "$SHA" cat README.md
  both_fail -sparse src/app "$REPO" "$SHA" stat README.md
  both_fail -sparse src/app "$REPO" "$SHA" ls docs
}

test_rejects_non_commit_sha() {
  both_fail "$REPO" main cat README.md          # branch name
  both_fail "$REPO" "${SHA:0:10}" cat README.md # short SHA
  both_fail "$REPO" 0000000000000000000000000000000000000000 cat README.md

  local err
  err=$("$GITFS_BIN" "$REPO" main cat README.md 2>&1 || true)
  assert_contains "$err" "not a full commit SHA"
}

test_rejects_non_repo() {
  local outside
  outside=$(mktemp -d)
  both_fail "$outside" "$SHA" cat README.md
  rm -rf "$outside"
}

test_missing_git_binary_fails() {
  if "$GITFS_BIN" -git-binary /nonexistent/git "$REPO" "$SHA" cat README.md >/dev/null 2>&1; then
    fail "expected failure with a missing git binary"
  else
    pass
  fi
}

test_missing_path_fails() {
  both_fail "$REPO" "$SHA" cat nope.txt
  both_fail "$REPO" "$SHA" stat nope.txt
  both_fail "$REPO" "$SHA" ls nope
}

# -- run ----------------------------------------------------------------------

run_tests
