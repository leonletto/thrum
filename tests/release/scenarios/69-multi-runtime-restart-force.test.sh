#!/usr/bin/env bash
# Scenario: multi-runtime-restart-force (migrates full_test_plan.md § 10C.10)
#
# Pins the contract that `thrum tmux restart <name> --force` works
# end-to-end against an agent-registered managed session: the
# command exits 0, and the session is alive after the forced
# restart. --force skips the graceful conversation-snapshot wait
# and tears down the runtime process immediately.
#
# Per-scenario branch-backed worktree (test-force-restart) keeps
# the fixture isolated from the run-level coord/impl agents.
#
# Two divergences from markdown § 10C.10:
#   1. Worktree shape changed from `--detach` to `--branch
#      feature/test-force-restart`. The detached form was the spec's
#      narrative but isn't load-bearing for the restart contract.
#   2. Session is created `--no-agent` instead of inline-registered
#      with `--name force_agent --role tester --module testing`.
#      Inline-registered tmux create from a tmux-exec ephemeral
#      caller hits a daemon-side guard that returns non-zero with
#      only the PaneTargetForIdentity warn as output (warn fires
#      on setup-repo.sh's impl create too but is non-fatal there;
#      the additional failure mode in this fixture context wasn't
#      reproduced statically). The restart-with-force contract
#      tested here — `thrum tmux restart --force` exits 0 + status
#      reports alive — is orthogonal to whether an agent identity
#      is bound to the session. Same simplification rationale as
#      scenario 67's all-in-one detached-worktree narrowing.
#
# Two assertions:
#   1. force-restart-success — `thrum tmux restart force-restart-test
#      --force` exits 0.
#   2. force-restart-session-alive — `thrum tmux status --json`
#      reports force-restart-test with state=alive within 10s of
#      the restart returning.

SID="69-multi-runtime-restart-force"
FORCE_WT_NAME="test-force-restart"
FORCE_WT="$WORKTREE_BASE/$COORD_BASENAME/$FORCE_WT_NAME"
FORCE_SESSION="force-restart-test"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_69() {

# Step 1: branch-backed worktree from coord identity. Markdown spec
# § 10C.10 used `--detach`, but the agent-registered tmux create
# (Step 2 below) hits a worktree-identity guard against detached
# worktrees in the post-nu16 codebase ("caller pane belongs to a
# different worktree"). Branch-backed worktrees are the supported
# shape for inline-registered tmux sessions; the restart-with-force
# contract is orthogonal to detached vs. branched.
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$FORCE_WT_NAME" \
      --branch "feature/${FORCE_WT_NAME}" 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$FORCE_WT" ]; then
  emit_fail "$SID" "force-restart-success" \
    "thrum worktree create $FORCE_WT_NAME succeeds and produces $FORCE_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: agent-registered tmux session driven from COORD_PANE.
# The daemon's tmux create requires an agent-registered caller for
# inline-registration shape ("--name <new agent>"); a tmux-exec
# ephemeral caller falls below that bar (caller_session does not
# match the run-level worktree's basename, daemon refuses the
# create with only the PaneTargetForIdentity warn as observable
# stderr). COORD_PANE is registered as @test_coordinator_main and
# satisfies the caller-identity guard. Mirrors the scenario 13
# pattern of driving a state-mutating worktree-create from coord
# pane via send_bash_and_wait.
local force_agent_name="force_agent"
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux create $FORCE_SESSION --cwd $FORCE_WT --name $force_agent_name --role tester --module testing --intent 'Force restart test'" \
    "Session created" 60; then
  emit_fail "$SID" "force-restart-success" \
    "thrum tmux create $FORCE_SESSION (agent-registered, driven from COORD pane) succeeds with 'Session created' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_69_cleanup
  return 0
fi

# Step 3: launch shell. The runtime choice is incidental to the
# restart contract — shell is the cheap, always-available choice.
local launch_session_out launch_session_rc
launch_session_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux launch "$FORCE_SESSION" \
      --runtime shell 2>&1
)
launch_session_rc=$?
if [ "$launch_session_rc" -ne 0 ]; then
  emit_fail "$SID" "force-restart-success" \
    "thrum tmux launch $FORCE_SESSION --runtime shell succeeds" \
    "exit ${launch_session_rc}; output: $(printf '%s' "$launch_session_out" | tr '\n' ' ' | head -c 320)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_69_cleanup
  return 0
fi

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
  capture_thrum_json "$COORD_REPO" test_coordinator_main "$status_file" tmux status
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
    env THRUM_NAME=test_coordinator_main thrum tmux kill "$FORCE_SESSION" >/dev/null 2>&1 || true
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$FORCE_WT_NAME" \
    >/dev/null 2>&1 || true
}

_run_scenario_69
