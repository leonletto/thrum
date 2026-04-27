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

# Assertion 1: zero agents with empty/null agent_id. Compose a
# bash subshell that emits a RUNID-anchored marker on success.
ZERO_EMPTY_QUERY="N=\$(thrum agent list --json | jq -r '.agents.agents | map(select(.agent_id == null or .agent_id == \"\")) | length'); if [ \"\$N\" = \"0\" ]; then echo VERIFIED_5_5_NONEMPTY_${RUNID}; else echo FAILED_5_5_EMPTY_\$N; fi"

if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "$ZERO_EMPTY_QUERY" \
    "VERIFIED_5_5_NONEMPTY_${RUNID}" 60; then
  emit_pass "$SID" "all-agents-have-id"
else
  emit_fail "$SID" "all-agents-have-id" \
    "0 agents in agent list with empty/null agent_id" \
    "(timeout, or 1+ agents missing agent_id)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertions 2+3: both fixture identities present. Defensive
# against the empty-list false-positive on assertion 1, and split
# per-identity (not a single comma-joined substring) so a third
# agent appearing in the registry between the two — e.g. a
# `test_debug` that sorts between `test_coordinator_main` and
# `test_implementer` — doesn't break the substring match. Each
# identity gets its own pre/post jq-anchored emit on a dedicated
# bash subshell.
COORD_PRESENT_QUERY="N=\$(thrum agent list --json | jq -r '[.agents.agents[] | select(.agent_id == \"test_coordinator_main\")] | length'); if [ \"\$N\" -ge 1 ]; then echo VERIFIED_COORD_PRESENT_${RUNID}; else echo FAILED_COORD_MISSING; fi"
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "$COORD_PRESENT_QUERY" \
    "VERIFIED_COORD_PRESENT_${RUNID}" 60; then
  emit_pass "$SID" "coord-identity-present"
else
  emit_fail "$SID" "coord-identity-present" \
    "agent list contains entry with agent_id 'test_coordinator_main'" \
    "(timeout or coord identity missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

IMPL_PRESENT_QUERY="N=\$(thrum agent list --json | jq -r '[.agents.agents[] | select(.agent_id == \"test_implementer\")] | length'); if [ \"\$N\" -ge 1 ]; then echo VERIFIED_IMPL_PRESENT_${RUNID}; else echo FAILED_IMPL_MISSING; fi"
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "$IMPL_PRESENT_QUERY" \
    "VERIFIED_IMPL_PRESENT_${RUNID}" 60; then
  emit_pass "$SID" "impl-identity-present"
else
  emit_fail "$SID" "impl-identity-present" \
    "agent list contains entry with agent_id 'test_implementer'" \
    "(timeout or impl identity missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
