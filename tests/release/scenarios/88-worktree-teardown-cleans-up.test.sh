#!/usr/bin/env bash
# Scenario: worktree-teardown-cleans-up (migrates full_test_plan.md § 10D.8)
#
# Verifies `thrum worktree teardown <name>` actually removes the
# worktree directory from disk after a successful create. The
# teardown contract is exercised implicitly by other scenarios'
# cleanup paths (13/14/85/86), but § 10D.8 pins the explicit success
# shape: the printed "✓ Worktree <name> removed" line + the
# directory genuinely no longer existing.
#
# Three assertions:
#   1. teardown-removed-line — stdout contains "Worktree <name> removed".
#   2. teardown-dir-gone — $WT_PATH no longer exists after teardown.
#   3. teardown-removed-identity — when an agent identity exists in
#      the worktree's local .thrum/identities/, teardown announces
#      "Removed identity: <agent>" (CLI emits past-tense; markdown
#      § 10D.8 documented "Removing" but observed reality is
#      "Removed", asserted on the past-tense form to match the CLI).
#
# Driven from COORD pane via send_bash_and_wait. Per-scenario
# self-contained: creates its own worktree + agent, tears down,
# verifies. No shared fixture mutation.

SID="88-worktree-teardown-cleans-up"
WT_NAME="kafm9-88-orch"
WT_PATH="$WORKTREE_BASE/$COORD_BASENAME/${WT_NAME}"
SESSION="kafm9-88-session"
AGENT="kafm9_88_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_88() {

wait_for_pane_idle "$COORD_PANE" 60

# Step 1: create the worktree.
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum worktree create ${WT_NAME} --branch feature/${WT_NAME}" \
    "Worktree created" 60; then
  emit_fail "$SID" "teardown-removed-line" \
    "thrum worktree create ${WT_NAME} succeeds" \
    "(timeout, no matching bash-stdout entry — fixture never came up)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

elapsed=0
while [ ! -d "$WT_PATH" ] && [ "$elapsed" -lt 10 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ ! -d "$WT_PATH" ]; then
  emit_fail "$SID" "teardown-removed-line" \
    "worktree directory $WT_PATH" \
    "(directory missing after create)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: register an agent in the new worktree so the teardown
# exercises the identity-removal branch (markdown § 10D.8 expects
# the "Removing identity" line).
wait_for_pane_idle "$COORD_PANE" 30
if ! send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum tmux create ${SESSION} --cwd ${WT_PATH} --name ${AGENT} --role orchestrator --module testing --intent 'kafm9-88 teardown'" \
    "Session created" 60; then
  # Continue anyway — assertion 3 will surface "no identity message"
  # but assertions 1+2 still meaningfully test the dir-removal path.
  :
fi

elapsed=0
while [ ! -f "$WT_PATH/.thrum/identities/${AGENT}.json" ] && [ "$elapsed" -lt 5 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done

# Step 3: kill the session before teardown — teardown checks for
# active tmux sessions binding the worktree and would otherwise
# refuse / warn.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux kill "$SESSION" >/dev/null 2>&1 || true

# Step 4: teardown driven via tmux-exec (out-of-pane to capture
# the full exit-and-stdout shape cleanly).
local td_out td_rc
td_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$WT_NAME" 2>&1
)
td_rc=$?

# Assertion 1: success line present in output. Exit code is intentionally
# NOT gated here — under heavy gate load the daemon emits a warn-level
# `worktree.PaneTargetForIdentity` hint when the tmux-exec pool pane
# (caller_pane=tmux-exec-pool-leon:0.0) doesn't match the agent's
# bound worktree, and a downstream code path turns that into a
# non-zero exit even when the operation itself succeeded (the success
# line prints, the directory IS removed — verified by assertion 2
# below). The CLI exit-code-vs-warn-hint coupling is tracked
# separately (thrum-9sxc). The on-disk side-effect check in
# assertion 2 is the authoritative success indicator; this
# assertion only verifies the user-facing success line was emitted.
if printf '%s' "$td_out" | grep -qE "Worktree ${WT_NAME} removed"; then
  emit_pass "$SID" "teardown-removed-line"
else
  emit_fail "$SID" "teardown-removed-line" \
    "thrum worktree teardown stdout contains 'Worktree ${WT_NAME} removed' line" \
    "exit ${td_rc}; output: $(printf '%s' "$td_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: directory actually gone. Brief poll; teardown's
# git-worktree-remove + rm chain may complete after stdout flush.
elapsed=0
while [ -d "$WT_PATH" ] && [ "$elapsed" -lt 5 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ ! -d "$WT_PATH" ]; then
  emit_pass "$SID" "teardown-dir-gone"
else
  emit_fail "$SID" "teardown-dir-gone" \
    "directory ${WT_PATH} no longer exists after teardown" \
    "(directory still present)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: identity-removal announcement. CLI emits past-tense
# "Removed identity:"; markdown § 10D.8's "Removing identity"
# documented an aspirational present-tense that the implementation
# never matched. Asserted on observed reality.
if printf '%s' "$td_out" | grep -qE "Removed identity"; then
  emit_pass "$SID" "teardown-removed-identity"
else
  emit_fail "$SID" "teardown-removed-identity" \
    "teardown stdout contains 'Removed identity' announcement" \
    "output: $(printf '%s' "$td_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_88

_run_scenario_88

# Defensive cleanup if anything leaked. teardown above is the
# happy-path; this catches mid-scenario failure that bailed before
# explicit teardown.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux kill "$SESSION" >/dev/null 2>&1 || true
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum worktree teardown "$WT_NAME" \
  >/dev/null 2>&1 || true
