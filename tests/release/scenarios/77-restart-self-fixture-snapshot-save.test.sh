#!/usr/bin/env bash
# Scenario: restart-self-fixture-snapshot-save (migrates full_test_plan.md § 10.8)
#
# Sets up the "Scenario 2: Self-Initiated Restart" sub-fixture and pins
# § 10.8's contract: an agent in its own pane invokes `thrum tmux
# snapshot save --reason '...'` and the daemon emits the canonical
# "Restart snapshot saved for <agent>" success line. Same shape as
# scenario 09's `save-success-line` assertion but driven against a
# kafm6-owned sub-fixture pane (the run-level coord/impl panes are
# off-limits for restart cycles per the dispatch's au7k discipline).
#
# This scenario sets up the fixture used by 78/79/80 and is the
# self-restart counterpart to scenarios 70-76's coord-restart chain.
#
# One assertion: snapshot save success line emitted from the kafm6
# self-restart pane.
#
# Why claude (not shell) runtime: § 10.11 (scenario 80) asserts the
# post-relaunch SessionStart hook injects "Previous Session Context"
# from the snapshot — that contract only exists for runtimes that
# fire SessionStart hooks, i.e. claude.

SID="77-restart-self-fixture-snapshot-save"

# Fixture identifiers (exported for scenarios 78-80).
KAFM6_S2_AGENT="kafm6_test_self"
KAFM6_S2_SESSION="kafm6-self-restart-test"
KAFM6_S2_WT_NAME="kafm6-self-restart-wt"
KAFM6_S2_WT="$WORKTREE_BASE/repo/$KAFM6_S2_WT_NAME"
KAFM6_S2_SAVE_REASON="kafm6-self-restart-${RUNID}"
KAFM6_S2_SNAPSHOT_FILE="$KAFM6_S2_WT/.thrum/restart/$KAFM6_S2_AGENT.md"

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_77() {

# Step 1: sub-worktree from coord identity.
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$KAFM6_S2_WT_NAME" \
      --branch "feature/${KAFM6_S2_WT_NAME}" 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$KAFM6_S2_WT" ]; then
  emit_fail "$SID" "snapshot-save-success-line" \
    "thrum worktree create $KAFM6_S2_WT_NAME succeeds and produces $KAFM6_S2_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: agent-registered tmux session, driven from COORD_PANE
# (daemon's caller-identity guard rejects inline-registration tmux
# create from tmux-exec).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux create $KAFM6_S2_SESSION --cwd $KAFM6_S2_WT --name $KAFM6_S2_AGENT --role implementer --module testing --intent 'kafm6 self-restart fixture'" \
    "Session created" 60; then
  emit_fail "$SID" "snapshot-save-success-line" \
    "thrum tmux create $KAFM6_S2_SESSION (driven from COORD pane) succeeds" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Brief settle between create and launch — daemon's session-row write
# is async; a fast launch immediately after create can race the row
# being persisted, which surfaces as a "session not found" exit 1
# from the launch RPC.
sleep 2

# Step 3: launch claude. Capture output for failure diagnostics
# (mirrors scenario 70's pattern — redirecting to /dev/null in an
# earlier draft hid the actual failure mode).
local launch_out launch_rc
launch_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux launch "$KAFM6_S2_SESSION" 2>&1
)
launch_rc=$?
if [ "$launch_rc" -ne 0 ]; then
  emit_fail "$SID" "snapshot-save-success-line" \
    "thrum tmux launch $KAFM6_S2_SESSION succeeds" \
    "exit ${launch_rc}; output: $(printf '%s' "$launch_out" | tr '\n' ' ' | head -c 320)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 4: wait for claude SessionStart so the pane has at least one
# JSONL entry and the auto-prime has reclaimed agent_pid (snapshot
# save needs a live agent_pid to extract from).
if ! wait_for_session_start "$KAFM6_S2_WT" 60; then
  emit_fail "$SID" "snapshot-save-success-line" \
    "claude SessionStart in $KAFM6_S2_WT within 60s of launch" \
    "(none observed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Settle before sending `!` so bash-prefix mode engages cleanly.
wait_for_pane_idle "$KAFM6_S2_SESSION" 60

# Pre-clean any leftover snapshot so 78's assertions see only what
# this scenario produced. (Highly unlikely to exist on a fresh
# sub-fixture, but cheap insurance against a partial-failure re-run
# if THRUM_RELEASE_NO_TEARDOWN was set on a prior run.)
rm -f "$KAFM6_S2_SNAPSHOT_FILE"

# Assertion: agent in pane saves snapshot via `! thrum tmux snapshot
# save`. Same shape as scenario 09 (CLI-direct save assertion) but
# driven from the self-restart sub-fixture pane.
if send_bash_and_wait "$KAFM6_S2_SESSION" "$KAFM6_S2_WT" \
    "thrum tmux snapshot save --reason '${KAFM6_S2_SAVE_REASON}'" \
    "Restart snapshot saved for ${KAFM6_S2_AGENT}" 60; then
  emit_pass "$SID" "snapshot-save-success-line"
else
  emit_fail "$SID" "snapshot-save-success-line" \
    "thrum tmux snapshot save stdout containing \"Restart snapshot saved for ${KAFM6_S2_AGENT}\"" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_77

_run_scenario_77

# Export shared fixture identifiers for scenarios 78-80.
export KAFM6_S2_AGENT KAFM6_S2_SESSION KAFM6_S2_WT_NAME KAFM6_S2_WT
export KAFM6_S2_SAVE_REASON KAFM6_S2_SNAPSHOT_FILE
