#!/usr/bin/env bash
# Scenario: check-pane-auto-nudge (migrates full_test_plan.md § 10D.11)
#
# Verifies the daemon's `tmux.check-pane` handler dispatches a nudge
# to an agent self-reporting `agent_status="working"` whose pane is
# silent (working_but_idle branch in HandleCheckPane). The
# observable side-effect is the daemon-side `Nudge()` call typing
# "New message from @daemon" + Enter into the target pane —
# captured here via `tmux capture-pane`.
#
# CLI output is NOT used as the assertion anchor because the
# `thrum tmux check-pane` CLI silently discards the
# CheckPaneResponse (cmd/thrum/main.go: `_ = client.Call(...)`)
# rather than printing the resolved state. The pane-side nudge
# text is the only externally observable signal of the
# working_but_idle branch firing end-to-end.
#
# Per-scenario branch-backed worktree (kafm9-91-nudge-wt) +
# inline-registered managed session (kafm9-91-nudge), driven from
# COORD_PANE per the scenario 69 caller-identity pattern.
# Runtime is shell — claude would burn API budget on the
# nudge-typed inbox prompt for no contract gain.
#
# Both `thrum tmux create` AND `thrum tmux launch` are driven from
# COORD_PANE via `send_bash_and_wait`. Scenarios 45/69 launch via
# tmux-exec successfully; in this scenario's setup tmux-exec
# launch reproducibly returned non-zero with only the
# `worktree.PaneTargetForIdentity refused` warn observable, so the
# launch was moved to coord-pane drive (mirrors scenario 13's
# create-from-coord pattern). The check-pane fire below remains
# tmux-exec because read-only RPCs don't trigger the
# caller-pane refusal we observed on launch.

SID="91-check-pane-auto-nudge"
NUDGE_WT_NAME="kafm9-91-nudge-wt"
NUDGE_WT="$WORKTREE_BASE/repo/$NUDGE_WT_NAME"
NUDGE_SESSION="kafm9-91-nudge"
NUDGE_AGENT="kafm9_91_nudge_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_91() {

wait_for_pane_idle "$COORD_PANE" 60

# Step 1: branch-backed worktree (mirrors scenario 69 — the
# inline-register tmux create requires a non-detached worktree
# under the post-nu16 caller-identity guard).
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$NUDGE_WT_NAME" \
      --branch "feature/${NUDGE_WT_NAME}" 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$NUDGE_WT" ]; then
  emit_fail "$SID" "nudge-fired" \
    "thrum worktree create $NUDGE_WT_NAME succeeds and produces $NUDGE_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: inline-register nudge_test_agent via thrum tmux create
# from COORD pane (caller-identity guard).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux create ${NUDGE_SESSION} --cwd ${NUDGE_WT} --name ${NUDGE_AGENT} --role tester --module testing --intent 'kafm9-91 nudge fixture'" \
    "Session created" 60; then
  emit_fail "$SID" "nudge-fired" \
    "thrum tmux create ${NUDGE_SESSION} succeeds with 'Session created' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_91_cleanup
  return 0
fi

# Step 3: launch shell runtime via COORD pane (caller-identity
# guard: tmux launch from a tmux-exec ephemeral caller hits the
# PaneTargetForIdentity refusal in this fixture context — the
# guard's expected_session=repo doesn't match the ephemeral
# session name, and unlike scenario 45/69 in our run-level layout
# the refusal returns non-zero. Driving from COORD_PANE matches
# scenario 13's create-from-coord pattern and avoids the issue).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux launch ${NUDGE_SESSION} --runtime shell" \
    "Launched shell" 60; then
  emit_fail "$SID" "nudge-fired" \
    "thrum tmux launch ${NUDGE_SESSION} --runtime shell driven from COORD pane outputs 'Launched shell'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_91_cleanup
  return 0
fi

# Brief settle: launch's HandleLaunch goroutine writes the row +
# starts the shell async. 3s mirrors scenario 45/69 timing.
sleep 3

# Step 4: set nudge_test_agent → working via daemon RPC
# (--agent flag → setRemoteAgentStatus path).
local sst_out sst_rc
sst_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum agent set-status working \
      --agent "$NUDGE_AGENT" 2>&1
)
sst_rc=$?
if [ "$sst_rc" -ne 0 ]; then
  emit_fail "$SID" "nudge-fired" \
    "set-status working --agent ${NUDGE_AGENT} exits 0" \
    "exit ${sst_rc}; output: $(printf '%s' "$sst_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_91_cleanup
  return 0
fi

# Brief poll: confirm the identity file is on disk before firing
# check-pane (HandleCheckPane reads the identity to detect the
# working_but_idle condition).
local nudge_id="$NUDGE_WT/.thrum/identities/${NUDGE_AGENT}.json"
local elapsed=0
while [ "$elapsed" -lt 5 ]; do
  if [ -f "$nudge_id" ] && \
     [ "$(jq -r '.agent_status // ""' "$nudge_id" 2>/dev/null)" = "working" ]; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

# Step 5: fire check-pane. The CLI walks the pane content + sends
# (session, content) to the daemon, which reads the identity's
# AgentStatus, sees "working" with no permission/queue activity,
# detects working_but_idle, and dispatches Nudge() to the pane.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux check-pane "$NUDGE_SESSION" \
  >/dev/null 2>&1 || true

# Step 6: poll the target pane for the nudge text. Nudge() chunks
# the message via send-keys with an inter-chunk delay; allow up to
# 10s for the full string + Enter to land.
local pane_capture nudge_seen=0
elapsed=0
while [ "$elapsed" -lt 10 ]; do
  pane_capture=$(tmux capture-pane -t "$NUDGE_SESSION" -p 2>/dev/null || true)
  if printf '%s' "$pane_capture" | grep -qE "New message from @daemon"; then
    nudge_seen=1
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ "$nudge_seen" -eq 1 ]; then
  emit_pass "$SID" "nudge-fired"
else
  emit_fail "$SID" "nudge-fired" \
    "tmux pane ${NUDGE_SESSION} contains 'New message from @daemon' after check-pane fire" \
    "(text not found in pane content within 10s)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

_scenario_91_cleanup

}  # _run_scenario_91

# Cleanup split out so failure paths can call it without
# duplicating the kill+teardown sequence inline.
_scenario_91_cleanup() {
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux kill "$NUDGE_SESSION" >/dev/null 2>&1 || true
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$NUDGE_WT_NAME" \
    >/dev/null 2>&1 || true
}

_run_scenario_91
