#!/usr/bin/env bash
# Scenario: restart-coord-fixture-conversation (migrates full_test_plan.md § 10.2)
#
# Generates conversation content in the kafm6-impl-restart fixture's JSONL
# so the subsequent snapshot extraction (scenario 73's `thrum tmux restart`)
# has body to capture. The markdown spec sends a thrum message from coord
# to test_implementer and waits for claude's autonomous reply; we send
# the same shape of message but assert delivery deterministically by
# driving the kafm6 pane to read its own inbox via `!`-bash. That gives
# the same "conversation content in JSONL" guarantee without depending
# on claude's autonomous reply timing (which is the failure mode that
# scenarios 06-08 already work around with explicit `!` drives).
#
# One assertion: the kafm6 agent's inbox lookup surfaces the marker
# coord sent — proves end-to-end delivery AND adds an assistant tool_use
# + bash-stdout pair to the JSONL for scenario 73 to extract.
#
# Depends on scenario 70 (fixture exists; tmux session + agent +
# worktree set up).

SID="71-restart-coord-fixture-conversation"

# Marker is RUNID-anchored so a re-run against a (rare) lingering fixture
# can't false-match a stale message.
KAFM6_S1_CONV_MARKER="kafm6-conversation-${RUNID}"

if [ -z "${KAFM6_S1_AGENT:-}" ] || [ -z "${KAFM6_S1_SESSION:-}" ] || [ -z "${KAFM6_S1_WT:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 70 fixture identifiers (KAFM6_S1_AGENT/SESSION/WT) exported" \
    "(missing — scenario 70 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

_run_scenario_71() {

# Step 1: coord sends marker-bearing message to the kafm6 agent. Driven
# from COORD_PANE (registered identity, satisfies recipient routing),
# matches the markdown spec's thrum-send-from-coord pattern.
local send_cmd
send_cmd="thrum send 'kafm6 conversation seed (${KAFM6_S1_CONV_MARKER})' --to @${KAFM6_S1_AGENT} --json"
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "$send_cmd" \
    '"message_id": "msg_' 60; then
  emit_fail "$SID" "kafm6-conversation-delivered" \
    "thrum send to @${KAFM6_S1_AGENT} returns success-path message_id envelope" \
    "(timeout, no matching bash-stdout entry on COORD)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: drive the kafm6 pane to read its inbox. This both confirms
# delivery and adds an assistant turn (tool_use Bash → bash-stdout)
# to the kafm6 JSONL so scenario 73's snapshot extraction has content.
# The pane may be settling post-launch — wait for idle first.
wait_for_pane_idle "$KAFM6_S1_SESSION" 30

if send_bash_and_wait "$KAFM6_S1_SESSION" "$KAFM6_S1_WT" \
    "thrum inbox --json" \
    "$KAFM6_S1_CONV_MARKER" 60; then
  emit_pass "$SID" "kafm6-conversation-delivered"
else
  emit_fail "$SID" "kafm6-conversation-delivered" \
    "thrum inbox --json on ${KAFM6_S1_AGENT} pane contains marker '${KAFM6_S1_CONV_MARKER}'" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_71

_run_scenario_71

export KAFM6_S1_CONV_MARKER
