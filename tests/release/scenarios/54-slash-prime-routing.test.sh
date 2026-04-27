#!/usr/bin/env bash
# Scenario: slash-prime-routing (migrates full_test_plan.md § 7.4)
#
# Verifies that typing /thrum:prime into the COORD pane is recognized
# and routed by Claude Code's slash command parser — i.e. the keystrokes
# get converted into a `<command-name>/thrum:prime</command-name>` user
# message in the agent's JSONL. That tag is the deterministic signature
# of slash-command registration; absence means the slash didn't register.
#
# Why ROUTING-only here (matching scenario 20's rationale): once the
# COORD pane has a primed SessionStart briefing in context, the model
# may reason "no need to re-run prime" and reply in chat instead of
# executing the skill body's `thrum prime` Bash call. That's a valid
# model-side optimization, not a regression. Asserting on the tool_use
# Bash call would couple this scenario to a particular model's
# eagerness to obey skill bodies. The slash-routing tag is emitted
# regardless.
#
# Skill-body execution coverage: scenario 21's
# context-survives-restart-slash sub-assertion drives /thrum:load-context
# against a freshly-restarted pane and asserts the assistant tool_use
# Bash for `thrum prime`. The skill body for /thrum:prime mirrors the
# /thrum:load-context body (both run `thrum prime`), so 21's coverage
# carries the post-routing chain for this scenario too.
#
# Why COORD pane (not IMPL): § 7.4 drives coord, and scenario 20 (the
# /thrum:load-context routing precedent) already covers IMPL-pane slash
# routing. Splitting panes between scenarios pins the routing-pane
# parity contract at the suite level.
#
# floor_ts: setup-repo.sh's `thrum tmux start --name coord` already
# fires /thrum:prime as part of the launch path, so we scope our match
# to only routing entries created at-or-after this scenario's send.
# Same pattern as scenario 20.
#
# Read-only at the fixture level; sending /thrum:prime doesn't mutate
# state.

SID="54-slash-prime-routing"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

_run_scenario_54() {

# Settle the COORD pane in case prior scenarios left rendering active.
# COORD's prime output is significantly larger than IMPL's; 60s gives
# enough headroom (memory: tmux-capture-pane-json-wrap ... and the
# COORD-pane idle bound documented in setup-repo.sh).
wait_for_pane_idle "$PANE" 60

# RFC3339 floor (no fractional seconds, no Z) sorts lexicographically
# before any subsequent JSONL timestamp at the same clock-second.
local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

send_slash_command "$PANE" "/thrum:prime"

local filter='.type == "user"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | tostring | contains("<command-name>/thrum:prime</command-name>"))'

if wait_for_jsonl_match "$REPO" "$filter" 60 >/dev/null; then
  emit_pass "$SID" "slash-prime-registered"
else
  emit_fail "$SID" "slash-prime-registered" \
    'user message containing "<command-name>/thrum:prime</command-name>" within 60s after slash send' \
    "(no matching JSONL entry — slash command did not register)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_54

_run_scenario_54
