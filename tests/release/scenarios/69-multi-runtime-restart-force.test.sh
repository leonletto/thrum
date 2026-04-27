#!/usr/bin/env bash
# Scenario: multi-runtime-restart-force (migrates full_test_plan.md § 10C.10)
#
# Pins the contract that `thrum tmux restart <name> --force` works
# end-to-end against an agent-registered managed session: the
# command exits 0, and the session is alive after the forced
# restart. --force skips the graceful conversation-snapshot wait
# and tears down the runtime process immediately.
#
# Differs from 62/67/68 in two ways: the session is registered with
# an inline agent (`--name force_agent --role tester`), not bare;
# and the worktree is detached (matches markdown § 10C.10's
# `worktree create --detach`). Per-scenario worktree
# (test-force-restart) keeps the registered-agent fixture isolated
# from the run-level coord/impl agents.
#
# Two assertions:
#   1. force-restart-success — `thrum tmux restart force-restart-test
#      --force` exits 0.
#   2. force-restart-session-alive — `thrum tmux status --json`
#      reports force-restart-test with state=alive within 10s of
#      the restart returning.

SID="69-multi-runtime-restart-force"
FORCE_WT_NAME="test-force-restart"
FORCE_WT="$WORKTREE_BASE/repo/$FORCE_WT_NAME"
FORCE_SESSION="force-restart-test"
# RUNID contains a hyphen (date-PID shape), but the daemon's agent-name
# validation rejects '-' (regex [a-zA-Z0-9_]+). Convert to underscore
# so the suffix is still per-run-unique without violating the guard.
FORCE_AGENT="force_agent_${RUNID//-/_}"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_69() {

# Step 1: detached-HEAD worktree from coord identity.
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$FORCE_WT_NAME" \
      --detach 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$FORCE_WT" ]; then
  emit_fail "$SID" "force-restart-success" \
    "thrum worktree create $FORCE_WT_NAME --detach succeeds and produces $FORCE_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: agent-registered tmux session. Inline registration via the
# tmux create flags (matches markdown § 10C.10's invocation shape;
# same pattern setup-repo.sh:135 uses for the impl pane). RUNID
# suffix on the agent name avoids collision across re-runs that
# might survive teardown failures.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux create "$FORCE_SESSION" \
    --cwd "$FORCE_WT" \
    --name "$FORCE_AGENT" \
    --role tester \
    --module testing \
    --intent "Force restart test" >/dev/null 2>&1 || {
    emit_fail "$SID" "force-restart-success" \
      "thrum tmux create $FORCE_SESSION (agent-registered) succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    _scenario_69_cleanup
    return 0
  }

# Step 3: launch shell. The runtime choice is incidental to the
# restart contract — shell is the cheap, always-available choice.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux launch "$FORCE_SESSION" \
    --runtime shell >/dev/null 2>&1 || {
    emit_fail "$SID" "force-restart-success" \
      "thrum tmux launch $FORCE_SESSION --runtime shell succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    _scenario_69_cleanup
    return 0
  }

# Brief settle: launch's HandleLaunch goroutine writes the session
# row asynchronously after returning. 3s mirrors the markdown spec
# timing and is enough for the row to land before we ask for
# restart.
sleep 3

# Step 4: forced restart.
local restart_out restart_rc
restart_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux restart "$FORCE_SESSION" \
      --force 2>&1
)
restart_rc=$?
if [ "$restart_rc" -eq 0 ]; then
  emit_pass "$SID" "force-restart-success"
else
  emit_fail "$SID" "force-restart-success" \
    "thrum tmux restart $FORCE_SESSION --force exits 0" \
    "exit ${restart_rc}; output: $(printf '%s' "$restart_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_69_cleanup
  return 0
fi

# Step 5: poll status for alive. Force-restart's row update runs
# async too — the restart returns once the kill side completes,
# and the new pane comes up over the next few seconds. 10s polling
# matches scenarios 62/67/68.
local status_file="/tmp/kafm8-69-status-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "thrum tmux status --json > '$status_file' 2>/dev/null"
  if jq -e --arg n "$FORCE_SESSION" \
      '.sessions[]? | select(.name == $n and .state == "alive")' \
      "$status_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if jq -e --arg n "$FORCE_SESSION" \
    '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "force-restart-session-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "force-restart-session-alive" \
    "tmux status --json contains $FORCE_SESSION with state=alive after force restart" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_file"

_scenario_69_cleanup

}  # _run_scenario_69

# Cleanup split out so failure paths can call it without
# duplicating the kill+teardown sequence inline. Force-restart
# leaves an agent registration (force_agent_<RUNID>) — worktree
# teardown handles its identity file via .thrum/redirect, but the
# tmux session must be killed explicitly first.
_scenario_69_cleanup() {
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    thrum tmux kill "$FORCE_SESSION" >/dev/null 2>&1 || true
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$FORCE_WT_NAME" \
    >/dev/null 2>&1 || true
}

_run_scenario_69
