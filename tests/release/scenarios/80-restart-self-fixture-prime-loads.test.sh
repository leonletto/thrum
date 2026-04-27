#!/usr/bin/env bash
# Scenario: restart-self-fixture-prime-loads (migrates full_test_plan.md § 10.11)
#
# Closes the self-restart chain: after scenario 79's manual exit and
# bare-tmux relaunch, the new claude session's SessionStart hook
# (inject-prime-context.sh) should consume the snapshot scenario 77
# wrote and inject "# Previous Session Context" with the snapshot's
# original reason marker. § 10.11's contract is the bridge between
# the agent's self-saved snapshot and the next session's recovered
# context — without it, a self-/exit&restart loop loses all
# conversation history.
#
# Two assertions:
#   1. self-restart-previous-context — post-relaunch SessionStart
#      attachment contains "# Previous Session Context".
#   2. self-restart-reason-preserved — that same attachment also
#      carries the reason marker scenario 77 supplied via
#      `--reason '${KAFM6_S2_SAVE_REASON}'` (proves the rendered
#      content is THIS run's snapshot, not a stale snapshot or a
#      cross-test leak).
#
# Filtering: both assertions scope to JSONL entries with
# .timestamp >= ${KAFM6_S2_RELAUNCH_FLOOR_TS} (exported by 79) so
# the pre-kill session's old SessionStart attachment can't false-
# match.
#
# Also tears down the Scenario 2 sub-fixture at scenario end (mirrors
# scenario 76 for the Scenario 1 chain). Cleanup runs unconditionally
# — assertion failure shouldn't leak fixtures into the next run.
#
# Depends on scenarios 77, 78, 79.

SID="80-restart-self-fixture-prime-loads"

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

# Defined ahead of any callers (precondition early-return path uses it
# too). Cleanup runs unconditionally — assertion failure shouldn't leak
# fixtures into the next run.
_scenario_80_cleanup() {
  # Raw kill of the manually-relaunched session (daemon doesn't
  # know about it — `thrum tmux kill` would no-op).
  [ -n "${KAFM6_S2_SESSION:-}" ] && tmux kill-session -t "$KAFM6_S2_SESSION" 2>/dev/null || true
  # Worktree teardown via thrum (worktree was created by thrum in
  # scenario 77). Stale daemon session-row referencing the killed
  # tmux session is non-fatal — daemon self-heals on next sweep.
  if [ -n "${KAFM6_S2_WT_NAME:-}" ]; then
    "$TE" exec --cwd "$COORD_REPO" --clean -- \
      env THRUM_NAME=test_coordinator_main thrum worktree teardown "$KAFM6_S2_WT_NAME" \
      >/dev/null 2>&1 || true
  fi
}

if [ -z "${KAFM6_S2_AGENT:-}" ] || [ -z "${KAFM6_S2_SESSION:-}" ] || [ -z "${KAFM6_S2_WT:-}" ] || [ -z "${KAFM6_S2_WT_NAME:-}" ] || [ -z "${KAFM6_S2_SAVE_REASON:-}" ] || [ -z "${KAFM6_S2_RELAUNCH_FLOOR_TS:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenarios 77+79 fixture identifiers exported (KAFM6_S2_*, KAFM6_S2_RELAUNCH_FLOOR_TS)" \
    "(missing — scenarios 77 + 79 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_80_cleanup
  return 0
fi

_run_scenario_80() {

# Assertion 1: SessionStart attachment with Previous Session Context
# heading, timestamp-scoped to entries from 79's relaunch.
local context_filter='.type == "attachment"
        and (.attachment.hookEvent == "SessionStart")
        and (.timestamp >= "'"$KAFM6_S2_RELAUNCH_FLOOR_TS"'")
        and (((.attachment.stdout // "" | tostring) | contains("# Previous Session Context"))
             or ((.attachment.content // "" | tostring) | contains("# Previous Session Context")))'
if wait_for_jsonl_match "$KAFM6_S2_WT" "$context_filter" 90 >/dev/null; then
  emit_pass "$SID" "self-restart-previous-context"
else
  emit_fail "$SID" "self-restart-previous-context" \
    "post-relaunch SessionStart attachment containing \"# Previous Session Context\"" \
    "(none observed within 90s after ${KAFM6_S2_RELAUNCH_FLOOR_TS} — inject-prime-context.sh hook may not have found the snapshot)" \
    "scenarios/${SID}.test.sh:$LINENO"
  _scenario_80_cleanup
  return 0
fi

# Assertion 2: same attachment carries the original save-reason
# marker, proving the rendered context is from THIS run's snapshot.
local reason_filter='.type == "attachment"
        and (.attachment.hookEvent == "SessionStart")
        and (.timestamp >= "'"$KAFM6_S2_RELAUNCH_FLOOR_TS"'")
        and (((.attachment.stdout // "" | tostring) | contains("'"$KAFM6_S2_SAVE_REASON"'"))
             or ((.attachment.content // "" | tostring) | contains("'"$KAFM6_S2_SAVE_REASON"'")))'
if wait_for_jsonl_match "$KAFM6_S2_WT" "$reason_filter" 60 >/dev/null; then
  emit_pass "$SID" "self-restart-reason-preserved"
else
  emit_fail "$SID" "self-restart-reason-preserved" \
    "post-relaunch SessionStart attachment containing reason marker \"${KAFM6_S2_SAVE_REASON}\"" \
    "(reason not found in attachment body — snapshot may have been replaced or hook stripped the reason)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_80

_run_scenario_80
_scenario_80_cleanup
