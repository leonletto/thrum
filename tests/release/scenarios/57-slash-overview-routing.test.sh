#!/usr/bin/env bash
# Scenario: slash-overview-routing (migrates full_test_plan.md § 7.8)
#
# Verifies that /thrum:overview is recognized and routed by Claude
# Code's slash parser into the agent's JSONL as a user message
# containing `<command-name>/thrum:overview</command-name>`.
#
# Same routing-only rationale as scenarios 20 and 54: skill-body
# execution (which command(s) the slash body invokes — overview
# composes whoami + team + inbox + status) is non-deterministic
# under model eagerness; routing-tag presence is the deterministic
# slash-registration contract.
#
# Driven against COORD pane (matches markdown § 7.8 subject).

SID="57-slash-overview-routing"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

_run_scenario_57() {

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

send_slash_command "$PANE" "/thrum:overview"

local filter='.type == "user"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | tostring | contains("<command-name>/thrum:overview</command-name>"))'

if wait_for_jsonl_match "$REPO" "$filter" 60 >/dev/null; then
  emit_pass "$SID" "slash-overview-registered"
else
  emit_fail "$SID" "slash-overview-registered" \
    'user message containing "<command-name>/thrum:overview</command-name>" within 60s after slash send' \
    "(no matching JSONL entry — slash command did not register)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_57

_run_scenario_57
