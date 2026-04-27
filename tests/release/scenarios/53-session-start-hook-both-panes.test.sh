#!/usr/bin/env bash
# Scenario: session-start-hook-both-panes (migrates full_test_plan.md § 7.3)
#
# Verifies the SessionStart hook auto-injection (commits ec6297a9c9 /
# ffc84f952a / 375463b4b2) fired in BOTH coord and impl panes during
# setup, putting the canonical "# Thrum Session Briefing (auto-loaded)"
# header into each pane's SessionStart attachment.
#
# Scenario 01 already covers the coord pane in detail (briefing-header
# + agent-identity + old-nudge-absent). This scenario's job is the
# parity check on impl — the manual plan's § 7.3 doubles up on both
# panes and a regression specific to one pane (e.g. impl's worktree
# redirect breaking the hook lookup) would slip past 01.
#
# We use check-context-value.sh from inside each pane (matching scenario
# 01's pattern) so the JSONL the hook actually attached is what gets
# scanned — not a separate filesystem-side reconstruction.

SID="53-session-start-hook-both-panes"

_run_scenario_53() {

# Coord briefing header (parity with scenario 01, but distinct tag so
# the JSONL search doesn't false-match scenario 01's earlier entry).
send_command "$COORD_PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh briefing_header_53_coord \"# Thrum Session Briefing (auto-loaded)\" SessionStart:startup"
assert_jsonl "$COORD_PANE" "$COORD_REPO" "$SID" "coord-briefing-header" \
  "VERIFIED briefing_header_53_coord" \
  "scenarios/${SID}.test.sh:$LINENO"

# Impl briefing header — the parity assertion this scenario exists for.
send_command "$IMPL_PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh briefing_header_53_impl \"# Thrum Session Briefing (auto-loaded)\" SessionStart:startup"
assert_jsonl "$IMPL_PANE" "$IMPL_REPO" "$SID" "impl-briefing-header" \
  "VERIFIED briefing_header_53_impl" \
  "scenarios/${SID}.test.sh:$LINENO"

# Impl agent identity present in SessionStart (mirrors 01's
# agent-identity sub-assertion but for impl).
send_command "$IMPL_PANE" "! $THRUM_RELEASE_REPO_ROOT/scripts/check-context-value.sh agent_identity_53_impl \"Agent: @\" SessionStart:startup"
assert_jsonl "$IMPL_PANE" "$IMPL_REPO" "$SID" "impl-agent-identity" \
  "VERIFIED agent_identity_53_impl" \
  "scenarios/${SID}.test.sh:$LINENO"

}  # _run_scenario_53

_run_scenario_53
