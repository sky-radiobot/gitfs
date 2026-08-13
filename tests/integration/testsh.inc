# testsh — minimal test runner for bash
# Source this file, define test_* functions, then call run_tests.
#
# Taken verbatim from github.com/radiospiel/critic
# (src/git-follow/integration/testsh.inc).

_passed=0
_failed=0
_failures=()

pass() {
  _passed=$((_passed + 1))
}

fail() {
  local msg="${1:-assertion failed}"
  echo "  FAIL: $msg"
  _failed=$((_failed + 1))
  _failures+=("$_current_test: $msg")
}

assert_eq() {
  local expected="$1" actual="$2" msg="${3:-expected '$1' got '$2'}"
  if [[ "$expected" == "$actual" ]]; then
    pass
  else
    fail "$msg"
  fi
}

assert_neq() {
  local unexpected="$1" actual="$2" msg="${3:-expected value to differ from '$1'}"
  if [[ "$unexpected" != "$actual" ]]; then
    pass
  else
    fail "$msg"
  fi
}

assert_match() {
  local pattern="$1" actual="$2" msg="${3:-expected '$2' to match '$1'}"
  if [[ "$actual" =~ $pattern ]]; then
    pass
  else
    fail "$msg"
  fi
}

assert_contains() {
  local haystack="$1" needle="$2" msg="${3:-expected '$1' to contain '$2'}"
  if [[ "$haystack" == *"$needle"* ]]; then
    pass
  else
    fail "$msg"
  fi
}

assert_true() {
  local msg="${2:-expected command to succeed}"
  if eval "$1"; then
    pass
  else
    fail "$msg"
  fi
}

assert_false() {
  local msg="${2:-expected command to fail}"
  if eval "$1"; then
    fail "$msg"
  else
    pass
  fi
}

run_tests() {
  local tests
  tests=$(declare -F | awk '{print $3}' | grep '^test_')

  for t in $tests; do
    _current_test="$t"
    echo "--- $t"
    if declare -f setup >/dev/null 2>&1; then setup; fi

    local before_failed=$_failed
    "$t"
    if (( _failed == before_failed )); then
      echo "  ok"
    fi

    if declare -f teardown >/dev/null 2>&1; then teardown; fi
  done

  echo ""
  echo "=== Results: $_passed passed, $_failed failed ==="
  if (( ${#_failures[@]} > 0 )); then
    echo "Failures:"
    for f in "${_failures[@]}"; do
      echo "  - $f"
    done
  fi
  (( _failed == 0 ))
}
