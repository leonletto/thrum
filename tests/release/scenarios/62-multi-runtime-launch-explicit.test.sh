#!/usr/bin/env bash
# Scenario: multi-runtime-launch-explicit (migrates full_test_plan.md § 10C.1)
#
# Sets up the shared rt-scratch fixture used by scenarios 62, 65, 66,
# 68 (torn down by scenario 68) AND pins the contract that
# `thrum tmux launch <session> --runtime shell` succeeds against a
# `--no-agent` managed session created with `thrum tmux create`.
#
# What the rt-scratch fixture is: a fresh worktree (rt-scratch) under
# the run-level $WORKTREE_BASE/repo/, used as the cwd for managed
# tmux sessions in 10C scenarios that don't need agent registration
# in the session's pane. tmux create requires --cwd to point at a
# git worktree with .thrum/redirect (post-nu16 x6e8.6); the run-level
# $REPO is NOT a worktree (it's the main repo), so a sibling worktree
# is required.
#
# Why scenarios share rt-scratch: each `thrum worktree create` adds
# ~2-5s and tmux create+launch adds another ~5s. Scenarios 62/65/66/68
# each only need a managed --no-agent session in some worktree —
# rebuilding rt-scratch four times would add ~12-20s with zero
# functional value. Scenarios 67 and 69 use their own per-scenario
# worktrees (test-tmux-start, test-force-restart) because they pin
# isolation contracts unrelated to the shared fixture.
#
# Two assertions:
#   1. launch-explicit-success — `thrum tmux launch runtime-test
#      --runtime shell` exits 0 (the daemon's HandleLaunch picks up
#      the --runtime flag at the top of the resolution chain).
#   2. runtime-test-session-alive — `thrum tmux status --json`
#      reports runtime-test with state=alive after launch.
#
# Note on runtime field assertion: same as kafm.10 scenario 45, we do
# NOT pin the runtime field to "shell" — thrum-rfn4 (P3) tracks
# observed inconsistency between `--runtime shell` and the runtime
# stamped on the session row. The pane-aliveness invariant is the
# stable contract this scenario asserts; runtime resolution behavior
# is more directly exercised by scenario 64 (resolution chain) and
# scenario 66 (prime command varies).

SID="62-multi-runtime-launch-explicit"

# Fixture identifiers (exported for scenarios 65, 66, 68).
RT_WT_NAME="rt-scratch"
RT_WT="$WORKTREE_BASE/repo/$RT_WT_NAME"
RT_SESSION="runtime-test"

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_62() {

# Step 1: create the rt-scratch worktree from coord's identity. tmux
# create + launch in subsequent scenarios target this worktree as
# their --cwd.
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$RT_WT_NAME" \
      --branch "feature/${RT_WT_NAME}" 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$RT_WT" ]; then
  emit_fail "$SID" "rt-scratch-worktree-created" \
    "thrum worktree create $RT_WT_NAME succeeds and produces $RT_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: create a --no-agent managed tmux session in rt-scratch.
# --no-agent is the right shape for runtime tests: we don't want to
# register a separate agent per scenario (and the runtime contract is
# orthogonal to agent registration). --force tolerates leftover state
# from a prior partial run.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux create "$RT_SESSION" \
    --cwd "$RT_WT" \
    --no-agent --force >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-create-runtime-test" \
      "thrum tmux create $RT_SESSION succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Step 3: launch with explicit --runtime shell. shell is always
# available (no external tool needed) and exercises the launch-time
# runtime resolution code path. THRUM_NAME pin keeps the call uniform
# with other out-of-pane invocations in the kafm.8 batch.
local launch_out launch_rc
launch_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux launch "$RT_SESSION" \
      --runtime shell 2>&1
)
launch_rc=$?
if [ "$launch_rc" -eq 0 ]; then
  emit_pass "$SID" "launch-explicit-success"
else
  emit_fail "$SID" "launch-explicit-success" \
    "thrum tmux launch $RT_SESSION --runtime shell exits 0" \
    "exit ${launch_rc}; output: $(printf '%s' "$launch_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 4: poll `thrum tmux status --json` until the runtime-test row
# reports state=alive. Daemon writes the row asynchronously after
# launch returns; 10s is generous for shell startup. capture_thrum_json
# (helpers/drive.sh) wraps the file-redirect→host-jq pattern (memory:
# tmux-capture-pane-json-wrap).
local status_file="/tmp/kafm8-62-status-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  capture_thrum_json "$COORD_REPO" test_coordinator_main "$status_file" tmux status
  if jq -e --arg n "$RT_SESSION" \
      '.sessions[]? | select(.name == $n and .state == "alive")' \
      "$status_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if jq -e --arg n "$RT_SESSION" \
    '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "runtime-test-session-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "runtime-test-session-alive" \
    "tmux status --json contains $RT_SESSION entry with state=alive" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_file"

# Per-scenario cleanup: kill runtime-test so the session name is
# free for any subsequent reuse and the daemon's session table
# doesn't leak. rt-scratch worktree is preserved for scenarios
# 65/66/68 (they create their own per-scenario session names but
# share the worktree as --cwd).
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum tmux kill "$RT_SESSION" >/dev/null 2>&1 || true

}  # _run_scenario_62

_run_scenario_62

# Export shared fixture identifiers for scenarios 65, 66, 68.
# Scenario 68 tears down both the runtime-test session and the
# rt-scratch worktree; helpers/teardown.sh has a defensive fallback
# for partial-failure paths.
export RT_WT_NAME RT_WT RT_SESSION
