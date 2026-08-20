#!/usr/bin/env bash
# Integration tests for cmd/git-ls, the `git ls` subcommand: the gitfs ls
# subcommand pinned to the current checkout (HEAD) — plain `git ls` lists
# names, `-l`/`--long` shows the blame listing with search depth from
# `git config gitls.blameLimit` (default 1000). Output is cross-checked
# against the gitfs CLI itself as ground truth, on both backends.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/testsh.inc"

GITFS_BIN="${GITFS_BIN:?GITFS_BIN must point at the compiled gitfs binary}"
GIT_LS="${GIT_LS_BIN:?GIT_LS_BIN must point at the compiled git-ls binary}"
GIT_BIN="$(command -v git)"

REPO=""
ORIG_PWD="$PWD"

export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null
export GIT_AUTHOR_NAME="Test User" GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME="Test User" GIT_COMMITTER_EMAIL=test@example.com

# The fixture has two commits with different authors: the initial commit
# (first@example.com) creates all files, the second (test@example.com)
# touches only README.md. That makes the blame search depth observable:
# with limit 0 every file falls back to HEAD's own author, while the
# default 1000 attributes src/app/main.go to the first commit.
setup() {
  REPO=$(mktemp -d)
  REPO=$(cd "$REPO" && pwd -P)
  git -C "$REPO" init --quiet --initial-branch=main
  mkdir -p "$REPO/src/app" "$REPO/docs"
  echo "hello world" >"$REPO/README.md"
  printf 'package main\n' >"$REPO/src/app/main.go"
  printf '# docs\n' >"$REPO/docs/index.md"
  git -C "$REPO" add -A
  GIT_AUTHOR_NAME="First Author" GIT_AUTHOR_EMAIL=first@example.com \
  GIT_COMMITTER_NAME="First Author" GIT_COMMITTER_EMAIL=first@example.com \
    git -C "$REPO" commit --quiet -m initial
  echo "hello again" >"$REPO/README.md"
  git -C "$REPO" add -A
  git -C "$REPO" commit --quiet -m "update readme"
  cd "$REPO"
}

teardown() {
  cd "$ORIG_PWD"
  [[ -n "$REPO" ]] && rm -rf "$REPO" && REPO=""
}

# both asserts that both backends (default go-git, and shell-out via
# GIT_BINARY) print the expected stdout for the given command.
both() {
  local expected="$1"
  shift
  assert_eq "$expected" "$("$@")"
  assert_eq "$expected" "$(GIT_BINARY="$GIT_BIN" "$@")"
}

test_plain_lists_names_like_gitfs_ls_head() {
  both "$("$GITFS_BIN" ls HEAD)" "$GIT_LS"
}

test_long_matches_gitfs_blame_1000() {
  local expected
  expected="$("$GITFS_BIN" ls HEAD --blame=1000)"
  both "$expected" "$GIT_LS" -l
  both "$expected" "$GIT_LS" --long
}

test_paths_are_passed_through() {
  both "$("$GITFS_BIN" ls HEAD src)" "$GIT_LS" src
  both "$("$GITFS_BIN" ls HEAD --blame=1000 src)" "$GIT_LS" -l src
}

test_default_limit_finds_first_commit_author() {
  assert_contains "$("$GIT_LS" -l src)" "first@example.com"
}

test_config_blame_limit_overrides_default() {
  git config gitls.blameLimit 0
  both "$("$GITFS_BIN" ls HEAD --blame=0)" "$GIT_LS" -l
  # limit 0 examines only HEAD itself, so even main.go (untouched by HEAD)
  # falls back to HEAD's author.
  assert_contains "$("$GIT_LS" -l src)" "test@example.com"
}

test_invalid_config_value_fails() {
  git config gitls.blameLimit abc
  assert_false "\"$GIT_LS\" -l >/dev/null 2>&1"
}

test_explicit_blame_flag_wins_over_config_default() {
  both "$("$GITFS_BIN" ls HEAD --blame=0)" "$GIT_LS" -l --blame=0
}

test_bare_blame_passthrough_is_unbounded() {
  both "$("$GITFS_BIN" ls HEAD --blame)" "$GIT_LS" --blame
}

test_available_as_git_subcommand() {
  assert_eq "$("$GITFS_BIN" ls HEAD)" \
    "$(PATH="$(dirname "$GIT_LS"):$PATH" git ls)"
}

run_tests
