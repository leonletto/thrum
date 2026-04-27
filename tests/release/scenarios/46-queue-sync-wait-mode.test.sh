#!/usr/bin/env bash
# Scenario: queue-sync-wait-mode (migrates full_test_plan.md § 10E.2)
#
# Verifies `thrum tmux queue --wait` blocks until the command reaches
# a terminal state, returns the captured output, AND auto-suppresses
# the @system completion message (notify_on_complete=false in --wait
# mode — caller already has the result).
#
# Two assertions:
#   1. wait-output-completed — synchronous output contains the
#      printed payload and "State: completed".
#   2. wait-suppresses-system-message — no NEW @system message lands
#      in the requester's inbox (compared against pre-test count).
#
# Depends on scenario 45's queue-test session.
# Read-only at the queue-state level (the command runs to completion
# and clears).

SID="46-queue-sync-wait-mode"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
PAYLOAD="kafm10-46-${RUNID}"

_run_scenario_46() {

# Mark inbox read so the count we capture next is a clean baseline.
# Driver-side thrum from the runner's bash → tmux-exec.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum message read --all \
  >/dev/null 2>&1 || true

# Baseline @system message count BEFORE the --wait run. The inbox
# JSON is captured via in-pane redirect to a file because the
# default tmux capture-pane path used by tmux-exec wraps long
# stdout at 80 cols, corrupting JSON mid-string (memory:
# tmux-capture-pane-json-wrap).
local before_file="/tmp/kafm10-46-before-${RUNID}.json"
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "env THRUM_NAME=test_coordinator_main thrum inbox --all --json > '$before_file' 2>/dev/null"
local before_count
before_count=$(jq '[.messages[]? | select(.agent_id == "system")] | length' \
  "$before_file" 2>/dev/null)
[ -z "$before_count" ] && before_count=0

# Submit the command in --wait mode. --silence 2.0 keeps the test
# fast (default 5.0 silence threshold); 30s overall timeout is
# generous (the script itself sleeps ~2s).
local wait_out wait_rc
wait_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux queue "$QUEUE_SESSION" \
      "echo ${PAYLOAD} && sleep 2 && echo done-${PAYLOAD}" \
      --wait --timeout 30 --silence 2.0 2>&1
)
wait_rc=$?

if [ "$wait_rc" -eq 0 ] && \
   printf '%s' "$wait_out" | grep -q "$PAYLOAD" && \
   printf '%s' "$wait_out" | grep -q "done-${PAYLOAD}" && \
   printf '%s' "$wait_out" | grep -qi "completed"; then
  emit_pass "$SID" "wait-output-completed"
else
  local got
  got=$(printf '%s' "$wait_out" | tr '\n' ' ' | head -c 320)
  emit_fail "$SID" "wait-output-completed" \
    "exit 0 + output containing '${PAYLOAD}', 'done-${PAYLOAD}', and 'completed'" \
    "exit ${wait_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Verify no NEW @system message was created. Inbox count should
# match the pre-test baseline (notify_on_complete=false suppresses
# the completion notification when --wait is active — the CLI
# already returned the result synchronously).
local after_file="/tmp/kafm10-46-after-${RUNID}.json"
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "env THRUM_NAME=test_coordinator_main thrum inbox --all --json > '$after_file' 2>/dev/null"
local after_count
after_count=$(jq '[.messages[]? | select(.agent_id == "system")] | length' \
  "$after_file" 2>/dev/null)
[ -z "$after_count" ] && after_count=0

if [ "$before_count" = "$after_count" ]; then
  emit_pass "$SID" "wait-suppresses-system-message"
else
  emit_fail "$SID" "wait-suppresses-system-message" \
    "@system message count unchanged across --wait submission" \
    "before=${before_count} after=${after_count}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$before_file" "$after_file"

}  # _run_scenario_46

_run_scenario_46
