#!/usr/bin/env bash
# Scenario: restart-coord-fixture-cleanup (migrates full_test_plan.md § 10.7)
#
# Tears down the Scenario 1 fixture set up by scenario 70: kills the
# kafm6-impl-restart-test tmux session and tears down the
# kafm6-impl-restart-wt worktree. § 10.7's contract is just the
# cleanup, but we also pin two assertions to make a regression in
# the teardown path visible:
#
#   1. fixture-tmux-killed — `thrum tmux status --json` no longer
#      reports the kafm6 session.
#   2. fixture-worktree-removed — the worktree directory no longer
#      exists on disk.
#
# Splitting cleanup into its own scenario file (rather than absorbing
# into 75) keeps the contract auditable: a teardown regression
# surfaces as a 76-tagged failure instead of being hidden inside an
# unrelated assertion's late stages. helpers/teardown.sh's defensive
# fallback also catches the no-77-runs partial-failure path.
#
# Also stages /tmp cleanup of the snapshot file scenario 73 wrote.

SID="76-restart-coord-fixture-cleanup"

if [ -z "${KAFM6_S1_SESSION:-}" ] || [ -z "${KAFM6_S1_WT_NAME:-}" ] || [ -z "${KAFM6_S1_WT:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 70 fixture identifiers exported" \
    "(missing — scenario 70 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_76() {

# Step 1: kill the tmux session. Driver-side via tmux-exec for the
# PID-chain break.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux kill "$KAFM6_S1_SESSION" \
  >/dev/null 2>&1 || true

# Step 2: tear down the worktree. tmux session must be killed first —
# worktree teardown otherwise refuses to remove a worktree with an
# active agent attached to it.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum worktree teardown "$KAFM6_S1_WT_NAME" \
  >/dev/null 2>&1 || true

# Step 3: remove staged snapshot if it's still around (typically
# scenario 73 wrote it; 80 doesn't share staging paths with this
# chain).
[ -n "${KAFM6_S1_SNAPSHOT_STAGING:-}" ] && rm -f "$KAFM6_S1_SNAPSHOT_STAGING"

# Step 4: poll briefly for the daemon to drop the session row.
local status_file="/tmp/kafm6-76-status-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  capture_thrum_json "$COORD_REPO" test_coordinator_main "$status_file" tmux status
  if ! jq -e --arg n "$KAFM6_S1_SESSION" \
      '.sessions[]? | select(.name == $n)' \
      "$status_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion 1: tmux session no longer in daemon's bookkeeping.
if ! jq -e --arg n "$KAFM6_S1_SESSION" \
    '.sessions[]? | select(.name == $n)' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "fixture-tmux-killed"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "fixture-tmux-killed" \
    "tmux status --json no longer contains ${KAFM6_S1_SESSION}" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_file"

# Assertion 2: worktree directory removed from disk.
elapsed=0
while [ -d "$KAFM6_S1_WT" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ ! -d "$KAFM6_S1_WT" ]; then
  emit_pass "$SID" "fixture-worktree-removed"
else
  emit_fail "$SID" "fixture-worktree-removed" \
    "worktree dir at ${KAFM6_S1_WT} removed by teardown" \
    "(directory still present after 10s — worktree teardown failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_76

_run_scenario_76
