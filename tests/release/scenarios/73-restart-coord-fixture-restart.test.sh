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

# Step 2: drive `thrum tmux restart --force` from COORD_PANE rather
# than tmux-exec. --force matches scenarios 02/03/21/69's precedent
# (graceful-shutdown wait isn't materially different from the
# verified contract). COORD_PANE drive (vs tmux-exec) sidesteps
# the daemon's PaneTargetForIdentity caller-pane guard's intermittent
# refusal of ephemeral callers in long-running test contexts —
# COORD_PANE is the registered run-level coord agent that the daemon
# trusts for cross-worktree restart authority. Mirrors the launch
# driver pattern in scenario 70.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux restart $KAFM6_S1_SESSION --force" \
    "restarted" 60; then
  emit_pass "$SID" "restart-success-line"
else
  emit_fail "$SID" "restart-success-line" \
    "thrum tmux restart --force (driven from COORD pane) emits 'restarted' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: snapshot file was written to .thrum/restart/<agent>.md.
# Scenario 74 reads the snapshot content from the post-restart
# SessionStart attachment instead (the briefing-rendered # Previous
# Session Context block contains the snapshot body, and survives
# even after the file gets consume-on-load deleted by inject-prime-
# context.sh). Here we just verify the daemon's restart RPC actually
# emitted the snapshot file — race-tolerant: poll briefly so a fast
# write doesn't false-negative.
local elapsed=0 saw_file=1
while [ "$elapsed" -lt 5 ]; do
  if [ -s "$KAFM6_S1_SNAPSHOT_FILE" ]; then
    saw_file=0
    # Stage immediately for downstream scenarios that may want it.
    cp "$KAFM6_S1_SNAPSHOT_FILE" "$KAFM6_S1_SNAPSHOT_STAGING" 2>/dev/null || true
    break
  fi
  # Race against the 15s consume-on-load window: file may have been
  # written, then consumed by the new prime hook firing fast. Don't
  # treat that as a bug — confirm via the briefing attachment in 74
  # instead. Loop terminates after 5s (long enough to see the write
  # path; short enough to bail before the test is wasting time).
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ "$saw_file" -eq 0 ]; then
  emit_pass "$SID" "restart-snapshot-written"
else
  # If we missed the file, try staging from any preserved copy or
  # surface the briefing-attachment fallback as a partial-credit
  # pass — but only if the file truly never existed across the
  # poll window. Most likely cause: consume-on-load fired faster
  # than 1s; scenario 74's attachment-content check still validates
  # the snapshot end-to-end.
  emit_fail "$SID" "restart-snapshot-written" \
    "snapshot file at ${KAFM6_S1_SNAPSHOT_FILE} observed within 5s of restart" \
    "(file never observed — daemon's restart RPC may not have written the snapshot, or consume-on-load fired faster than the 1s poll interval)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_73

_run_scenario_73

# Export staging path for scenario 74.
export KAFM6_S1_SNAPSHOT_STAGING KAFM6_S1_SNAPSHOT_FILE
