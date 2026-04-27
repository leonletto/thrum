#!/usr/bin/env bash
# Scenario: nl-who-has-tool-use (migrates full_test_plan.md § 7.10)
#
# Verifies that claude understands an NL ask about file ownership and
# shells out to `thrum who-has`. Spec § 7.10's wording: "run thrum
# who-has README.md and tell me who is editing it". The assertion
# anchor is the assistant tool_use Bash with .input.command containing
# "thrum who-has" — agnostic to claude's prose answer about WHO is
# editing.
#
# Pre-condition: impl must have an uncommitted change to README.md so
# the who-has command has a non-empty answer. We dirty README.md from
# IMPL_REPO via a plain shell append (no thrum involved) and revert at
# scenario end. The actual answer correctness ("test_implementer has
# uncommitted changes") is delegated to who-has's own daemon-side
# tests; this scenario's contract is the NL→tool_use chain.
#
# Driven against COORD pane.

SID="59-nl-who-has-tool-use"
PANE="$COORD_PANE"
REPO="$COORD_REPO"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_59() {

# Step 1: dirty README.md in IMPL_REPO (out-of-pane, no claude
# involvement). Append a marker so the dirty-file shape is unambiguous
# under git status; revert at end so subsequent scenarios that run
# git assertions on impl don't see leftover noise.
"$TE" exec --cwd "$IMPL_REPO" --clean -- \
  bash -c 'printf "\n// kafm-3-59 who-has marker\n" >> README.md' >/dev/null 2>&1 || true

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

send_command "$PANE" "run thrum who-has README.md and tell me who is editing it"

assert_tool_use_bash "$REPO" "$SID" "claude-invokes-thrum-who-has" \
  "$floor_ts" "thrum who-has" 90 \
  "scenarios/${SID}.test.sh:$LINENO" || true

# Cleanup: revert IMPL_REPO's README.md.
"$TE" exec --cwd "$IMPL_REPO" --clean -- \
  bash -c 'git checkout -- README.md' >/dev/null 2>&1 || true

}  # _run_scenario_59

_run_scenario_59
