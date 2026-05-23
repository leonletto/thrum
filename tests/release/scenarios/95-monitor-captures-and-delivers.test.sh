#!/usr/bin/env bash
# Scenario: monitor-captures-and-delivers (migrates full_test_plan.md § 10F.5)
#
# End-to-end output-capture + delivery contract: the running monitor
# from scenario 92 tails a log file, suppresses non-matching lines,
# and emits matching lines as @system messages addressed to its
# target agent (test_coordinator_main). Two assertions:
#
#   1. non-matching line ("INFO: ...") does NOT produce an inbox
#      message containing the line. The regex is "ALERT" so an INFO
#      write should never match; this pins the suppression contract
#      (a regression that delivered every line regardless of regex
#      would fail here).
#   2. matching line ("ALERT: kafm11-deliver-marker-${RUNID}")
#      produces an inbox message in test_coordinator_main's inbox
#      whose body contains the RUNID-anchored marker. This pins the
#      delivery side end-to-end: regex match → debounce gate →
#      Delivery.Deliver → MessageHandler.HandleSend → recipient
#      inbox row.
#
# Marker is RUNID-anchored so the inbox poll cannot false-match
# stale messages from earlier in the run-level coord's inbox (the
# coord agent receives broadcast messages from setup-repo + scenario
# 25's --to @everyone send, etc.).
#
# Driven via capture_thrum_json out-of-pane against COORD_REPO with
# THRUM_NAME=test_coordinator_main pinned. The recipient-side inbox
# read MUST be out-of-pane (not driven from $COORD_PANE) — claude
# in $COORD_PANE will autonomously consume the inbound nudge,
# potentially marking the new message as read between the write side
# and our jq filter (same race scenarios 06-08 work around).
# Out-of-pane reads daemon state directly and is deterministic.
#
# Timing: the monitor was started with --debounce 30s (the documented
# floor). Leading-edge debounce means the first match in a window
# fires immediately, so we wait up to 60s for delivery — comfortably
# under the implicit "interactive" budget while leaving headroom for
# tail's poll interval + the daemon's send pipeline.
#
# Depends on scenario 92.

SID="95-monitor-captures-and-delivers"

if [ -z "${KAFM11_MON_NAME:-}" ] || [ -z "${KAFM11_LOG_FILE:-}" ]; then
  emit_fail "$SID" "shared-fixture-exports-present" \
    "KAFM11_MON_NAME + KAFM11_LOG_FILE exported by scenario 92" \
    "(missing — scenario 92 likely failed before the export)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

if [ ! -f "$KAFM11_LOG_FILE" ]; then
  emit_fail "$SID" "shared-fixture-log-file-present" \
    "log file at ${KAFM11_LOG_FILE}" \
    "(missing — was it cleaned up by an unexpected path?)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

INFO_MARKER="kafm11-info-marker-${RUNID}"
ALERT_MARKER="kafm11-deliver-marker-${RUNID}"

# Drain test_coordinator_main's inbox so subsequent count comparisons
# are clean. Use capture_thrum_json with --json so the helper's
# default `--json` append doesn't break the call (`message read`
# tolerates --json — emits a structured response).
drain_file="$(mktemp -t kafm11-95-drain.XXXXXX).json"
capture_thrum_json "$COORD_REPO" test_coordinator_main "$drain_file" \
  message read --all >/dev/null 2>&1 || true
rm -f "$drain_file"

# --- Negative path: non-matching line ---
# Writing "INFO: ..." should produce zero inbox arrivals carrying
# the INFO marker. Wait 5s for the daemon to do nothing visible
# (tail's poll + the regex evaluator's pass-through).
echo "INFO: ${INFO_MARKER} — should be suppressed" >> "$KAFM11_LOG_FILE"
sleep 5

inbox_neg="$(mktemp -t kafm11-95-neg.XXXXXX).json"
capture_thrum_json "$COORD_REPO" test_coordinator_main "$inbox_neg" \
  inbox >/dev/null 2>&1 || true

