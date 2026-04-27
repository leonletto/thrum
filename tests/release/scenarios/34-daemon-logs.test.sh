#!/usr/bin/env bash
# Scenario: daemon-logs (migrates full_test_plan.md § 4.8)
#
# Verifies `thrum daemon logs` flag combinations exit 0 and surface
# log content (when there is any) without error. Pure read-only CLI
# against the run-level fixture's daemon.
#
# Four assertions on the contract surface:
#   1. --lines 5 → exit 0
#   2. --since 1m → exit 0
#   3. --lines 0 → exit 0 (sentinel: "all lines")
#   4. --lines 3 --since 1h → exit 0 (combined flags)
#
# Output content (timestamps, levels) is not strictly asserted —
# logs may be sparse on a freshly-spawned daemon, and asserting the
# exact format here would couple the test to the slog handler's
# output style. The exit-code contract is what § 4.8 documents.

SID="34-daemon-logs"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_logs_check() {
  local name="$1"
  shift
  local out
  out="$(mktemp -t kafm1-34-${name}.XXXXXX).txt"
  "$TE" exec --cwd "$COORD_REPO" --clean -- thrum daemon logs "$@" \
    > "$out" 2>&1
  local rc=$?
  if [ "$rc" -eq 0 ]; then
    emit_pass "$SID" "$name"
  else
    local got
    got="$(tr '\n' ' ' < "$out" | head -c 240)"
    emit_fail "$SID" "$name" \
      "thrum daemon logs $* exits 0" \
      "rc=${rc}; output: ${got:-<empty>}" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
  rm -f "$out"
}

_logs_check "lines-5"          --lines 5
_logs_check "since-1m"         --since 1m
_logs_check "lines-0"          --lines 0
_logs_check "lines-3-since-1h" --lines 3 --since 1h
