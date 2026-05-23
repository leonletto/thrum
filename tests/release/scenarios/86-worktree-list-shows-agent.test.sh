#!/usr/bin/env bash
# Scenario: worktree-list-shows-agent (migrates full_test_plan.md § 10D.6)
#
# Verifies `thrum worktree list --json` populates the per-row `agent`
# and `status` fields by reading each worktree's local
# `.thrum/identities/*.json`. Pins the read-side projection contract
# for the orchestrator UI surface that distinguishes worktrees by
# which agent owns them.
#
# Two assertions:
#   1. orch-row-present — list contains a row whose `path` matches
#      the new worktree path.
#   2. orch-row-agent-name — that row's `agent` field equals the
#      registered agent name (status is incidentally set to whatever
#      the inline-register flow stamps; we only pin agent name to
#      keep the assertion robust to default-status drift).
#
# Worktree creation + agent registration both run from COORD_PANE
# via send_bash_and_wait — same pattern as scenario 69's
# inline-registered tmux create. The daemon's tmux-create RPC
# rejects ephemeral tmux-exec callers when --name registration is
# requested (caller-identity guard); the COORD pane is registered
# as test_coordinator_main and satisfies the guard.
#
# JSON shape: `thrum worktree list --json` emits a top-level array
# of {path, branch, agent, status, ...} objects (cmd/thrum/main.go
# worktreeListJSON). Use capture_thrum_json to dodge the tmux 80-col
# wrap that mangles long absolute paths inside JSON strings.

SID="86-worktree-list-shows-agent"
WT_NAME="kafm9-86-orch"
WT_PATH="$WORKTREE_BASE/$COORD_BASENAME/${WT_NAME}"
ORCH_SESSION="kafm9-86-orch-session"
ORCH_AGENT="kafm9_86_orch_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_86() {

wait_for_pane_idle "$COORD_PANE" 60

# Step 1: create the worktree from COORD pane (registered-agent
# caller, mirrors scenario 13/69 pattern).
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create ${WT_NAME} --branch feature/${WT_NAME}" \
    "Worktree created" 60; then
  emit_fail "$SID" "orch-row-present" \
    "thrum worktree create ${WT_NAME} succeeds with 'Worktree created' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

elapsed=0
while [ ! -d "$WT_PATH" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ ! -d "$WT_PATH" ]; then
  emit_fail "$SID" "orch-row-present" \
    "worktree directory $WT_PATH" \
    "(directory missing after create)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: inline-register an agent via thrum tmux create driven
# from COORD pane (same caller-identity-guard rationale as
# scenario 69). Identity lands in $WT_PATH/.thrum/identities/.
wait_for_pane_idle "$COORD_PANE" 30
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux create ${ORCH_SESSION} --cwd ${WT_PATH} --name ${ORCH_AGENT} --role orchestrator --module testing --intent 'kafm9-86 orch'" \
    "Session created" 60; then
  emit_fail "$SID" "orch-row-present" \
    "thrum tmux create ${ORCH_SESSION} succeeds with 'Session created' line" \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_86_cleanup
  return 0
fi

# Brief poll for the identity file to land before list reads.
elapsed=0
while [ ! -f "$WT_PATH/.thrum/identities/${ORCH_AGENT}.json" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# Step 3: capture worktree list --json via the helper (host-side
# file write avoids the 80-col wrap memory bug on long paths).
local list_file="/tmp/kafm9-86-list-${RUNID}.json"
capture_thrum_json "$COORD_REPO" test_coordinator_main "$list_file" worktree list

# Assertion 1: list contains a row with path matching $WT_PATH.
# macOS resolves /var → /private/var so anchor on basename equality
# rather than full-path string match. The branch field is the
# secondary anchor (worktree names are unique within the run).
if jq -e --arg b "feature/${WT_NAME}" \
    '.[] | select(.branch == $b)' \
    "$list_file" >/dev/null 2>&1; then
  emit_pass "$SID" "orch-row-present"
else
  local got
  got=$(tr '\n' ' ' < "$list_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "orch-row-present" \
    "worktree list --json contains row with branch feature/${WT_NAME}" \
    "${got:-<no list output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_86_cleanup
  rm -f "$list_file"
  return 0
fi

# Assertion 2: the row's `agent` field equals $ORCH_AGENT.
local row_agent
row_agent=$(jq -r --arg b "feature/${WT_NAME}" \
  '.[] | select(.branch == $b) | .agent // ""' \
  "$list_file" 2>/dev/null)
if [ "$row_agent" = "$ORCH_AGENT" ]; then
  emit_pass "$SID" "orch-row-agent-name"
else
  emit_fail "$SID" "orch-row-agent-name" \
    "worktree row's .agent == '${ORCH_AGENT}'" \
    "got: '${row_agent}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$list_file"

_scenario_86_cleanup

}  # _run_scenario_86

# Cleanup split out so failure paths can call it without
# duplicating the kill+teardown sequence inline.
_scenario_86_cleanup() {
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux kill "$ORCH_SESSION" >/dev/null 2>&1 || true
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$WT_NAME" \
    >/dev/null 2>&1 || true
}

_run_scenario_86
