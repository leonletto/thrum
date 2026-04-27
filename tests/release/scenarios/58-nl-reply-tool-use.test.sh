#!/usr/bin/env bash
# Scenario: nl-reply-tool-use (migrates full_test_plan.md § 7.9)
#
# Spec § 7.9's heading is "Test /thrum:reply" but the body drives a
# natural-language prompt ("reply to message $MSG_ID with 'Got it,
# thanks'") — same heading-vs-body drift pattern as § 7.7. Asserts the
# deterministic NL→tool_use Bash signature: claude shells out to
# `thrum reply` after parsing the ask.
#
# Pre-condition: a real msg_id must exist in coord's inbox for the
# NL prompt to address. We pre-deliver one out-of-pane via tmux-exec
# (impl→coord, marker-anchored), then read the message_id back via
# `thrum inbox --json` captured to a host file (capture_thrum_json
# helper, no 80-col wrap mangling).
#
# Why "thrum reply" as the assertion substring: same model-wording-
# stability rationale as scenario 56. The reply daemon RPC contract
# is independently covered by scenario 24 (mcp-reply-routes-back); the
# unique value of THIS scenario is the NL→tool_use chain.
#
# Driven against COORD pane.

SID="58-nl-reply-tool-use"
PANE="$COORD_PANE"
REPO="$COORD_REPO"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
PRESEED_MARKER="kafm3-58-preseed-${RUNID}"

_run_scenario_58() {

# Pre-deliver a marker message impl→coord. THRUM_NAME pinned to
# preserve identity across the tmux-exec ephemeral pane.
"$TE" exec --cwd "$IMPL_REPO" --clean -- \
  env THRUM_NAME=test_implementer thrum send \
    "Reply test preseed (${PRESEED_MARKER})" \
    --to @test_coordinator_main >/dev/null 2>&1 || true

# Pull coord's inbox as JSON and extract the preseed message's id.
# capture_thrum_json appends --json; helper handles the file-redirect
# pattern (memory: tmux-capture-pane-json-wrap). --all so it doesn't
# matter whether claude already auto-marked-read; the marker filter
# pinpoints our message regardless.
local inbox_file="/tmp/kafm3-58-inbox-${RUNID}.json"
capture_thrum_json "$COORD_REPO" "test_coordinator_main" "$inbox_file" \
  inbox --all

local msg_id
msg_id="$(jq -r --arg m "$PRESEED_MARKER" \
  '[.messages[]? | select((.body.content // "") | contains($m)) | .message_id][0] // ""' \
  "$inbox_file" 2>/dev/null)"
rm -f "$inbox_file"

if [ -z "$msg_id" ]; then
  emit_fail "$SID" "preseed-msg-id-resolved" \
    "preseed message_id retrievable from coord's inbox JSON" \
    "(no inbox entry matching marker '${PRESEED_MARKER}')" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi
emit_pass "$SID" "preseed-msg-id-resolved"

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

# NL ask using the resolved msg_id. Mirrors § 7.9's wording shape.
send_command "$PANE" "reply to message ${msg_id} with 'Got it, thanks (kafm-3-58)'"

assert_tool_use_bash "$REPO" "$SID" "claude-invokes-thrum-reply" \
  "$floor_ts" "thrum reply" 90 \
  "scenarios/${SID}.test.sh:$LINENO" || true

}  # _run_scenario_58

_run_scenario_58
