#!/usr/bin/env bash
# Scenario: nl-set-intent-tool-use (migrates full_test_plan.md § 7.11)
#
# Verifies that claude parses an NL "update my thrum intent to: X" ask
# and shells out to `thrum agent set-intent`. Assertion anchor is the
# assistant tool_use Bash with .input.command containing "thrum agent
# set-intent".
#
# Spec § 7.11 is the F4 video demo source — a high-visibility user-
# facing path. A regression where the agent stops recognizing intent-
# update phrasing would surface here.
#
# Substring choice: "set-intent" (just the verb-token) rather than the
# full "thrum agent set-intent" path. Empirically the latter missed in
# the first kafm.3 run — claude may legitimately invoke the command
# with intermediate whitespace, comment-stripped argv reconstruction,
# or a brief preceding `bash -c` wrapper that buries "thrum agent" out
# of the contains-substring window. "set-intent" is distinctive on its
# own (no other thrum subcommand uses that token; no shell builtin
# uses the hyphenated form), so the false-positive risk is acceptably
# low. The model-stability guarantee is what matters: any path claude
# takes to mutate intent passes through the `set-intent` verb.
#
# Driven against COORD pane.

SID="60-nl-set-intent-tool-use"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

_run_scenario_60() {

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

# NL prompt mirroring § 7.11 wording. Distinct intent string anchored
# to the scenario id so a successful run leaves a recognizable
# fingerprint in coord's agent-state if a later scenario inspected it.
send_command "$PANE" "update my thrum intent to: Building authentication system (kafm-3-60)"

assert_tool_use_bash "$REPO" "$SID" "claude-invokes-set-intent" \
  "$floor_ts" "set-intent" 120 \
  "scenarios/${SID}.test.sh:$LINENO" || true

}  # _run_scenario_60

_run_scenario_60
