#!/usr/bin/env bash
# Scenario: nl-send-tool-use (migrates full_test_plan.md § 7.7)
#
# Spec § 7.7's heading is "Test /thrum:send" but the body drives a
# NATURAL-LANGUAGE prompt ("send a thrum message to @test_implementer
# saying 'Hello from slash command test'"), not the literal slash.
# Same documentation drift pattern as § 9.1 (where scenario 17 added
# its own deviation note). The user-facing contract being tested is:
# claude understands the NL ask and shells out to `thrum send`. Asserting
# on the assistant's tool_use Bash is the deterministic signature.
#
# Routing-tag assertion (the slash-only shape used by 54/55/57) doesn't
# apply here because there is no slash to register — the input is plain
# chat. assert_tool_use_bash polls JSONL for an assistant message
# containing a tool_use whose .name == "Bash" and .input.command
# contains "thrum send", scoped to entries newer than floor_ts.
#
# Why "thrum send" as the substring (not the more specific
# "@test_implementer"): claude may legitimately quote the recipient
# differently across model versions ("@test_implementer", "agent
# test_implementer", with/without trailing punctuation) — pinning the
# substring to the command verb keeps the assertion stable across
# model wording variation. Coverage of caller-id stamping +
# cross-session delivery already lives in scenarios 06/07/22-25.
#
# Driven against COORD pane.

SID="56-nl-send-tool-use"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

_run_scenario_56() {

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

# NL prompt mirroring § 7.7's wording. Sent as plain chat (no `!`
# prefix, no `/`-slash) so claude has to interpret intent and shell
# out via Bash to fulfill the request.
send_command "$PANE" "send a thrum message to @test_implementer saying 'Hello from kafm-3-56 NL test'"

assert_tool_use_bash "$REPO" "$SID" "claude-invokes-thrum-send" \
  "$floor_ts" "thrum send" 90 \
  "scenarios/${SID}.test.sh:$LINENO" || true

}  # _run_scenario_56

_run_scenario_56
