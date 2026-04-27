#!/usr/bin/env bash
# Scenario: agent-set-status-nonexistent (migrates full_test_plan.md § 10D.4)
#
# Verifies that `thrum agent set-status <state> --agent <ghost>` errors
# when the named target is not registered in any worktree. Pins the
# daemon-side lookup contract: the agent-name resolution path must
# return a "not found" error rather than silently no-op'ing or writing
# a phantom identity file.
#
# Two assertions:
#   1. ghost-rejected-non-zero — exit non-zero on unknown agent.
#   2. ghost-error-message — error output mentions "not found"
#      (markdown § 10D.4: "find agent ghost_agent: agent ghost_agent
#      not found in any worktree").
#
# Driven from COORD pane. No fixture mutation — daemon rejects before
# any write — so no cleanup needed.
#
# Ghost name: ghost_agent_${RUNID} so a stale registration from a
# crashed prior run cannot accidentally satisfy the lookup.

SID="84-agent-set-status-nonexistent"
GHOST="ghost_agent_${RUNID}"

wait_for_pane_idle "$COORD_PANE" 60

# Probe 1: exit code.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum agent set-status working --agent ${GHOST} 2>&1; echo \"exit: \$?\"" \
    "exit: 1" 60; then
  emit_pass "$SID" "ghost-rejected-non-zero"
else
  emit_fail "$SID" "ghost-rejected-non-zero" \
    'thrum agent set-status --agent <unknown> exits non-zero (literal "exit: 1" in stdout)' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Probe 2: error output mentions "not found". Substring chosen for
# stability against the daemon's lookup-error wording polish.
wait_for_pane_idle "$COORD_PANE" 30
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum agent set-status working --agent ${GHOST} 2>&1" \
    "not found" 60; then
  emit_pass "$SID" "ghost-error-message"
else
  emit_fail "$SID" "ghost-error-message" \
    'thrum agent set-status --agent <unknown> error output containing "not found"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
