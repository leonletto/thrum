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

# Assertion 6: size-aware directive (thrum-xupf + thrum-a6sw). The
# inject-prime-context.sh hook chooses between two directive variants
# at emit time based on the assembled body's byte count:
#   - small body (< 1500 bytes): "Context auto-loaded by SessionStart
#     hook ... Do NOT run \`/thrum:prime\`" (xupf+2qe2 phrasing).
#   - large body (>= 1500 bytes): "🛑 BRIEFING TRUNCATED — YOU MUST
#     READ THE PERSISTED FILE 🛑 ... Do NOT run \`thrum prime\`
#     manually" (a6sw — turns Claude Code's persisted-output
#     truncation into a forcing function instead of a silent loss).
#
# Both variants share the literal token `Do NOT run` (capital D + the
# space). The briefing envelope's prose at step 4 uses lowercase
# `do NOT need to run` — distinct from the directive's `Do NOT run`,
# so this needle remains regression-proof against a refactor that
# silently dropped the blockquote while leaving the envelope text
# behind. The fixture's coord briefing size determines which variant
# fires; the common needle assertion passes either way.
send_command "$PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh autoload_directive \"Do NOT run\" SessionStart:startup"
assert_jsonl "$PANE" "$REPO" "$SID" "autoload-directive-present" "VERIFIED autoload_directive" \
  "scenarios/${SID}.test.sh:$LINENO"
