#!/usr/bin/env bash
# Scenario: queue-daemon-restart-recovery (migrates full_test_plan.md § 10E.5)
#
# Verifies `RecoverQueueState` (called from daemon startup): a command
# that was active (StateSent) when the daemon stopped is marked
# `interrupted` after `thrum daemon restart`, and the requester gets
# an @system message mentioning interruption + daemon restart.
#
# Two assertions:
#   1. restart-system-message-arrives — an @system message mentioning
#      the cmd id lands in the requester's inbox after restart.
#   2. restart-message-mentions-interruption — the message body
#      contains 'interrupted' AND ('restart' OR 'daemon'). Matches
#      the daemon's exact body template:
#      "Command <id> interrupted by daemon restart.
#       Session: <s>
#       Resubmit if needed."
#      (queue_rpc.go:729)
#
# Final-scenario teardown: this scenario also tears down the queue-test
# fixture (kill session + worktree teardown) since it's the last in
# the kafm.10 batch. helpers/teardown.sh has a defensive cleanup that
# catches partial failures earlier in the batch.
#
# Depends on scenario 45's queue-test session.

SID="49-queue-daemon-restart-recovery"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
PAYLOAD="kafm10-49-pre-restart-${RUNID}"

_run_scenario_49() {

# Clear inbox so the recovery message is the only @system entry
# matching $cmd_id below.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum message read --all \
  >/dev/null 2>&1 || true

# Submit a long-running command. We need it in StateSent (active)
# when the daemon stops — that's the state RecoverQueueState
# transitions to interrupted.
local submit_out submit_rc
submit_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux queue "$QUEUE_SESSION" \
      "echo ${PAYLOAD} && sleep 120" --timeout 300 2>&1
)
submit_rc=$?
local cmd_id
cmd_id=$(printf '%s' "$submit_out" | grep -oE 'cmd_[a-zA-Z0-9]+' | head -1)
if [ "$submit_rc" -ne 0 ] || [ -z "$cmd_id" ]; then
  emit_fail "$SID" "restart-system-message-arrives" \
    "queue submit succeeds with a cmd_xxx id (precondition for restart test)" \
    "exit ${submit_rc}; output: $(printf '%s' "$submit_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_49_drain_assertion; _scenario_49_cleanup_worktree
  return 0
fi

# Wait for the command to enter StateSent (daemon types it into the
# pane). 3s mirrors the markdown spec timing.
sleep 3

# Restart the daemon. tmux-exec breaks the PID chain so the daemon
# stop signal doesn't ride up through the runner's parent claude.
# THRUM_NAME prefix kept consistent with all other out-of-pane
# thrum invocations in the kafm.10 batch — daemon restart is
# daemon-scoped (doesn't strictly need an identity) but uniform
# prefixing matters for audit/telemetry.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum daemon restart >/dev/null 2>&1 || true

# Give the daemon time to come back up, run RecoverQueueState
# (marks the command interrupted + writes the @system message),
# and reload its connection pool.
sleep 5

# Poll inbox for the recovery message. File-redirect for JSON
# capture (memory: tmux-capture-pane-json-wrap).
local inbox_file="/tmp/kafm10-49-inbox-${RUNID}.json"
local elapsed=0 body=""
while [ "$elapsed" -lt 30 ]; do
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "env THRUM_NAME=test_coordinator_main thrum inbox --all --json > '$inbox_file' 2>/dev/null"
  # Array wrapper keeps the full body intact for multi-line grep.
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
  emit_pass "$SID" "restart-system-message-arrives"
else
  emit_fail "$SID" "restart-system-message-arrives" \
    "@system message mentioning ${cmd_id} within 30s of daemon restart" \
    "(no matching @system message)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_49_drain_assertion; _scenario_49_cleanup_worktree
  return 0
fi

# Body shape: must mention "interrupted" AND one of "restart"/"daemon".
if printf '%s' "$body" | grep -qi "interrupted" && \
   printf '%s' "$body" | grep -qiE "restart|daemon"; then
  emit_pass "$SID" "restart-message-mentions-interruption"
else
  emit_fail "$SID" "restart-message-mentions-interruption" \
    "@system body contains 'interrupted' AND ('restart' OR 'daemon')" \
    "$(printf '%s' "$body" | tr '\n' ' ' | head -c 320)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Drain-on-kill assertion (§ 10E.6 absorption): kill the queue-test
# session and verify the queue is drained. HandleKill calls
# drainQueueOnKill internally; queue-status post-kill should report
# the empty shape (active=null, queued=empty) — getQueue() returns
# nil for unknown sessions, which marshals to an empty-active /
# empty-queued response. This is what completes the kafm.10.6
# cleanup contract; without this assertion the absorption claim is
# incomplete (the kill happens but drain isn't verified).
_scenario_49_drain_assertion

_scenario_49_cleanup_worktree

}  # _run_scenario_49

# Drain assertion split out so it runs BEFORE worktree teardown:
# queue-status against a torn-down worktree's daemon would surface
# as a connection error, not the empty-shape contract we want to
# verify. Order matters: kill → assert drain → teardown worktree.
_scenario_49_drain_assertion() {
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    thrum tmux kill "$QUEUE_SESSION" >/dev/null 2>&1 || true

  local status_file="/tmp/kafm10-49-drain-${RUNID}.json"
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "thrum tmux queue-status '$QUEUE_SESSION' --json > '$status_file' 2>/dev/null"
  if jq -e '((.active // null) == null) and (((.queued // []) | length) == 0)' \
      "$status_file" >/dev/null 2>&1; then
    emit_pass "$SID" "kill-drains-queue"
  else
    local got
    got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
    emit_fail "$SID" "kill-drains-queue" \
      "queue-status reports active=null AND queued=empty after kill (HandleKill drain)" \
      "${got:-<no status output>}" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
  rm -f "$status_file"
}

# Worktree teardown follows the drain assertion. The framework
# teardown trap (helpers/teardown.sh) has a defensive fallback that
# kills queue-test if scenario 49 never reaches this cleanup —
# but the worktree teardown is scenario-scoped and only happens
# here on the happy path.
_scenario_49_cleanup_worktree() {
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$QUEUE_WT_NAME" \
    >/dev/null 2>&1 || true
}

_run_scenario_49
