#!/usr/bin/env bash
# Scenario: restart-coord-fixture-launch (migrates full_test_plan.md § 10.1)
#
# Sets up the shared "Scenario 1: Coordinator-Initiated Restart" fixture
# used by scenarios 70-76, AND pins § 10.1's two contracts: after `thrum
# tmux create + launch` registers a fresh agent identity in a fresh
# worktree and boots claude, the daemon reports the session alive AND
# the agent's identity file has tmux_session populated + worktree as an
# absolute path (x6e8.6 / x6e8.2 regression guards).
#
# Why a sub-fixture (and not the run-level impl pane): § 10 runs TWO
# back-to-back restart cycles (Scenario 1 here, Scenario 2 in 77-80).
# Driving those on the run-level coord/impl would mutate the JSONL +
# identity state of agents that subsequent kafm scenarios depend on.
# Sub-fixtures isolate the restart-cycle mutations to a worktree the
# rest of the suite never reads.
#
# Why claude (not shell) runtime: § 10's value is the full save-via-JSONL
# → restart → load-via-prime chain. Shell runtime has no JSONL extraction
# or prime-rendered Previous Session Context — it would short-circuit
# every contract this batch is meant to verify.
#
# Why scenarios 70-76 share state: each create+launch+claude-boot round
# trip costs ~20-30s + real claude API. Doing it per-scenario (×6) would
# add 100s+ for no functional gain — we explicitly drive a single
# fixture through the whole flow (create → converse → restart → verify
# → cleanup) and assert at each stage. Scenario 76 tears down the
# fixture; helpers/teardown.sh's defensive cleanup catches partial
# failures.
#
# Two assertions:
#   1. coord-restart-fixture-alive — `thrum tmux status --json` reports
#      kafm6-impl-restart-test with state=alive within 30s of launch.
#   2. coord-restart-fixture-identity — the agent's identity file has
#      a non-empty tmux_session AND an absolute worktree path (guards
#      against thrum-x6e8.6 stale-PID bug + thrum-x6e8.2 relative-path
#      bug, both fixed pre-v0.9.0 but still load-bearing for restart).
#
# Fixture: $WORKTREE_BASE/repo/kafm6-impl-restart-wt + tmux session
# kafm6-impl-restart-test + agent kafm6_test_impl, all torn down by
# scenario 76.

SID="70-restart-coord-fixture-launch"

# Fixture identifiers (exported for scenarios 71-76).
KAFM6_S1_AGENT="kafm6_test_impl"
KAFM6_S1_SESSION="kafm6-impl-restart-test"
KAFM6_S1_WT_NAME="kafm6-impl-restart-wt"
KAFM6_S1_WT="$WORKTREE_BASE/repo/$KAFM6_S1_WT_NAME"

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_70() {

# Step 1: create the sub-fixture worktree from coord identity. Same
# pattern as scenarios 45/69. Branch-backed (not detached) so inline
# agent-registration on the tmux create below isn't refused by the
# daemon's worktree-identity guard.
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$KAFM6_S1_WT_NAME" \
      --branch "feature/${KAFM6_S1_WT_NAME}" 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$KAFM6_S1_WT" ]; then
  emit_fail "$SID" "coord-restart-fixture-alive" \
    "thrum worktree create $KAFM6_S1_WT_NAME succeeds and produces $KAFM6_S1_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: agent-registered tmux session, driven from COORD_PANE
# (mirrors scenario 69's rationale — daemon's identity guard refuses
# inline-registration tmux create from tmux-exec ephemeral callers).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux create $KAFM6_S1_SESSION --cwd $KAFM6_S1_WT --name $KAFM6_S1_AGENT --role implementer --module testing --intent 'kafm6 restart fixture'" \
    "Session created" 60; then
  emit_fail "$SID" "coord-restart-fixture-alive" \
    "thrum tmux create $KAFM6_S1_SESSION (driven from COORD pane) succeeds with 'Session created' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 3: launch claude. Daemon's HandleLaunch goroutine clears any
# stale subshell PID and writes tmux_session; claude's /thrum:prime
# auto-run reclaims agent_pid.
local launch_out launch_rc
launch_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux launch "$KAFM6_S1_SESSION" 2>&1
)
launch_rc=$?
if [ "$launch_rc" -ne 0 ]; then
  emit_fail "$SID" "coord-restart-fixture-alive" \
    "thrum tmux launch $KAFM6_S1_SESSION succeeds" \
    "exit ${launch_rc}; output: $(printf '%s' "$launch_out" | tr '\n' ' ' | head -c 320)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 4: wait for claude SessionStart in the new pane (proves the
# /thrum:prime run flushed JSONL and the agent is alive). 60s mirrors
# setup-repo.sh's impl-side wait.
if ! wait_for_session_start "$KAFM6_S1_WT" 60; then
  emit_fail "$SID" "coord-restart-fixture-alive" \
    "claude SessionStart attachment in $KAFM6_S1_WT within 60s of launch" \
    "(none observed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 1: daemon-side bookkeeping reports the session alive.
local status_file="/tmp/kafm6-70-status-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 30 ]; do
  capture_thrum_json "$COORD_REPO" test_coordinator_main "$status_file" tmux status
  if jq -e --arg n "$KAFM6_S1_SESSION" \
      '.sessions[]? | select(.name == $n and .state == "alive")' \
      "$status_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done
if jq -e --arg n "$KAFM6_S1_SESSION" \
    '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "coord-restart-fixture-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "coord-restart-fixture-alive" \
    "tmux status --json contains $KAFM6_S1_SESSION with state=alive" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_file"

# Assertion 2: identity file has tmux_session populated + worktree
# absolute. Driver-side jq read of the on-disk identity (no daemon
# round-trip needed). Per markdown § 10.1, these guard against
# x6e8.6 (stale subshell PID overriding tmux_session) and x6e8.2
# (relative worktree path) regressions.
local id_file="$KAFM6_S1_WT/.thrum/identities/$KAFM6_S1_AGENT.json"
elapsed=0
while [ ! -s "$id_file" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ ! -s "$id_file" ]; then
  emit_fail "$SID" "coord-restart-fixture-identity" \
    "non-empty identity file at ${id_file}" \
    "(file missing or empty after 10s)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi
local tmux_session worktree
tmux_session=$(jq -r '.tmux_session // ""' "$id_file" 2>/dev/null)
worktree=$(jq -r '.worktree // ""' "$id_file" 2>/dev/null)
if [ -n "$tmux_session" ] && [[ "$worktree" == /* ]]; then
  emit_pass "$SID" "coord-restart-fixture-identity"
else
  emit_fail "$SID" "coord-restart-fixture-identity" \
    "identity has non-empty tmux_session AND absolute worktree" \
    "tmux_session='${tmux_session}'; worktree='${worktree}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_70

_run_scenario_70

# Export shared fixture identifiers for scenarios 71-76.
export KAFM6_S1_AGENT KAFM6_S1_SESSION KAFM6_S1_WT_NAME KAFM6_S1_WT
