#!/usr/bin/env bash
# Scenario: 01-session-start-injection — read-only assertions against the coord
# pane's SessionStart attachment captured during run_setup. Verifies that the
# SessionStart hook auto-injects the thrum prime briefing (commits ec6297a9c9 /
# ffc84f952a / 375463b4b2) AND that the old "Run /thrum:prime to load" nudge
# is gone. Regression here means agents boot unprimed.
# Sourced by tests/release/run.sh; not standalone (fixture must be up).

SID="01-session-start-injection"
PANE="$COORD_PANE"
REPO="$COORD_REPO"

# Assertion 1: briefing header present
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh briefing_header \"# Thrum Session Briefing (auto-loaded)\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "briefing-header" "VERIFIED briefing_header" \
  "scenarios/${SID}.test.sh:$LINENO"

# Assertion 2: agent identity present
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh agent_identity \"Agent: @\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "agent-identity" "VERIFIED agent_identity" \
  "scenarios/${SID}.test.sh:$LINENO"

# Assertion 3: old echo-nudge absent (we expect FAILED here — that's a PASS for us)
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh old_nudge_absent \"Run /thrum:prime to load\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "old-nudge-absent" "FAILED old_nudge_absent" \
  "scenarios/${SID}.test.sh:$LINENO"
