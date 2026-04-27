#!/usr/bin/env bash
# Scenario: self-restart-preamble
#
# What this verifies: same loud preamble + Resume Plan path as scenario 02,
# but exercised against the COORD pane instead of IMPL. Covers the
# "coordinator pane is the one being restarted" path that hits in real
# sessions when coordinators run out of context first.
#
# Why it matters: scenario 02 only covers the implementer pane. The
# coordinator pane is a more common real-world target for /thrum:restart
# since coordinators hit context limits more often.
#
# Fixture mutation: writes a snapshot in COORD repo, restarts COORD pane.
# Impl pane is left untouched.

SID="03-self-restart-preamble"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

# Step 1: have the coordinator save a restart snapshot
send_command "$PANE" "! thrum tmux snapshot save --reason 'release-test 03 snapshot precondition'"
# Wait for the .md to appear on disk
elapsed=0
while [ ! -f "$REPO/.thrum/restart/test_coordinator_main.md" ] && [ "$elapsed" -lt 30 ]; do
  sleep 1
  elapsed=$((elapsed + 1))
done
if [ ! -f "$REPO/.thrum/restart/test_coordinator_main.md" ]; then
  emit_fail "$SID" "snapshot-precondition" "snapshot file at $REPO/.thrum/restart/test_coordinator_main.md" "(file not present after 30s)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: restart the COORD pane (fires a fresh SessionStart with the snapshot
# embedded in the briefing). Driver-side thrum calls must wrap through
# tmux-exec to break the PID chain back to the parent runtime — otherwise
# the call is attributed to the parent and routed to the wrong daemon, and
# `thrum tmux restart coord` becomes a no-op (no coord session known to that
# daemon).
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$REPO" --clean -- \
  thrum tmux restart coord --force >/dev/null

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
#
# Inter-send pane-idle waits: coord's `thrum prime` output is much larger
# than impl's, so post-`!` claude rendering takes longer than send_command's
# default 10s wait_for_pane_idle. Without an explicit longer settle between
# sends, the next `!` keystroke can land mid-render and miss bash-prefix
# mode → no <bash-stdout> entry → 30s assert_jsonl timeout → flake.
# 30s is empirically sufficient for coord pane.
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh loud_preamble \"🛑 ACTION REQUIRED\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "loud-preamble" "VERIFIED loud_preamble" \
  "scenarios/${SID}.test.sh:$LINENO"
wait_for_pane_idle "$PANE" 60

send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh section_heading \"# Previous Session Context\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "section-heading" "VERIFIED section_heading" \
  "scenarios/${SID}.test.sh:$LINENO"
wait_for_pane_idle "$PANE" 60

send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh resume_plan \"## Resume Plan\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "resume-plan" "VERIFIED resume_plan" \
  "scenarios/${SID}.test.sh:$LINENO"
