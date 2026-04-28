#!/usr/bin/env bash
# Scenario: 01-session-start-injection — read-only assertions against the coord
# pane's SessionStart attachment captured during run_setup. Verifies that the
# SessionStart hook auto-injects the thrum prime briefing (commits ec6297a9c9 /
# ffc84f952a / 375463b4b2) AND that the old "Run /thrum:prime to load" nudge
# is gone. Regression here means agents boot unprimed.
#
# Also pins the thrum-2qe2 identity banner + thrum-xupf loud
# auto-load directive that ride at the top of the injected text
# above the briefing envelope. Both must be present for a registered
# agent — the banner so the agent (and any human watching the pane)
# sees identity at a glance, the directive so the agent doesn't
# reflexively re-run /thrum:prime when the briefing is already in
# context.
#
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

# Assertion 4: identity banner header (thrum-2qe2). The header text
# `# 🎯 You are: @<agent_id>` is emitted at the very top of the
# injected text by inject-prime-context.sh. Search for the
# `🎯 You are:` substring (without the agent_id, so the assertion
# stays portable across runs whose coord agent_id may shift).
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh identity_banner \"You are: @\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "identity-banner-header" "VERIFIED identity_banner" \
  "scenarios/${SID}.test.sh:$LINENO"

# Assertion 5: identity banner role field (thrum-2qe2). The fixture's
# coord agent registers as role=coordinator; the banner renders it as
# `**Role:** coordinator`. Pinning the formatted bullet defends
# against a regression that emitted the field name without the value
# or vice versa.
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh identity_banner_role \"**Role:** coordinator\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "identity-banner-role" "VERIFIED identity_banner_role" \
  "scenarios/${SID}.test.sh:$LINENO"

# Assertion 6: loud auto-load directive (thrum-xupf). The directive
# blockquote tells the agent NOT to re-run /thrum:prime. The
# substring `Do NOT run` followed by the inline-code marker for
# /thrum:prime is the load-bearing phrasing that defends against a
# regression that softened the directive back into a paragraph.
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh autoload_directive \"Do NOT run\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "autoload-directive-present" "VERIFIED autoload_directive" \
  "scenarios/${SID}.test.sh:$LINENO"
