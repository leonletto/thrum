#!/usr/bin/env bash
# Scenario: multi-runtime-tmux-start-detached-worktree (migrates full_test_plan.md § 10C.7)
#
# Pins the contract that `thrum tmux create + launch shell` works
# against a detached-HEAD worktree (created via `thrum worktree
# create --detach`). The markdown framing of this scenario as "tmux
# start all-in-one" is narrative — `thrum tmux start` itself is
# attach-blocking (interactive) and verified manually; the migrated
# automation drives the create + launch + alive contract on a
# detached worktree, which is the underlying surface the all-in-one
# command exercises.
#
# Detached worktrees are a real surface: orchestrators sometimes
# spin up scratch worktrees for short-lived agent panes without
# committing a branch name. A regression where tmux create or
# launch rejected a detached worktree would block that workflow
# silently.
#
# Per-scenario detached worktree (test-tmux-start) — distinct from
# the rt-scratch shared fixture so the --detach contract isn't
# entangled with the branched-worktree assertions in 62/65/66/68.
# Worktree is torn down at scenario end.
#
# Two assertions:
#   1. tmux-start-launch-success — `thrum tmux launch
#      tmux-start-test --runtime shell` exits 0 against a
#      detached-HEAD --cwd.
#   2. tmux-start-session-alive — `thrum tmux status --json` reports
#      tmux-start-test with state=alive.

SID="67-multi-runtime-tmux-start-detached-worktree"
START_WT_NAME="test-tmux-start"
START_WT="$WORKTREE_BASE/repo/$START_WT_NAME"
START_SESSION="tmux-start-test"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_67() {

# Step 1: detached-HEAD worktree from coord identity. --detach is
# the surface this scenario specifically targets; without it the
# scenario degenerates into a duplicate of 62.
local create_out create_rc
create_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree create "$START_WT_NAME" \
      --detach 2>&1
)
create_rc=$?
if [ "$create_rc" -ne 0 ] || [ ! -d "$START_WT" ]; then
  emit_fail "$SID" "detached-worktree-created" \
    "thrum worktree create --detach succeeds and produces $START_WT/" \
    "exit ${create_rc}; output: $(printf '%s' "$create_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: tmux create + launch shell. --no-agent + --force matches
# the markdown § 10C.7 invocation.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux create "$START_SESSION" \
    --cwd "$START_WT" \
    --no-agent --force >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-create-on-detached" \
      "thrum tmux create $START_SESSION on detached worktree succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    _scenario_67_cleanup
    return 0
  }

local launch_out launch_rc
launch_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux launch "$START_SESSION" \
      --runtime shell 2>&1
)
launch_rc=$?
if [ "$launch_rc" -eq 0 ]; then
  emit_pass "$SID" "tmux-start-launch-success"
else
  emit_fail "$SID" "tmux-start-launch-success" \
    "thrum tmux launch $START_SESSION --runtime shell exits 0" \
    "exit ${launch_rc}; output: $(printf '%s' "$launch_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_67_cleanup
  return 0
fi

# Step 3: poll status JSON for state=alive (file-redirect→jq per
# tmux-capture-pane-json-wrap memory; same shape as scenarios 45,
# 47, 49 — uniform so a future capture_thrum_json sweep can lift
# all four call sites cleanly).
local status_file="/tmp/kafm8-67-status-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "thrum tmux status --json > '$status_file' 2>/dev/null"
  if jq -e --arg n "$START_SESSION" \
      '.sessions[]? | select(.name == $n and .state == "alive")' \
      "$status_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if jq -e --arg n "$START_SESSION" \
    '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "tmux-start-session-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "tmux-start-session-alive" \
    "tmux status --json contains $START_SESSION entry with state=alive" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_file"

_scenario_67_cleanup

}  # _run_scenario_67

# Cleanup split out so the early-return failure paths can call it
# without duplicating the kill+teardown sequence inline.
_scenario_67_cleanup() {
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    thrum tmux kill "$START_SESSION" >/dev/null 2>&1 || true
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum worktree teardown "$START_WT_NAME" \
    >/dev/null 2>&1 || true
}

_run_scenario_67
