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
  # Defensive cleanup for the kafm.10 queue-test fixture. Scenario 49
  # tears it down explicitly on the happy path; this catches partial
  # failures (e.g. scenario 45 created the session but 46-49 errored
  # out before reaching scenario 49's cleanup).
  thrum tmux kill bare-queue 2>/dev/null || true
  thrum tmux kill queue-test 2>/dev/null || true
  # Defensive cleanup for the kafm.8 multi-runtime fixtures. Scenario
  # 68 tears down rt-scratch (worktree + sessions) on the happy path;
  # scenarios 67 and 69 tear down their own per-scenario worktrees.
  # This catches partial failures and orphaned sessions from any of
  # 62-69.
  thrum tmux kill runtime-test 2>/dev/null || true
  thrum tmux kill invalid-rt-test 2>/dev/null || true
  thrum tmux kill prime-rt-test 2>/dev/null || true
  thrum tmux kill bare-session 2>/dev/null || true
  thrum tmux kill tmux-start-test 2>/dev/null || true
  thrum tmux kill force-restart-test 2>/dev/null || true
  # Defensive cleanup for the kafm.6 restart-snapshot fixtures. Scenario
  # 76 tears down kafm6-impl-restart-test on the happy path; scenario 80
  # tears down kafm6-self-restart-test. This catches partial failures
  # and the bare-tmux relaunch in scenario 79 that the daemon doesn't
  # know about (raw tmux kill needed to reach it).
  thrum tmux kill kafm6-impl-restart-test 2>/dev/null || true
  thrum tmux kill kafm6-self-restart-test 2>/dev/null || true
  tmux kill-session -t kafm6-impl-restart-test 2>/dev/null || true
  tmux kill-session -t kafm6-self-restart-test 2>/dev/null || true
  # Defensive cleanup for the kafm.9 orchestrator-infrastructure
  # fixtures. Scenarios 86/88/91 each tear down their own
  # session+worktree on the happy path; this catches partial
  # failures that bailed before explicit teardown. Sub-fixture
  # daemons (85/89/90) are stopped by their own scenarios.
  thrum tmux kill kafm9-86-orch-session 2>/dev/null || true
  thrum tmux kill kafm9-88-session 2>/dev/null || true
  thrum tmux kill kafm9-91-nudge 2>/dev/null || true
  # Per-scenario worktree teardowns alongside the session kills —
  # rm -rf $WORKTREE_BASE below is a backstop that bypasses
  # daemon-internal cleanup (git worktree prune, .thrum redirect
  # state). Mirrors kafm.6/8 precedent.
  thrum worktree teardown kafm9-86-orch >/dev/null 2>&1 || true
  thrum worktree teardown kafm9-88-orch >/dev/null 2>&1 || true
  thrum worktree teardown kafm9-91-nudge-wt >/dev/null 2>&1 || true
  # Defensive cleanup for the kafm.11 monitor-jobs fixture. Scenarios
  # 92-98 share a single monitor + log file driven against the
  # run-level daemon. Happy-path stop happens in scenario 96; the
  # daemon stop below reaps any lingering monitor child process when
  # the daemon shuts down, and rm -rf "$BASE" clears the SQLite
  # monitor rows along with the rest of the fixture state. The only
  # side-effect that lives outside $BASE is the temp log file under
  # /tmp at a deterministic RUNID-keyed path; remove that exact path
  # only — a glob would match log files from concurrent suite runs
  # on the same machine and wipe their artifacts.
  rm -f "/tmp/thrum-monitor-kafm11-${RUNID}.log"
  if [ -n "${REPO:-}" ] && [ -d "$REPO" ]; then
    (cd "$REPO" && thrum daemon stop) >/dev/null 2>&1 || true
  fi
  rm -rf "$BASE"
  # WORKTREE_BASE is at a separate parent path from BASE (so thrum's repo-name
  # auto-append doesn't collide with $REPO), so it needs its own cleanup.
  if [ -n "${WORKTREE_BASE:-}" ]; then
    rm -rf "$WORKTREE_BASE"
  fi
  return 0
}
