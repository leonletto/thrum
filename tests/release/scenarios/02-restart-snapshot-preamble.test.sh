#!/usr/bin/env bash
# Scenario: restart-snapshot-preamble
#
# What this verifies: when an agent saves a restart snapshot then its session
# is restarted, the next SessionStart hook injects a loud action-required
# preamble pointing at the Resume Plan inside the previous-session snapshot.
#
# Why it matters: without the preamble, agents read the snapshot section as
# background reading and skip the actionable Resume Plan steps.
#
# Fixture mutation: writes a snapshot in IMPL repo, restarts IMPL pane.
# Coord pane is left untouched (stable driver surface).

SID="02-restart-snapshot-preamble"
PANE="$IMPL_PANE"
REPO="$IMPL_REPO"

# Step 1: have the implementer save a restart snapshot
send_command "$PANE" "! thrum tmux snapshot save --reason 'release-test 02 snapshot precondition'"
# Wait for the .md to appear on disk
elapsed=0
while [ ! -f "$REPO/.thrum/restart/test_implementer.md" ] && [ "$elapsed" -lt 30 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ ! -f "$REPO/.thrum/restart/test_implementer.md" ]; then
  emit_fail "$SID" "snapshot-precondition" "snapshot file at $REPO/.thrum/restart/test_implementer.md" "(file not present after 30s)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: restart the IMPL pane (fires a fresh SessionStart with the snapshot
# embedded in the briefing). Driver-side thrum calls must wrap through
# tmux-exec to break the PID chain back to the parent runtime — otherwise
# the call is attributed to the parent and routed to the wrong daemon, and
# `thrum tmux restart impl` becomes a no-op (no impl session known to that
# daemon).
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$REPO" --clean -- \
  thrum tmux restart "$IMPL_PANE" --force >/dev/null

# Step 3: wait for the NEW SessionStart attachment to appear in IMPL JSONL.
#
# Race-condition note: right after `thrum tmux restart`, the OLD JSONL still
# exists with its OLD SessionStart entries. Until the new claude process
# creates its new JSONL file, `wait_for_session_start` would match the stale
# SessionStart from the old file. The 5-second sleep gives claude time to
# create its new JSONL before polling starts. Conservative but reliable; if
# performance matters later, replace with: snapshot the project dir's *.jsonl
# listing pre-restart, poll until a NEW filename appears, THEN call
# wait_for_session_start.
sleep 5
if ! wait_for_session_start "$REPO" 60; then
  emit_fail "$SID" "restart-session-start" "new SessionStart attachment within 60s" "(none observed)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# A bare SessionStart attachment can land in JSONL before claude flushes
# all the per-hook attachments (the inject-prime-context.sh hook output is
# typically the second of three startup attachments). Asserting against
# the loud preamble before that specific attachment lands produces a
# false-negative FAILED entry that the rest of the run can never recover
# from. Wait specifically for an attachment whose stdout (or content)
# contains the post-restart loud-preamble marker before continuing.
if ! wait_for_jsonl_match "$REPO" \
  '.attachment.hookEvent == "SessionStart" and (((.attachment.stdout // "" | tostring) | contains("ACTION REQUIRED")) or ((.attachment.content // "" | tostring) | contains("ACTION REQUIRED")))' \
  60 >/dev/null; then
  emit_fail "$SID" "restart-loud-preamble-attachment" "SessionStart attachment containing the loud preamble within 60s" "(none observed)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Post-restart, claude auto-runs `/thrum:prime` and renders a multi-line
# response (briefing already loaded via the SessionStart hook, status
# summary, etc). send_command's built-in 10s pane-idle gate can return on
# timeout before that render fully settles, causing the FIRST `!` keystroke
# to land mid-render and be typed as a literal char instead of triggering
# bash-prefix mode. Wait for the longer post-restart settle explicitly here.
wait_for_pane_idle "$PANE" 30

# Step 4: three assertions on the new SessionStart attachment.
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh loud_preamble \"🛑 ACTION REQUIRED\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "loud-preamble" "VERIFIED loud_preamble" \
  "scenarios/${SID}.test.sh:$LINENO"

send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh section_heading \"# Previous Session Context\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "section-heading" "VERIFIED section_heading" \
  "scenarios/${SID}.test.sh:$LINENO"

send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh resume_plan \"## Resume Plan\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "resume-plan" "VERIFIED resume_plan" \
  "scenarios/${SID}.test.sh:$LINENO"
