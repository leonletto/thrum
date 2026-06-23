#!/usr/bin/env bash
# Scenario: mcp-list-agents-id (migrates full_test_plan.md § 5.5)
#
# Verifies that `thrum agent list --json` returns each registered
# agent with a non-empty, non-null `agent_id` field. Routing-parity
# contract for the MCP `list_agents` tool + CLI `thrum agent list`:
# both surfaces project the same daemon-side agent registry, so a
# regression in agent_id population breaks both simultaneously.
#
# JSON shape (per markdown § 5.5 note):
#   {"agents":{"agents":[...]},"contexts":...}
# Note the double "agents" nesting and the absence of any "data"
# wrapper. Asserting on the inner array via .agents.agents[] is
# the right path; .data.agents[] would silently miss.
#
# Two assertions:
#   1. zero-empty — count of agents whose agent_id is null OR ""
#      is 0 (the spec's "non-empty for ALL agents" invariant).
#   2. fixture-identities-present — both test_coordinator_main and
#      test_implementer appear in the list. Stronger than (1) alone:
#      catches regressions where the registry returns an empty list
#      (which trivially passes (1) on length 0).
#
# Driven from COORD pane (any registered pane works; coord is the
# stable driver surface). Read-only — no fixture mutation.

SID="26-mcp-list-agents-id"

# Settle COORD pane in case prior scenarios left rendering active.
wait_for_pane_idle "$COORD_PANE" 60

# Single read-only query covering all three assertions, with a bounded
# retry-resend (thrum-vjqn pattern). Three separate `!`-probes gave three
# independent race windows against the shared COORD pane; under full-gate load
# (claude 2.1.x panes do more autonomous work, so the COORD pane is busier) one
# probe's keystrokes intermittently queued and the wait timed out — a load-only
# flake that passed in isolation. Collapsing to ONE idempotent query shrinks
# the race to a single window, and resending on a missed keystroke recovers it.
# `thrum agent list` is a global daemon read, so one snapshot answers all three.
COMBINED_QUERY="OUT=\$(thrum agent list --json); E=\$(printf '%s' \"\$OUT\" | jq -r '.agents.agents | map(select(.agent_id == null or .agent_id == \"\")) | length'); C=\$(printf '%s' \"\$OUT\" | jq -r '[.agents.agents[] | select(.agent_id == \"test_coordinator_main\")] | length'); I=\$(printf '%s' \"\$OUT\" | jq -r '[.agents.agents[] | select(.agent_id == \"test_implementer\")] | length'); if [ \"\$E\" = \"0\" ] && [ \"\$C\" -ge 1 ] && [ \"\$I\" -ge 1 ]; then echo VERIFIED_5_5_ALL_${RUNID}; else echo \"FAILED_5_5 empty=\$E coord=\$C impl=\$I\"; fi"

_mcp_list_ok=0
_mcp_attempt=1
while [ "$_mcp_attempt" -le 3 ]; do
  if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
      "$COMBINED_QUERY" \
      "VERIFIED_5_5_ALL_${RUNID}" 30; then
    _mcp_list_ok=1
    break
  fi
  _mcp_attempt=$((_mcp_attempt + 1))
done

if [ "$_mcp_list_ok" = "1" ]; then
  emit_pass "$SID" "all-agents-have-id"
  emit_pass "$SID" "coord-identity-present"
  emit_pass "$SID" "impl-identity-present"
else
  # One emit_fail per assertion name so the bucket attribution is unchanged.
  emit_fail "$SID" "all-agents-have-id" \
    "0 agents in agent list with empty/null agent_id" \
    "(no VERIFIED marker after 3 attempts — pane busy or 1+ agents missing agent_id)" \
    "scenarios/${SID}.test.sh:$LINENO"
  emit_fail "$SID" "coord-identity-present" \
    "agent list contains entry with agent_id 'test_coordinator_main'" \
    "(no VERIFIED marker after 3 attempts)" \
    "scenarios/${SID}.test.sh:$LINENO"
  emit_fail "$SID" "impl-identity-present" \
    "agent list contains entry with agent_id 'test_implementer'" \
    "(no VERIFIED marker after 3 attempts)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
