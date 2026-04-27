#!/usr/bin/env bash
# Scenario: nl-team-tool-use (migrates full_test_plan.md § 7.12)
#
# Verifies that claude parses an NL "show me the thrum team status"
# ask and shells out to `thrum team`. Assertion anchor is the assistant
# tool_use Bash with .input.command containing "thrum team".
#
# Spec § 7.12 is the F2 video demo source. This scenario also closes
# the coverage loop for kafm.3.5 (the spec § 7.5 /thrum:team subsection
# that's flagged P3 "verify covered, then delete"). § 7.5 was the slash-
# routing variant of the same `thrum team` invocation; § 7.12 covers
# the NL variant. Together with setup-repo.sh's whoami probes (which
# implicitly verify the team registry is queryable) and scenario 26
# (mcp-list-agents-id, the JSON-shape contract for `thrum agent list`),
# the team-listing surface is fully covered without a dedicated /thrum:
# team scenario.
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
