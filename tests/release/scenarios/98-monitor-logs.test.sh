#!/usr/bin/env bash
# Scenario: monitor-logs (migrates full_test_plan.md § 10F.8)
#
# Pins the daemon-side `thrum monitor logs <id>` history-lookup
# contract: matches that have been delivered are queryable after the
# fact, the result is valid JSON under --json, and --limit caps the
# returned set.
#
# Backed by HandleLogs (internal/daemon/rpc/monitor.go:377-) which
# queries the messages table on agent_id="monitor:<name>" — i.e. the
# same caller-id stamp scenario 95 already pinned. The match scenario
# 95 delivered ("ALERT: ... kafm11-deliver-marker-${RUNID}") should
# therefore be retrievable here, even after the stop+restart cycle in
# 96/97 (the restart preserves the monitor ID and therefore the
# caller-id key the messages table indexes on).
#
# Three assertions:
#   1. logs --json returns a valid JSON array (no panic, no error).
#   2. The returned array contains an entry whose body matches the
#      marker scenario 95 delivered, proving the history projection
#      survived stop+restart.
#   3. logs --limit 1 --json returns at most 1 entry (cap honored).
#
# Driven via capture_thrum_json out-of-pane.
#
# Depends on scenarios 92 + 95 + 97. Scenario 95 is the source of
# the historical match the body-content assertion looks for.

SID="98-monitor-logs"

if [ -z "${KAFM11_MON_NAME:-}" ] || [ -z "${KAFM11_MON_ID:-}" ] || [ -z "${RUNID:-}" ]; then
  emit_fail "$SID" "shared-fixture-exports-present" \
    "KAFM11_MON_NAME + KAFM11_MON_ID + RUNID set" \
    "(missing — scenario 92/95 likely failed before exports)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

ALERT_MARKER="kafm11-deliver-marker-${RUNID}"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

# Note: `monitor logs` ignores `--json` (the persistent flag is
# defined globally but the RunE at cmd/thrum/main.go:2183-2190
# routes straight through cli.MonitorLogs which always emits the
# `<RFC3339>  <content>` text shape per internal/cli/monitor.go:251-254).
# So we exercise the text-pretty-print path: capture stdout, grep
# for the marker, count lines for the --limit cap. The daemon-side
# JSON projection is covered by internal/daemon/rpc/monitor_test.go.

# Assertion 1: logs returns non-empty output (proves the RPC + the
# CLI render path didn't error).
logs_file="$(mktemp -t kafm11-98.XXXXXX).out"
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "env THRUM_NAME=test_coordinator_main thrum monitor logs $(printf %q "$KAFM11_MON_ID") \
    > $(printf %q "$logs_file") 2>&1" \
  >/dev/null 2>&1 || true

if [ -s "$logs_file" ]; then
  emit_pass "$SID" "logs-returns-output"
else
  emit_fail "$SID" "logs-returns-output" \
    "monitor logs stdout non-empty" \
    "(empty)" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$logs_file"
  return 0
fi

# Assertion 2: the historical delivery from scenario 95 is present.
# Each rendered match is a single line `<RFC3339>  <content>`; grep
# the RUNID-anchored marker. Pins the stop+restart-survives-history
# contract end-to-end — same caller-id key (monitor:<name>) persists
# across the row's lifecycle through 96/97.
if grep -qF "$ALERT_MARKER" "$logs_file" 2>/dev/null; then
  emit_pass "$SID" "logs-include-prior-delivery"
else
  emit_fail "$SID" "logs-include-prior-delivery" \
    "logs output contains '${ALERT_MARKER}' (delivered in scenario 95)" \
    "raw: $(printf '%s' "$(cat "$logs_file" 2>/dev/null)" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$logs_file"

# Assertion 3: --limit caps the result set. Use --limit 1 to make
# the cap unambiguous: regardless of how many historical matches
# exist, the rendered output must contain ≤1 line (each match is
# one line per internal/cli/monitor.go:251-254). Counting lines
# rather than matching a specific entry sidesteps the ordering
# question (most-recent-first sort, oldest-first render).
limit_file="$(mktemp -t kafm11-98-limit.XXXXXX).out"
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "env THRUM_NAME=test_coordinator_main thrum monitor logs $(printf %q "$KAFM11_MON_ID") --limit 1 \
    > $(printf %q "$limit_file") 2>&1" \
  >/dev/null 2>&1 || true

limited_lines=0
if [ -s "$limit_file" ]; then
  limited_lines=$(grep -cE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T' "$limit_file" 2>/dev/null || true)
fi
case "${limited_lines:-0}" in
  ''|*[!0-9]*) limited_lines=0 ;;
esac

if [ "$limited_lines" -le 1 ]; then
  emit_pass "$SID" "logs-limit-flag-honored"
else
  emit_fail "$SID" "logs-limit-flag-honored" \
    "monitor logs --limit 1 stdout has ≤1 timestamp-prefixed line" \
    "got ${limited_lines} lines (raw: $(printf '%s' "$(cat "$limit_file" 2>/dev/null)" | tr '\n' ' ' | head -c 240))" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$limit_file"
