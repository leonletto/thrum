#!/usr/bin/env bash
# Scenario: restart-coord-fixture-restart (migrates full_test_plan.md § 10.4)
#
# Pins the `thrum tmux restart` success-line contract AND captures the
# snapshot file content to a temp path before the new claude session
# can consume it via /thrum:prime. The capture is what scenario 74
# asserts against — once the new session's auto-prime runs (within
# ~15s of restart returning), the snapshot file gets read and the
# default consume-on-load path deletes it. So we have a window of
# seconds to copy-and-stash the file's contents.
#
# Two assertions:
#   1. restart-success-line — `thrum tmux restart` stdout contains the
#      canonical "restarted" success token (the daemon's restart RPC
#      emits "Session <name> restarted (N snapshot lines)"; we match
#      the substring "restarted" because the snapshot-line count is
#      input-dependent and the substring "snapshot lines" only
#      appears on the snapshot-positive path which is what we want).
#   2. restart-snapshot-captured — the snapshot file existed at
#      $KAFM6_S1_WT/.thrum/restart/${KAFM6_S1_AGENT}.md immediately
#      after the restart returned AND we successfully copied it to
#      a /tmp staging path for scenario 74 to read.
#
# Depends on scenario 70 (fixture); side effect: kicks the kafm6
# pane into a new claude session.

SID="73-restart-coord-fixture-restart"

if [ -z "${KAFM6_S1_AGENT:-}" ] || [ -z "${KAFM6_S1_SESSION:-}" ] || [ -z "${KAFM6_S1_WT:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 70 fixture identifiers exported" \
    "(missing — scenario 70 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
KAFM6_S1_SNAPSHOT_FILE="$KAFM6_S1_WT/.thrum/restart/$KAFM6_S1_AGENT.md"
KAFM6_S1_SNAPSHOT_STAGING="/tmp/kafm6-73-snapshot-${RUNID}.md"

_run_scenario_73() {

# Step 1: settle the kafm6 pane before restart so the snapshot extracts
# a stable JSONL state (avoids capturing a half-rendered turn).
wait_for_pane_idle "$KAFM6_S1_SESSION" 30

# Step 2: drive `thrum tmux restart` from coord identity via tmux-exec
# (PID-chain break). Capture stdout for the success-line assertion.
# NOT --force here: § 10.4's contract is the GRACEFUL restart path
# (snapshot is saved as part of the restart, not a kill-first flow).
local restart_out restart_rc
restart_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux restart "$KAFM6_S1_SESSION" 2>&1
)
restart_rc=$?

# Step 3: as fast as possible, stage the snapshot file. The new claude
# pane will run /thrum:prime within ~15s and the default behavior
# consumes the snapshot on read. Copying right after restart returns
# keeps us well inside that window.
local stage_rc=1
if [ -s "$KAFM6_S1_SNAPSHOT_FILE" ]; then
  cp "$KAFM6_S1_SNAPSHOT_FILE" "$KAFM6_S1_SNAPSHOT_STAGING" 2>/dev/null && stage_rc=0
fi

# Assertion 1: restart success line.
if [ "$restart_rc" -eq 0 ] && printf '%s' "$restart_out" | grep -q "restarted"; then
  emit_pass "$SID" "restart-success-line"
else
  emit_fail "$SID" "restart-success-line" \
    'thrum tmux restart exits 0 AND stdout contains "restarted"' \
    "exit ${restart_rc}; output: $(printf '%s' "$restart_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: snapshot was staged for scenario 74.
if [ "$stage_rc" -eq 0 ] && [ -s "$KAFM6_S1_SNAPSHOT_STAGING" ]; then
  emit_pass "$SID" "restart-snapshot-captured"
else
  emit_fail "$SID" "restart-snapshot-captured" \
    "snapshot file at ${KAFM6_S1_SNAPSHOT_FILE} copied to ${KAFM6_S1_SNAPSHOT_STAGING}" \
    "(file missing or empty post-restart — daemon's restart RPC may not have written the snapshot, or the new prime consumed it before we could stage)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_73

_run_scenario_73

# Export staging path for scenario 74.
export KAFM6_S1_SNAPSHOT_STAGING KAFM6_S1_SNAPSHOT_FILE
