#!/usr/bin/env bash
# Scenario: agent-set-status-remote (migrates full_test_plan.md § 10D.2)
#
# Verifies the daemon-RPC path of `thrum agent set-status <state>
# --agent <other>`: when the target is a different live agent, the
# daemon's UpdateAgentStatus handler writes the target's identity
# file. Routing-parity contract distinct from scenario 81's local
# in-process write.
#
# Uses the run-level test_implementer (live agent_pid from `thrum tmux
# launch impl`) as the target, which avoids spinning up a sub-fixture
# just to satisfy the daemon's "live pid" guard. Driven from COORD
# pane via `!`-bash so THRUM_NAME resolves to test_coordinator_main
# and the daemon sees a registered caller.
#
# Mutation safety: sets test_implementer → working, then resets to
# idle on every exit path. Subsequent scenarios (notably 28's drain +
# 91's working-but-idle nudge dispatch) read agent_status; the reset
# keeps the fixture's idle invariant intact.

SID="82-agent-set-status-remote"
IMPL_IDENTITY="$IMPL_REPO/.thrum/identities/test_implementer.json"

_run_scenario_82() {

wait_for_pane_idle "$COORD_PANE" 60

# Assertion 1: success line. The CLI's remote success format is
# "✓ Status for <agent> set to <state>" (vs. local "Status set to").
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum agent set-status working --agent test_implementer" \
    "Status for test_implementer set to working" 60; then
  emit_pass "$SID" "set-status-remote-success-line"
else
  emit_fail "$SID" "set-status-remote-success-line" \
    'thrum agent set-status working --agent test_implementer stdout containing "Status for test_implementer set to working"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief poll: daemon RPC writes the impl identity asynchronously
# relative to the success-line print on the caller side.
elapsed=0
while [ "$elapsed" -lt 5 ]; do
  if [ -f "$IMPL_IDENTITY" ] && \
     [ "$(jq -r '.agent_status // ""' "$IMPL_IDENTITY" 2>/dev/null)" = "working" ]; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion 2: target identity file's agent_status == "working".
if [ -f "$IMPL_IDENTITY" ]; then
  status=$(jq -r '.agent_status // ""' "$IMPL_IDENTITY" 2>/dev/null)
  if [ "$status" = "working" ]; then
    emit_pass "$SID" "impl-identity-status-working"
  else
    emit_fail "$SID" "impl-identity-status-working" \
      "agent_status == 'working' in $IMPL_IDENTITY" \
      "got: '${status}'" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_fail "$SID" "impl-identity-status-working" \
    "identity file at $IMPL_IDENTITY" \
    "(file missing — daemon RPC did not write target identity)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_82

_run_scenario_82

# Reset impl identity to idle. Run unconditionally so a failed
# assertion doesn't leak working-state into subsequent scenarios.
wait_for_pane_idle "$COORD_PANE" 30
send_command "$COORD_PANE" "! thrum agent set-status idle --agent test_implementer 2>/dev/null || true"
