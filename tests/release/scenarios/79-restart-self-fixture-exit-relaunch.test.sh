#!/usr/bin/env bash
# Scenario: restart-self-fixture-exit-relaunch (migrates full_test_plan.md § 10.10)
#
# Exercises the bare-tmux exit-and-relaunch path the markdown spec
# documents: kill the tmux session via raw `tmux kill-session` (NOT
# `thrum tmux kill`, which would also drop daemon-side bookkeeping),
# verify the snapshot file scenario 77 wrote survives the kill, then
# spawn a fresh tmux session at the same cwd and relaunch claude with
# THRUM_NAME pinned. § 10.10's contract is the durability of the
# snapshot across an unexpected runtime exit AND the survival of the
# agent identity across a manual relaunch.
#
# Two assertions:
#   1. snapshot-survives-kill — snapshot file still on disk after the
#      raw tmux kill of the agent's session.
#   2. session-relaunched — new tmux session is running claude AND a
#      fresh SessionStart attachment lands in JSONL after relaunch
#      (timestamp filtered to entries newer than relaunch start).
#
# Why raw tmux kill (not thrum tmux kill): the markdown spec
# explicitly tests the bare-exit recovery path. `thrum tmux kill`
# routes through the daemon and triggers RemoveSession which clears
# bookkeeping cleanly — useful in real life, but not the recovery
# path the snapshot file is designed to survive. Raw kill leaves
# the daemon's session row stale (state=dead) and the agent's
# identity file stale (tmux_session points at a no-longer-existing
# session) — exactly the failure mode `thrum prime` and the
# snapshot file are meant to recover from.
#
# Depends on scenario 77 (fixture exists; snapshot file written).

SID="79-restart-self-fixture-exit-relaunch"

if [ -z "${KAFM6_S2_AGENT:-}" ] || [ -z "${KAFM6_S2_SESSION:-}" ] || [ -z "${KAFM6_S2_WT:-}" ] || [ -z "${KAFM6_S2_SNAPSHOT_FILE:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 77 fixture identifiers exported" \
    "(missing — scenarios 77 + 78 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

_run_scenario_79() {

# Step 1: raw tmux kill of the agent's session. Bypasses the daemon.
tmux kill-session -t "$KAFM6_S2_SESSION" 2>/dev/null || true

# Brief settle: kill returns synchronously but on-disk inotify-style
# watchers (none in thrum, but reserved for safety) need a tick to
# observe the dead pane.
sleep 1

# Assertion 1: snapshot file still on disk after the kill. The
# contract is durability across runtime exit; if a regression in
# the snapshot save path made it ephemeral or if the runtime tear-
# down triggered a stale-cleanup, this would catch it.
if [ -s "$KAFM6_S2_SNAPSHOT_FILE" ]; then
  emit_pass "$SID" "snapshot-survives-kill"
else
  emit_fail "$SID" "snapshot-survives-kill" \
    "snapshot file at ${KAFM6_S2_SNAPSHOT_FILE} present after raw tmux kill" \
    "(file missing or empty — kill path may be dropping snapshots)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: capture a floor timestamp BEFORE relaunch so the post-
# relaunch SessionStart poll doesn't false-match the pre-kill
# session's SessionStart entries (still present in the project
# dir's older *.jsonl files).
local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

# Step 3: bare new-session at the same cwd, then send-keys to
# launch claude with THRUM_NAME pinned. Mirrors markdown § 10.10
# verbatim. -x 500 / -y 50 mirrors the run-level setup-repo.sh
# tmux pane geometry — large enough to render any briefing
# without column-wrap (memory: tmux-capture-pane-json-wrap).
tmux new-session -d -s "$KAFM6_S2_SESSION" -c "$KAFM6_S2_WT" -x 500 -y 50

# Settle the new session before keystrokes (claude trust dialog
# would not re-fire here because scenario 77 already touched this
# cwd — claude trusts known cwds — but settle is cheap insurance).
wait_for_pane_idle "$KAFM6_S2_SESSION" 10

# Launch claude with identity pinned. unset CLAUDECODE matches the
# markdown spec verbatim (avoids inheriting an outer claude marker).
# Bare `claude` (no --dangerously-skip-permissions): per
# helpers/fixture-perms.sh:17, bypass mode triggers a SECOND modal
# whose default is "No, exit" — the defensive Enter below would kill
# the session. The `! true` kick at the end uses bash-prefix mode
# which BYPASSES claude's tool-permission system entirely (see
# fixture-perms.sh:12-15), so no allowlist is needed for this
# scenario.
tmux send-keys -t "$KAFM6_S2_SESSION" \
  "unset CLAUDECODE && THRUM_NAME=$KAFM6_S2_AGENT claude --model haiku"
sleep 0.5
tmux send-keys -t "$KAFM6_S2_SESSION" Enter

# Trust dialog: even on known cwds, the welcome screen sometimes
# wants Enter once before claude is interactive. Mirror
# spawn_sub_fixture_claude's approach: wait_for_pane_idle then a
# defensive Enter. Harmless if the prompt isn't there.
wait_for_pane_idle "$KAFM6_S2_SESSION" 30
tmux send-keys -t "$KAFM6_S2_SESSION" Enter

# Claude writes ZERO JSONL until first user input — the welcome
# banner is keystrokes-only. SessionStart hooks fire but their
# output is buffered until the JSONL file is created on first
# real input. Kick the new session with `! true` (mirrors
# kick_session_then_wait) so the SessionStart attachment with
# the snapshot-loaded briefing actually flushes to disk for the
# polling filter below.
wait_for_pane_idle "$KAFM6_S2_SESSION" 30
send_command "$KAFM6_S2_SESSION" "! true"

# Step 4: poll for a SessionStart attachment with timestamp >= floor_ts
# (proves the NEW claude wrote its own JSONL with hooks fired). 60s
# matches setup-repo.sh's bootloader headroom.
local relaunch_filter='.type == "attachment"
        and (.attachment.hookEvent == "SessionStart")
        and (.timestamp >= "'"$floor_ts"'")'
if wait_for_jsonl_match "$KAFM6_S2_WT" "$relaunch_filter" 90 >/dev/null; then
  emit_pass "$SID" "session-relaunched"
else
  # Capture pane content for diagnostics — a hung trust dialog,
  # missing claude binary, or shell-syntax error would all surface
  # here.
  local pane_capture
  pane_capture=$(tmux capture-pane -t "$KAFM6_S2_SESSION" -p 2>&1 | tr '\n' ' ' | head -c 320)
  emit_fail "$SID" "session-relaunched" \
    "post-relaunch SessionStart attachment in JSONL with timestamp >= ${floor_ts}" \
    "(none observed within 90s; pane content: ${pane_capture:-<empty>})" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Export floor_ts for scenario 80 so its assertions can scope to
# entries from this relaunch (vs. the pre-kill session's entries).
export KAFM6_S2_RELAUNCH_FLOOR_TS="$floor_ts"

}  # _run_scenario_79

_run_scenario_79
