#!/usr/bin/env bash
# tests/release/helpers/teardown.sh — run-level teardown (spec § 4 step E).
# Idempotent: safe to call after partial setup failure or crashed prior run.

run_teardown() {
  # If RUNID was never set (setup failed before exporting), nothing to do.
  if [ -z "${RUNID:-}" ] || [ -z "${BASE:-}" ]; then
    return 0
  fi

  thrum tmux kill coord 2>/dev/null || true
  thrum tmux kill impl 2>/dev/null || true
  # Daemon was started by `thrum init` in $BASE/main, not $REPO (the worktree).
  if [ -n "${BASE:-}" ] && [ -d "$BASE/main" ]; then
    (cd "$BASE/main" && thrum daemon stop) >/dev/null 2>&1 || true
  fi
  rm -rf "$BASE"
  unset THRUM_HOME    # leave caller env clean
  return 0
}
