#!/usr/bin/env bash
# Scenario: slash-inbox-routing (migrates full_test_plan.md § 7.6)
#
# Verifies that /thrum:inbox is recognized and routed by Claude Code's
# slash parser into the agent's JSONL as a user message containing
# `<command-name>/thrum:inbox</command-name>`.
#
# Same routing-only rationale as scenarios 20, 54, and 57: skill-body
# execution (which under the slash body would render claude's
# inbox-summary prose) couples to model eagerness; the routing tag is
# the deterministic slash-registration anchor.
#
# Pre-condition: the spec § 7.6 first sends a marker message from impl
# to coord so the inbox-render path has something to display. We
# preserve that intent by sending a marker via tmux-exec out-of-pane
# (PID-chain break) before the slash send. The assertion only depends
# on the routing tag landing — not on whether the marker shows up in
# claude's prose response — but the precondition matters for
# regression coverage of "inbox slash with non-empty inbox" (an empty
# inbox still routes, but a routing regression that only affected the
# non-empty path would slip past).
#
# Driven against COORD pane (matches markdown § 7.6 subject).

SID="55-slash-inbox-routing"
PANE="$COORD_PANE"
REPO="$COORD_REPO"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
INBOX_MARKER="kafm3-55-inbox-${RUNID}"

_run_scenario_55() {

# Pre-seed: out-of-pane send from impl→coord, so coord's inbox isn't
# empty when /thrum:inbox runs. THRUM_NAME pinned (impl identity).
"$TE" exec --cwd "$IMPL_REPO" --clean -- \
  env THRUM_NAME=test_implementer thrum send \
    "Inbox slash precondition (${INBOX_MARKER})" \
    --to @test_coordinator_main >/dev/null 2>&1 || true

wait_for_pane_idle "$PANE" 60

local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

send_slash_command "$PANE" "/thrum:inbox"

local filter='.type == "user"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | tostring | contains("<command-name>/thrum:inbox</command-name>"))'

if wait_for_jsonl_match "$REPO" "$filter" 60 >/dev/null; then
  emit_pass "$SID" "slash-inbox-registered"
else
  emit_fail "$SID" "slash-inbox-registered" \
    'user message containing "<command-name>/thrum:inbox</command-name>" within 60s after slash send' \
    "(no matching JSONL entry — slash command did not register)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_55

_run_scenario_55
