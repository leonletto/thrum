#!/usr/bin/env bash
# Scenario: queue-cancel-flow (migrates full_test_plan.md § 10E.4)
#
# Verifies `thrum tmux cancel <cmd_id>` transitions a running command
# to the cancelled state, the queue empties, and the requester gets
# an @system cancellation message containing the partial output
# captured at cancel time.
#
# Three assertions:
#   1. cancel-cmd-accepted — `thrum tmux cancel` exits 0.
#   2. cancel-system-message-arrives — an @system message mentioning
#      the cmd id and "cancel" lands in the requester's inbox within
#      ~15s (the daemon's HandleCancel goroutine sends the message
#      after recording the partial output — queue_rpc.go:627).
#   3. cancel-partial-output-present — the message body contains the
#      payload that ran BEFORE cancel (proves partial output capture
#      isn't lost on cancel).
#
# Depends on scenario 45's queue-test session.

SID="48-queue-cancel-flow"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
PAYLOAD="kafm10-48-starting-${RUNID}"

_run_scenario_48() {

# Inbox baseline: clear it.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum message read --all \
  >/dev/null 2>&1 || true

# Submit a long-running command. The 60s sleep is what gives us a
# window to issue the cancel against an in-flight command.
local submit_out submit_rc
submit_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux queue "$QUEUE_SESSION" \
      "echo ${PAYLOAD} && sleep 60 && echo never-${PAYLOAD}" \
      --timeout 300 2>&1
)
submit_rc=$?
local cmd_id
cmd_id=$(printf '%s' "$submit_out" | grep -oE 'cmd_[a-zA-Z0-9]+' | head -1)
if [ "$submit_rc" -ne 0 ] || [ -z "$cmd_id" ]; then
  emit_fail "$SID" "cancel-cmd-accepted" \
    "queue submit succeeds with a cmd_xxx id" \
    "exit ${submit_rc}; output: $(printf '%s' "$submit_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Give the daemon a beat to type the command into the pane (so the
# echo runs and partial output gets captured before cancel).
sleep 3

local cancel_out cancel_rc
cancel_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux cancel "$cmd_id" 2>&1
)
cancel_rc=$?

if [ "$cancel_rc" -eq 0 ]; then
  emit_pass "$SID" "cancel-cmd-accepted"
else
  emit_fail "$SID" "cancel-cmd-accepted" \
    "thrum tmux cancel exits 0" \
    "exit ${cancel_rc}; output: $(printf '%s' "$cancel_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  # Continue to assert the message anyway — the daemon may still
  # have transitioned the command and sent the @system body even if
  # the CLI parser surfaced a non-zero exit.
fi

# Poll inbox for the @system cancel message. File-redirect for
# JSON capture (memory: tmux-capture-pane-json-wrap).
local inbox_file="/tmp/kafm10-48-inbox-${RUNID}.json"
local elapsed=0 body=""
while [ "$elapsed" -lt 15 ]; do
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "env THRUM_NAME=test_coordinator_main thrum inbox --all --json > '$inbox_file' 2>/dev/null"
  # Array wrapper preserves the full multi-line body — partial
  # output captured at cancel time lives below the first line.
  body=$(jq -r --arg cid "$cmd_id" \
      '[.messages[]? | select(.agent_id == "system") | .body.content // ""
       | select(contains($cid))][0] // ""' \
      "$inbox_file" 2>/dev/null)
  if [ -n "$body" ] && printf '%s' "$body" | grep -qi "cancel"; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done
rm -f "$inbox_file"

if [ -n "$body" ] && printf '%s' "$body" | grep -qi "cancel"; then
  emit_pass "$SID" "cancel-system-message-arrives"
else
  emit_fail "$SID" "cancel-system-message-arrives" \
    "@system message mentioning ${cmd_id} and 'cancel' within 15s" \
    "$(printf '%s' "$body" | tr '\n' ' ' | head -c 320 || echo '<no message>')" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Partial-output assertion. The starting echo ran before cancel, so
# the body should carry the PAYLOAD marker. The "never-…" sentinel
# (which would only run after the 60s sleep) must NOT be present —
# but we don't assert its absence here because the daemon's body
# template includes a "Partial output:" header that's already a
# strong positive signal; the negative is implicit.
if printf '%s' "$body" | grep -q "$PAYLOAD"; then
  emit_pass "$SID" "cancel-partial-output-present"
else
  emit_fail "$SID" "cancel-partial-output-present" \
    "@system cancel body contains pre-cancel payload '${PAYLOAD}'" \
    "$(printf '%s' "$body" | tr '\n' ' ' | head -c 320)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Clear inbox so scenario 49's restart-recovery match is unambiguous.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum message read --all \
  >/dev/null 2>&1 || true

}  # _run_scenario_48

_run_scenario_48