neg_count=0
if [ -s "$inbox_neg" ]; then
  neg_count=$(jq -r --arg m "$INFO_MARKER" '
    [.messages[]? | select(.body.content // "" | contains($m))] | length' \
    < "$inbox_neg" 2>/dev/null)
fi
case "${neg_count:-0}" in
  ''|*[!0-9]*) neg_count=0 ;;
esac

if [ "$neg_count" = "0" ]; then
  emit_pass "$SID" "non-matching-line-not-delivered"
else
  emit_fail "$SID" "non-matching-line-not-delivered" \
    "0 inbox messages containing '${INFO_MARKER}' (regex 'ALERT' should suppress)" \
    "got: ${neg_count} matches" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$inbox_neg"

# --- Positive path: matching line ---
# Append the ALERT line to the same tailed log file. The monitor's
# tail child follows the file; the regex evaluator sees "ALERT", the
# leading-edge debounce gate passes (window starts here), and
# Delivery.Deliver routes a synthetic message with caller_agent_id =
# "monitor:<name>" + content = the matched line through the existing
# send pipeline.
echo "ALERT: disk usage at 95% — ${ALERT_MARKER}" >> "$KAFM11_LOG_FILE"

# Poll coord's claude JSONL for two independent witnesses of the
# monitor-delivered message. Pivots away from the prior
# `thrum inbox --json` via tmux-exec approach (which intermittently
# returned hints-only responses under heavy gate load — the
# daemon's RefreshLocalIdentity fails on the tmux-exec pool pane's
# caller-identity, hints accumulate, the inbox RPC is never
# reached, response has no .messages field). The JSONL surface is
# both deterministic (encode_cwd locates the project dir; the
# wait_for_jsonl_match helper handles file rotation via glob) AND
# free of the load-flake mode the inbox-via-tmux-exec path hits.
#
# Two witnesses (one per original assertion):
#
#   1. matching-line-delivered-as-message: the ALERT marker
#      appears in a user-message OR bash-stdout entry in COORD's
#      JSONL. claude autonomously runs `thrum inbox --unread` in
#      response to the inbound nudge (visible in the v0.10.6 RC1
#      pane snapshot at /tmp/thrum-release-failures/reltest-8091/
#      95-monitor-captures-and-delivers--*--COORD_PANE.snap as
#      "Ran 2 shell commands"), so the bash-stdout output of
#      that inbox dump contains the marker if delivery succeeded.
#      Also matches user-message content (e.g. claude
#      paraphrasing the alert in its response stream).
#
#   2. delivered-caller-id-monitor-prefixed: the daemon's
#      inbound-message nudge to claude is formatted as
#      "New message from @<sender> -- run `thrum inbox --unread`
#      to read" (see internal/daemon/notifier or similar), and
#      for monitor-delivered messages the sender is
#      "monitor:<name>" per
#      internal/daemon/monitor/delivery.go:57. This nudge lands
#      as a user-message in claude's JSONL; the substring proves
#      both that the message routed AND that its caller_id
#      carries the monitor: prefix.
expected_caller="monitor:${KAFM11_MON_NAME}"

# Marker witness: claude autonomously runs `thrum inbox --unread` in
# response to the inbound nudge (visible in pane snapshots as "Ran 2
# shell commands"). The full message body — including the
# RUNID-anchored marker — lands in claude's JSONL via the Bash tool's
# tool_result entry (under .message.content[].content for user-type
# entries with tool_result blocks). Verified via direct JSONL
# inspection of v0.10.6 RC1 gate reltest-20861 fixture: the marker
# `kafm11-deliver-marker-<RUNID>` appears verbatim in the
# tool_result content of claude's autonomous Bash call.
#
# Match shape: stringify the entire .message.content (which works
# for both user-message tool_result arrays and assistant text
# arrays) and check for substring. This is broader than
# wait_for_bash_stdout_contains (which is `!`-prefix-specific and
# does NOT match autonomous Bash tool_result entries — the prior
# pivot to it was misaligned with claude's actual transcript shape
# here).
marker_filter='(.message.content // "") | tostring | contains("'"$ALERT_MARKER"'")'
if wait_for_jsonl_match "$COORD_REPO" "$marker_filter" 60 >/dev/null; then
  emit_pass "$SID" "matching-line-delivered-as-message"
else
  emit_fail "$SID" "matching-line-delivered-as-message" \
    "COORD JSONL entry containing '${ALERT_MARKER}' within 60s" \
    "(no matching JSONL entry — monitor may not have delivered)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

nudge_filter='.type == "user"
        and (.message.content | type == "string")
        and (.message.content | contains("New message from @'"$expected_caller"'"))'
if wait_for_jsonl_match "$COORD_REPO" "$nudge_filter" 30 >/dev/null; then
  emit_pass "$SID" "delivered-caller-id-monitor-prefixed"
else
  emit_fail "$SID" "delivered-caller-id-monitor-prefixed" \
    "COORD JSONL nudge containing 'New message from @${expected_caller}' within 30s" \
    "(marker delivered but nudge text didn't carry the monitor: caller_id prefix)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Drain inbox so subsequent scenarios see a clean coord state. The
# scenario 28 read-all sub-assertion runs much earlier in the suite
# and we don't break that, but a future scenario inspecting coord's
# inbox under unread-only filtering shouldn't pick up our marker.
# Use a real tempfile (not /dev/null) — capture_thrum_json's contract
# isn't documented to support /dev/null and a future `[ -s ]` guard
# would break us silently. Mirrors the drain at line 67-69 above.
drain_file2="$(mktemp -t kafm11-95-drain2.XXXXXX).json"
capture_thrum_json "$COORD_REPO" test_coordinator_main "$drain_file2" \
  message read --all >/dev/null 2>&1 || true
rm -f "$drain_file2"
