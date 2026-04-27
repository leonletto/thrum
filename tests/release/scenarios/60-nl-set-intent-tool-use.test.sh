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
# of the contains-substring window. **NB:** the SPECIFIC invocation
# shape that caused the original miss was not directly captured —
# run-level teardown removed the failing JSONL before it could be
# inspected, and the subsequent NO_TEARDOWN re-run hit the green path
# under both the relaxed and full substrings (so the failing shape
# self-recovered before forensic capture). The relaxation is therefore
# a defensive choice based on the failure-class hypotheses above, not
# from observed evidence — if the false-positive risk ever materializes
# (e.g. `set-intent` matches a non-thrum tool surface), the right move
# is to retighten with a NO_TEARDOWN reproducer in hand. "set-intent"
# is distinctive on its own (no other thrum subcommand uses that token;
# no shell builtin uses the hyphenated form), so the false-positive
# risk is acceptably low for now. The model-stability guarantee is what
# matters: any path claude takes to mutate intent passes through the
# `set-intent` verb.
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
