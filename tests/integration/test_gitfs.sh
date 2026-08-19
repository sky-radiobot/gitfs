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
# CLI arguments, run from the current directory.
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
  both "$expected" "$SHA" cat README.md

  expected=$(git -C "$REPO" show "$SHA:src/app/main.go")
  both "$expected" "$SHA" cat src/app/main.go
}

test_cat_multiple_paths_concatenates() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md"; git -C "$REPO" show "$SHA:docs/index.md")
  both "$expected" "$SHA" cat README.md docs/index.md
}

test_cat_symlink_returns_target() {
  both "README.md" "$SHA" cat link
}

test_ls_root_default_is_plain_names() {
  local expected
  expected=$(git -C "$REPO" ls-tree --name-only "$SHA" | LC_ALL=C sort)
  both "$expected" "$SHA" ls
}

test_ls_long_matches_git() {
  local expected
  expected=$(git -C "$REPO" ls-tree --name-only "$SHA" | LC_ALL=C sort)
  assert_eq "$expected" "$("$GITFS_BIN" "$SHA" ls -l | cut -f3-)"
  assert_eq "$expected" "$("$GITFS_BIN" -git-binary "$GIT_BIN" "$SHA" ls -l | cut -f3-)"
}

test_ls_subdirectory() {
  both "app" "$SHA" ls src
  both "drwxr-xr-x${TAB}0${TAB}app" "$SHA" ls -l src
}

test_ls_multiple_paths_prints_headers() {
  both "$(printf 'src:\napp\n\ndocs:\nindex.md')" "$SHA" ls src docs
}

test_bare_repo_discovered_from_cwd() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  (
    cd "$BARE/repo.git"
    assert_eq "$expected" "$("$GITFS_BIN" "$SHA" cat README.md)"
    assert_eq "$expected" "$("$GITFS_BIN" -git-binary "$GIT_BIN" "$SHA" cat README.md)"
    assert_eq "app" "$("$GITFS_BIN" "$SHA" ls src)"
  )
}

test_repo_discovered_from_subdirectory() {
  (
    cd "$REPO/src/app"
    assert_eq "package main" "$("$GITFS_BIN" "$SHA" cat main.go)"
  )
}

test_sparse() {
  # Root listing is limited to the ancestors of the sparse paths.
  both "src" -sparse src/app "$SHA" ls
  both "app" -sparse src/app "$SHA" ls src

  # Inside the sparse set: readable.
  both "package main" -sparse src/app "$SHA" cat src/app/main.go

  # Outside the sparse set: indistinguishable from absent.
  both_fail -sparse src/app "$SHA" cat README.md
  both_fail -sparse src/app "$SHA" ls docs
}

test_resolves_branch_name() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" main cat README.md
}

test_resolves_tag() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" v1 cat README.md
}

test_resolves_head() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" HEAD cat README.md
}

test_resolves_short_sha() {
  local expected
  expected=$(git -C "$REPO" show "$SHA:README.md")
  both "$expected" "${SHA:0:10}" cat README.md
}

test_rejects_unresolvable_ref() {
  both_fail no-such-ref cat README.md

  local err
  err=$("$GITFS_BIN" no-such-ref cat README.md 2>&1 || true)
  assert_contains "$err" "cannot resolve"
}

test_rejects_unresolvable_full_sha() {
  both_fail 0000000000000000000000000000000000000000 cat README.md
}

test_rejects_non_repo() {
  local outside
  outside=$(mktemp -d)
  (
    cd "$outside"
    if "$GITFS_BIN" "$SHA" cat README.md >/dev/null 2>&1; then
      fail "expected failure outside a git repository"
    else
      pass
    fi
  )
  rm -rf "$outside"
}

test_missing_git_binary_fails() {
  if "$GITFS_BIN" -git-binary /nonexistent/git "$SHA" cat README.md >/dev/null 2>&1; then
    fail "expected failure with a missing git binary"
  else
    pass
  fi
}

test_missing_path_fails() {
  both_fail "$SHA" cat nope.txt
  both_fail "$SHA" ls nope
}

# -- run ----------------------------------------------------------------------

run_tests
