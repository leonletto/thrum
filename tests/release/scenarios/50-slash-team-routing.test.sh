#!/usr/bin/env bash
# Scenario: slash-team-routing (migrates full_test_plan.md § 7.5)
#
# Verifies that /thrum:team is recognized and routed by Claude
# Code's slash parser into the agent's JSONL as a user message
# containing `<command-name>/thrum:team</command-name>`.
#
# Same routing-only rationale as scenarios 20, 54, 55, and 57:
# skill-body execution (which command(s) the slash body invokes —
# /thrum:team's body is a Bash fence calling `thrum team`) couples
# to model eagerness, while the slash-routing tag is the
# deterministic registration contract. Scenario 61 (nl-team-tool-use)
# already covers the NL → `thrum team` tool_use chain; this scenario
# adds the missing slash-routing anchor so a regression that
# deletes/renames the /thrum:team slash command (skill file removal,
# plugin install regression, slash parsing breakage) would surface
# here even if the underlying CLI continues to work via NL.
#
# Driven against COORD pane (matches markdown § 7.5 subject — the
# /thrum:team variant the spec table assigns to the coordinator).

SID="50-slash-team-routing"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

_run_scenario_50() {

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

send_slash_command "$PANE" "/thrum:team"

local filter='.type == "user"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | tostring | contains("<command-name>/thrum:team</command-name>"))'

if wait_for_jsonl_match "$REPO" "$filter" 60 >/dev/null; then
  emit_pass "$SID" "slash-team-registered"
else
  emit_fail "$SID" "slash-team-registered" \
    'user message containing "<command-name>/thrum:team</command-name>" within 60s after slash send' \
    "(no matching JSONL entry — slash command did not register)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_50

_run_scenario_50
