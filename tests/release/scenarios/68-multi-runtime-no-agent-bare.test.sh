#!/usr/bin/env bash
# Scenario: multi-runtime-no-agent-bare (migrates full_test_plan.md § 10C.8)
#
# Pins the contract for `thrum tmux create --no-agent`: the resulting
# session is a managed tmux session WITHOUT an associated agent
# registration. A regression that silently registered an agent on
# --no-agent would pollute `thrum team`, contaminate routing, and
# spawn a daemon-tracked agent identity for what was intended to be
# a bare-shell scratch session.
#
# Reuses rt-scratch as --cwd. **Tears down the rt-scratch fixture
# at scenario end** (last scenario in the kafm.8 batch that uses
# the shared worktree). Helpers/teardown.sh has a defensive fallback
# for partial-failure paths.
#
# Three assertions:
#   1. bare-session-not-in-team — `thrum team --json` does NOT
#      contain an agent whose name matches "bare-session" or whose
#      worktree path is rt-scratch (the bare session must NOT
#      register an agent).
#   2. bare-session-launch-success — `thrum tmux launch bare-session
#      --runtime shell` exits 0 against the bare session.
#   3. bare-session-alive — `thrum tmux status --json` reports
#      bare-session with state=alive after launch.
#
# Depends on scenario 62 (rt-scratch fixture).

SID="68-multi-runtime-no-agent-bare"
BARE_SESSION="bare-session"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_68() {

# Step 1: bare --no-agent session. --force tolerates leftover state.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux create "$BARE_SESSION" \
    --cwd "$RT_WT" \
    --no-agent --force >/dev/null 2>&1 || {
    emit_fail "$SID" "tmux-create-bare-session" \
      "thrum tmux create $BARE_SESSION --no-agent succeeds" \
      "(non-zero exit)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Step 2: assert NO agent named "bare-session" appears in team.
# `thrum team --json` is the structured form (markdown's
# `thrum team | grep` would also work, but JSON is parser-stable).
# File-redirect for capture so long output doesn't wrap-break.
local team_file="/tmp/kafm8-68-team-${RUNID}.json"
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "thrum team --json > '$team_file' 2>/dev/null"

# The agent list shape is .agents[] (or .agents.agents[] depending
# on schema version). Defensive: check both. The contract: no
# agent_id / name field equals "bare-session", and no agent's
# worktree contains rt-scratch's basename. Stronger than just a
# name check because --no-agent could in theory register an agent
# under a different synthetic name and still violate the contract.
local team_match
team_match=$(jq -r --arg n "$BARE_SESSION" --arg wt "$RT_WT_NAME" '
    [(.agents // .agents.agents // [])[]?
     | select((.name // "" | contains($n))
              or (.worktree // "" | contains($wt)))
    ] | length' "$team_file" 2>/dev/null || echo "?")

if [ "$team_match" = "0" ]; then
  emit_pass "$SID" "bare-session-not-in-team"
else
  local got
  got=$(tr '\n' ' ' < "$team_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "bare-session-not-in-team" \
    "thrum team --json contains 0 agents matching '$BARE_SESSION' or worktree '$RT_WT_NAME'" \
    "matched=${team_match}; team body: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$team_file"

# Step 3: launch shell against the bare session.
local launch_out launch_rc
launch_out=$(
  "$TE" exec --cwd "$COORD_REPO" --clean -- \
    env THRUM_NAME=test_coordinator_main thrum tmux launch "$BARE_SESSION" \
      --runtime shell 2>&1
)
launch_rc=$?
if [ "$launch_rc" -eq 0 ]; then
  emit_pass "$SID" "bare-session-launch-success"
else
  emit_fail "$SID" "bare-session-launch-success" \
    "thrum tmux launch $BARE_SESSION --runtime shell exits 0" \
    "exit ${launch_rc}; output: $(printf '%s' "$launch_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 4: poll status for alive. Same shape as scenarios 62/67.
local status_file="/tmp/kafm8-68-status-${RUNID}.json"
local elapsed=0
while [ "$elapsed" -lt 10 ]; do
  "$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
    "thrum tmux status --json > '$status_file' 2>/dev/null"
  if jq -e --arg n "$BARE_SESSION" \
      '.sessions[]? | select(.name == $n and .state == "alive")' \
      "$status_file" >/dev/null 2>&1; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if jq -e --arg n "$BARE_SESSION" \
    '.sessions[]? | select(.name == $n and .state == "alive")' \
    "$status_file" >/dev/null 2>&1; then
  emit_pass "$SID" "bare-session-alive"
else
  local got
  got=$(tr '\n' ' ' < "$status_file" 2>/dev/null | head -c 320)
  emit_fail "$SID" "bare-session-alive" \
    "tmux status --json contains $BARE_SESSION with state=alive" \
    "${got:-<no status output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$status_file"

}  # _run_scenario_68

_run_scenario_68

# Cleanup the bare session immediately, then tear down the
# rt-scratch shared fixture (last user of it in the kafm.8 batch).
# `|| true` everywhere — partial-state cleanup must not pollute
# the run's exit code; helpers/teardown.sh's defensive cleanup
# catches anything we miss.
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  thrum tmux kill "$BARE_SESSION" >/dev/null 2>&1 || true
"$TE" exec --cwd "$COORD_REPO" --clean -- \
  env THRUM_NAME=test_coordinator_main thrum worktree teardown "$RT_WT_NAME" \
  >/dev/null 2>&1 || true
