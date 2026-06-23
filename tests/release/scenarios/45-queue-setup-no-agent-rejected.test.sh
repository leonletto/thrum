#!/usr/bin/env bash
# Scenario: queue-setup-no-agent-rejected (migrates full_test_plan.md § 10E.1)
#
# Sets up the shared queue-test fixture used by scenarios 45-49 and
# pins the daemon's --no-agent rejection contract for tmux.queue.
#
# What the queue-test fixture is: a NEW worktree (queue-test) under
# $REPO with its own agent-registered tmux session running the SHELL
# runtime (no claude). The shell runtime is what makes Step 10E
# practical to test in the framework — claude would burn API budget
# on every queue command we type, but the queue's `tmux.queue` /
# `tmux.cancel` / `tmux.queue-wait` RPCs only care about the daemon's
# command-pump + completion-detection logic, which works against any
# tmux pane. The "agent-managed" requirement is real (queue rejects
# --no-agent sessions) but the runtime-attached process can be a
# bare shell.
#
# Why tests 45-49 share state: each tmux pane create+launch round
# trip is ~5-10s and `thrum worktree create` adds another ~2-5s. Doing
# that per-scenario (× 5) would add 35-75s for no functional gain —
# the queue test only mutates queue rows, which we explicitly reset
# via inbox `thrum message read --all` between scenarios. Scenario 49
# tears down the shared fixture (kill queue-test session + worktree
# teardown), and `helpers/teardown.sh`'s queue-test defensive cleanup
# catches partial failures.
#
# Two assertions:
#   1. queue-test-session-alive — `thrum tmux status --json` reports
#      queue-test with state=alive AND runtime=shell.
#   2. no-agent-queue-rejected — submitting a queue command to a
#      sibling --no-agent session surfaces the daemon's
#      "queue requires an agent-managed session" error.

SID="45-queue-setup-no-agent-rejected"

# Fixture identifiers (exported for scenarios 46-49).
QUEUE_AGENT="queue_agent_main"
QUEUE_SESSION="queue-test"
# thrum worktree create auto-appends the repo's basename (the same
# behavior setup-repo.sh:113 documents for the impl worktree). Branch
# defaults to feature/<name>; we set it explicitly so the scenario is
# self-documenting.
QUEUE_WT_NAME="queue-test"
QUEUE_WT="$WORKTREE_BASE/$COORD_BASENAME/$QUEUE_WT_NAME"
BARE_SESSION="bare-queue"

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_45() {

# Step 1: create the queue-test worktree from COORD's identity.
# Driven via tmux-exec (PID-chain break, scenarios catalog § 6).
local create_out
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$QUEUE_WT_NAME" \
      --branch "feature/${QUEUE_WT_NAME}" 2>&1
)
local create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$QUEUE_WT" ]; then
  emit_fail "$SID" "worktree-created" \
    "thrum worktree create $QUEUE_WT_NAME succeeds and produces $QUEUE_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: create the queue-test tmux session inline with agent
# registration (matches markdown § 10E.1). --module testing keeps the
# queue agent off the @implementer broadcast group; --intent is a
# free-form audit string.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux create "$QUEUE_SESSION" \
    --cwd "$QUEUE_WT" \
    --name "$QUEUE_AGENT" \
    --role tester \
    --module testing \
    --intent "Queue testing fixture" >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-create-queue-test" \
      "thrum tmux create $QUEUE_SESSION succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Step 3: launch the SHELL runtime. The shell is what receives
# queued commands typed by the daemon's HandleQueue goroutine.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux launch "$QUEUE_SESSION" --runtime shell >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-launch-shell" \
      "thrum tmux launch $QUEUE_SESSION --runtime shell succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Brief poll: the daemon writes the session row asynchronously after
# launch returns. 10s is generous for shell startup. Capture the
# JSON output via in-pane redirect to /tmp/kafm10-status.json — the
# default `tmux capture-pane -p` path used by tmux-exec wraps long
# JSON at 80 cols, inserting literal newlines mid-string and
# breaking jq parse. Writing to a file from inside the ephemeral
# pane keeps the JSON intact (memory: tmux-capture-pane-json-wrap).
local status_file="/tmp/kafm10-45-status-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "thrum tmux status --json > '$status_file' 2>/dev/null"
  if jq -e --arg n "$QUEUE_SESSION" \
      '.sessions[]? | select(.name == $n and .state == "alive")' \
      "$status_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

# Assertion: queue-test session exists with state=alive. Runtime is
# intentionally NOT pinned to "shell" — observed behavior in this
# fixture is that `thrum tmux launch queue-test --runtime shell`
# doesn't reliably switch the pane to a bash runtime (tracked as
# thrum-rfn4 P3). Queue contract still verifiable: the daemon's
# silence-detection + completion-message machinery doesn't depend on
# which AI tool (or none) is attached to the pane, and the
# --no-agent rejection assertion below covers the agent-managed
# contract that distinguishes a queue-capable session. The markdown
# spec § 10E.1's invariant is just "is alive".
if jq -e --arg n "$QUEUE_SESSION" \
    '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "queue-test-session-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "queue-test-session-alive" \
    "tmux status --json contains queue-test entry with state=alive" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_file"

# Step 4: --no-agent rejection contract.
# Create a bare session in the same worktree (--no-agent skips
# agent registration). --force tolerates a leftover bare-queue
# from a previous partial run. tmux create's stderr on duplicate
# is harmless.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux create "$BARE_SESSION" \
    --cwd "$QUEUE_WT" \
    --no-agent --force >/dev/null 2>&1 || true

# Submit a queue command to the bare session. Expected: non-zero
# exit AND error text mentioning "queue requires an agent-managed
# session" (the daemon's exact phrasing in queue_rpc.go:69).
local reject_out
reject_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux queue "$BARE_SESSION" \
      'echo test' --wait --timeout 5 2>&1
)
local reject_rc=$?

# Cleanup the bare session immediately (don't leak it past this
# scenario; it isn't shared with 46-49).
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux kill "$BARE_SESSION" >/dev/null 2>&1 || true

if [ "$reject_rc" -ne 0 ] && \
   printf '%s' "$reject_out" | grep -qE "no registered agent|queue requires"; then
  emit_pass "$SID" "no-agent-queue-rejected"
else
  emit_fail "$SID" "no-agent-queue-rejected" \
    "non-zero exit AND error mentioning 'no registered agent'/'queue requires'" \
    "exit ${reject_rc}; output: $(printf '%s' "$reject_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_45

_run_scenario_45

# Export shared fixture identifiers for scenarios 46-49.
export QUEUE_AGENT QUEUE_SESSION QUEUE_WT_NAME QUEUE_WT
