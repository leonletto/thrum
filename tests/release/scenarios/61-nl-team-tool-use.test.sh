#!/usr/bin/env bash
# Scenario: nl-team-tool-use (migrates full_test_plan.md § 7.12)
#
# Verifies that claude parses an NL "show me the thrum team status"
# ask and shells out to `thrum team`. Assertion anchor is the assistant
# tool_use Bash with .input.command containing "thrum team".
#
# Spec § 7.12 is the F2 video demo source. § 7.5 (/thrum:team slash
# variant) is covered by scenario 50 (slash-team-routing) which asserts
# the slash-routing tag; this scenario covers § 7.12's NL → tool_use
# chain. Together with setup-repo.sh's whoami probes (registry-reachable)
# and scenario 26 (mcp-list-agents-id, JSON-shape contract for
# `thrum agent list`), the team-listing surface is fully covered.
#
# Driven against COORD pane.

SID="61-nl-team-tool-use"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

_run_scenario_61() {

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

send_command "$PANE" "show me the thrum team status"

assert_tool_use_bash "$REPO" "$SID" "claude-invokes-thrum-team" \
  "$floor_ts" "thrum team" 90 \
  "scenarios/${SID}.test.sh:$LINENO" || true

}  # _run_scenario_61

_run_scenario_61
