#!/usr/bin/env bash
# Scenario: agent-set-status-local (migrates full_test_plan.md § 10D.1)
#
# Verifies `thrum agent set-status <state>` (no --agent flag) updates the
# CALLER's own identity file in-process: writes both `agent_status` and
# `agent_status_updated_at`, prints the canonical "✓ Status set to" line.
#
# Driven via the COORD pane (`!`-bash) so the in-process write resolves
# THRUM_NAME against the pane's coord-pane identity (test_coordinator_main).
# Sub-fixture would also work but adds setup cost for no extra contract
# coverage — set-status without --agent is a pure local file write that
# doesn't go through the daemon.
#
# Mutation safety: this scenario sets test_coordinator_main → working, then
# resets to idle on every exit path (success and failure). No subsequent
# scenario reads coord's agent_status, but resetting keeps the run-level
# fixture in its setup-time shape so scenarios 86/91 (which DO read agent
# status from coord-or-other panes) start clean.

SID="81-agent-set-status-local"
COORD_IDENTITY="$COORD_REPO/.thrum/identities/test_coordinator_main.json"

_run_scenario_81() {

wait_for_pane_idle "$COORD_PANE" 60

# Assertion 1: success line printed.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum agent set-status working" \
    "Status set to working" 60; then
  emit_pass "$SID" "set-status-success-line"
else
  emit_fail "$SID" "set-status-success-line" \
    'thrum agent set-status working stdout containing "Status set to working"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief poll: identity-file write may trail stdout flush slightly.
elapsed=0
while [ "$elapsed" -lt 5 ]; do
  if [ -f "$COORD_IDENTITY" ] && \
     [ "$(jq -r '.agent_status // ""' "$COORD_IDENTITY" 2>/dev/null)" = "working" ]; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion 2: identity file's agent_status field == "working".
if [ -f "$COORD_IDENTITY" ]; then
  status=$(jq -r '.agent_status // ""' "$COORD_IDENTITY" 2>/dev/null)
  if [ "$status" = "working" ]; then
    emit_pass "$SID" "identity-status-working"
  else
    emit_fail "$SID" "identity-status-working" \
      "agent_status == 'working' in $COORD_IDENTITY" \
      "got: '${status}'" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_fail "$SID" "identity-status-working" \
    "identity file at $COORD_IDENTITY" \
    "(file missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: agent_status_updated_at is non-empty (the timestamp gets
# stamped on every set-status write — empty would indicate the in-process
# writer skipped the timestamp branch).
if [ -f "$COORD_IDENTITY" ]; then
  ts=$(jq -r '.agent_status_updated_at // ""' "$COORD_IDENTITY" 2>/dev/null)
  if [ -n "$ts" ]; then
    emit_pass "$SID" "identity-status-timestamp-set"
  else
    emit_fail "$SID" "identity-status-timestamp-set" \
      "agent_status_updated_at non-empty in $COORD_IDENTITY" \
      "(field empty or missing)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
fi

}  # _run_scenario_81

_run_scenario_81

# Reset coord identity to idle. `|| true` so a failed reset doesn't
# pollute EXIT — but we still log the reset attempt so post-run
# inspection can see whether cleanup succeeded.
wait_for_pane_idle "$COORD_PANE" 30
send_command "$COORD_PANE" "! thrum agent set-status idle 2>/dev/null || true"
