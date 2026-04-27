#!/usr/bin/env bash
# Scenario: restart-coord-fixture-prime-loads (migrates full_test_plan.md § 10.6)
#
# After `thrum tmux restart` (scenario 73) saves a snapshot, kills the
# old session, creates a new one, and launches claude, the new
# claude's SessionStart hook (inject-prime-context.sh) should consume
# the snapshot from .thrum/restart/<agent>.md and inject a briefing
# with "# Previous Session Context" + "## Resume Plan" sections.
#
# Two assertions:
#   1. post-restart-loud-preamble — the new SessionStart attachment
#      contains "🛑 ACTION REQUIRED" (the loud preamble the briefing
#      uses to block past-self's resume plan from being skipped).
#   2. post-restart-previous-context — the new SessionStart attachment
#      contains "# Previous Session Context" (the heading that wraps
#      the resumed snapshot).
#
# Mirrors scenarios 02/03's preamble assertions, scoped to the kafm6
# sub-fixture pane so the run-level coord/impl panes stay clean.
#
# Depends on scenario 73 (which triggered the restart).

SID="75-restart-coord-fixture-prime-loads"

if [ -z "${KAFM6_S1_AGENT:-}" ] || [ -z "${KAFM6_S1_SESSION:-}" ] || [ -z "${KAFM6_S1_WT:-}" ]; then
  emit_fail "$SID" "fixture-precondition" \
    "scenario 70 fixture identifiers exported" \
    "(missing — scenarios 70 + 73 must run first)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

_run_scenario_75() {

# Step 1: wait for the post-restart SessionStart attachment whose body
# carries the loud preamble. Scenario 73 returned ~immediately after
# the restart RPC; the new claude needs ~15s to boot and write
# SessionStart. 90s headroom matches scenario 02/03's polling.
#
# 5s sleep before polling: the OLD JSONL still holds matching
# SessionStart entries (from the pre-restart claude session) until
# the new claude writes its own JSONL file. Polling immediately
# would match a stale pre-restart attachment that doesn't carry the
# ACTION REQUIRED needle. The sleep gives the new claude time to
# create its post-restart JSONL before polling starts. Same
# precedent + race-condition rationale as scenario 02.
sleep 5
if ! wait_for_jsonl_match "$KAFM6_S1_WT" \
  '.attachment.hookEvent == "SessionStart" and (((.attachment.stdout // "" | tostring) | contains("ACTION REQUIRED")) or ((.attachment.content // "" | tostring) | contains("ACTION REQUIRED")))' \
  90 >/dev/null; then
  emit_fail "$SID" "post-restart-loud-preamble" \
    "post-restart SessionStart attachment containing \"ACTION REQUIRED\" within 90s" \
    "(none observed — daemon may not have triggered the loud-preamble injection, or claude hasn't booted)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: settle the pane so the post-restart auto-prime render
# completes before we type `!` keystrokes for the assertion checks.
# Same precedent as scenario 02/03's longer settle window.
wait_for_pane_idle "$KAFM6_S1_SESSION" 60

# Assertion 1: loud-preamble needle in SessionStart attachment.
send_command "$KAFM6_S1_SESSION" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh kafm6_75_loud_preamble \"🛑 ACTION REQUIRED\" SessionStart:startup"
assert_jsonl "$KAFM6_S1_SESSION" "$KAFM6_S1_WT" "$SID" "post-restart-loud-preamble" "VERIFIED kafm6_75_loud_preamble" \
  "scenarios/${SID}.test.sh:$LINENO"

# Assertion 2: Previous Session Context heading in SessionStart attachment.
send_command "$KAFM6_S1_SESSION" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh kafm6_75_previous_context \"# Previous Session Context\" SessionStart:startup"
assert_jsonl "$KAFM6_S1_SESSION" "$KAFM6_S1_WT" "$SID" "post-restart-previous-context" "VERIFIED kafm6_75_previous_context" \
  "scenarios/${SID}.test.sh:$LINENO"

}  # _run_scenario_75

_run_scenario_75
