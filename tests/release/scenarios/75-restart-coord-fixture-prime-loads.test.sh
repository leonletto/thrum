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

# Assertions 1 & 2: read the post-restart SessionStart attachment directly
# from the JSONL (harness-side) instead of driving the shared COORD pane with
# two `!`-probes. Under full-gate load the coord pane is busy (claude 2.1.x
# does more autonomous work between scenarios), so the SECOND probe
# intermittently queued and timed out — a load-only cascade flake that passed
# in isolation and in the restart chain. wait_for_attachment polls the
# authoritative JSONL surface and is immune to pane-render contention. The
# wait_for_jsonl_match above already confirmed the post-restart attachment
# landed; both needles appear ONLY in the post-restart attachment, so no
# floor_ts is needed.
if wait_for_attachment "$KAFM6_S1_WT" "SessionStart" "🛑 ACTION REQUIRED" 60 >/dev/null; then
  emit_pass "$SID" "post-restart-loud-preamble"
else
  emit_fail "$SID" "post-restart-loud-preamble" \
    "post-restart SessionStart attachment containing the loud preamble" \
    "(not found in JSONL within 60s)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

if wait_for_attachment "$KAFM6_S1_WT" "SessionStart" "# Previous Session Context" 60 >/dev/null; then
  emit_pass "$SID" "post-restart-previous-context"
else
  emit_fail "$SID" "post-restart-previous-context" \
    "post-restart SessionStart attachment containing \"# Previous Session Context\"" \
    "(not found in JSONL within 60s)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

return 0
}  # _run_scenario_75

_run_scenario_75
