#!/usr/bin/env bash
# Scenario: queue-async-system-delivery (migrates full_test_plan.md § 10E.3)
#
# Verifies that a queue submission WITHOUT --wait returns a cmd_*
# ID synchronously and that the command's captured output is later
# delivered to the requester as an @system Thrum message
# (notify_on_complete=true is the default when --wait is absent).
#
# Three assertions:
#   1. async-cmd-id-returned — submit returns a cmd_xxx id on stdout.
#   2. async-system-message-arrives — an @system inbox message
#      mentioning the cmd id appears within ~30s.
#   3. async-message-body-shape — the @system body contains both the
#      command's captured output AND the word "completed" (matches
#      the daemon's "Command <id> completed.\nSession: …\nElapsed: …
#      \nOutput:\n---\n…\n---" template at queue_rpc.go:200).
#
# Depends on scenario 45's queue-test session.

SID="47-queue-async-system-delivery"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
PAYLOAD="kafm10-47-${RUNID}"

_run_scenario_47() {

# Clear inbox so the @system match below is unambiguously the new one.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum message read --all \
  >/dev/null 2>&1 || true

# Submit async (no --wait). Expected stdout: "Queued cmd_xxx (position N)".
local submit_out submit_rc
submit_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux queue "$QUEUE_SESSION" \
      "sleep 2 && echo ${PAYLOAD}" --timeout 60 --silence 2.0 2>&1
)
submit_rc=$?

local cmd_id
cmd_id=$(printf '%s' "$submit_out" | grep -oE 'cmd_[a-zA-Z0-9]+' | head -1)
if [ "$submit_rc" -eq 0 ] && [ -n "$cmd_id" ]; then
  emit_pass "$SID" "async-cmd-id-returned"
else
  emit_fail "$SID" "async-cmd-id-returned" \
    "exit 0 and a cmd_xxx id on stdout" \
    "exit ${submit_rc}; output: $(printf '%s' "$submit_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Poll the inbox for the @system completion message. Total wait:
# 2s sleep + ~2s silence + delivery latency. 60s timeout is
# generous; failures here usually mean the daemon's completion
# detector wedged or HandleQueue's completion goroutine never ran.
#
# Inbox JSON is captured via in-pane redirect to a file: tmux-exec
# captures stdout via `tmux capture-pane -p` which wraps at 80 cols,
# inserting literal newlines into JSON strings and breaking jq
# parse mid-content (memory: tmux-capture-pane-json-wrap).
local inbox_file="/tmp/kafm10-47-inbox-${RUNID}.json"
local elapsed=0 body=""
while [ "$elapsed" -lt 60 ]; do
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "env THRUM_NAME=test_coordinator_main thrum inbox --all --json > '$inbox_file' 2>/dev/null"
  # `[…][0] // ""` returns the first matching body in full (newlines
  # intact). Without the array wrapper a multi-line `.body.content`
  # gets piped through `head -1` upstream, which strips the
  # "Output:" section the body-shape assertion depends on.
  body=$(jq -r --arg cid "$cmd_id" \
      '[.messages[]? | select(.agent_id == "system") | .body.content // ""
       | select(contains($cid))][0] // ""' \
      "$inbox_file" 2>/dev/null)
  if [ -n "$body" ]; then
    break
  fi
  sleep 2
  elapsed=$((elapsed + 2))
done
rm -f "$inbox_file"

if [ -n "$body" ]; then
  emit_pass "$SID" "async-system-message-arrives"
else
  emit_fail "$SID" "async-system-message-arrives" \
    "@system inbox message mentioning ${cmd_id} within 60s" \
    "(no matching @system message)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Body-shape assertion: payload AND "completed" both present.
if printf '%s' "$body" | grep -q "$PAYLOAD" && \
   printf '%s' "$body" | grep -qi "completed"; then
  emit_pass "$SID" "async-message-body-shape"
else
  emit_fail "$SID" "async-message-body-shape" \
    "@system body contains '${PAYLOAD}' AND 'completed'" \
    "$(printf '%s' "$body" | tr '\n' ' ' | head -c 320)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Mark read so scenario 48's inbox baseline is clean.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum message read --all \
  >/dev/null 2>&1 || true

}  # _run_scenario_47

_run_scenario_47
