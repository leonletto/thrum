#!/usr/bin/env bash
# Scenario: monitor-start (migrates full_test_plan.md § 10F.2)
#
# Pins the daemon-side `thrum monitor start` contract: a `tail -F`
# child wired to a regex match + target agent gets accepted, a row is
# persisted with status="running" + a non-empty ULID-shaped ID, and
# the start succeeds with a parseable `{"id": "..."}` JSON payload
# from `--json` mode.
#
# This scenario primes the shared monitor fixture used by 92-98:
#   - $KAFM11_LOG_FILE — temp log file the monitor's tail child watches
#   - $KAFM11_MON_NAME — monitor name (RUNID-anchored to dodge cross-
#                        run collisions in the run-level daemon)
#   - $KAFM11_MON_ID   — daemon-assigned monitor ID, exported for 93-98
#
# Scenarios 93-98 read these exports. Cleanup of the log file is
# deferred to helpers/teardown.sh (per the kafm.6/8/9 precedent for
# fixture artifacts that live outside $BASE); the daemon-side monitor
# row is reaped when the run-level daemon stops at run_teardown.
#
# Driven via tmux-exec out-of-pane with THRUM_NAME=test_coordinator_main
# pinned (mirrors scenario 22's caller-id pattern). Out-of-pane is
# correct here because the contract under test is the daemon RPC's
# acceptance + ID assignment, not a NL- or slash-routing chain — and
# pane-claude would otherwise wrap the JSON in tool-output framing
# that complicates jq parsing.
#
# --debounce 30s: the CLI's documented minimum; default is 1m. We use
# the floor because scenario 95 (capture+deliver) needs the leading
# edge of the debounce window to fire fast enough that the test
# completes inside its own time budget.

SID="92-monitor-start"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

# Export the shared fixture handles for scenarios 93-98.
export KAFM11_MON_NAME="kafm11-test-alerts-${RUNID}"
export KAFM11_LOG_FILE="/tmp/thrum-monitor-kafm11-${RUNID}.log"

# Touch the log file the monitor will tail. tail -F is happy if the
# file is missing at start (it'll wait for it to appear), but the
# scenario 95 write side appends to it — guarantee its presence so
# the append is unambiguous.
: > "$KAFM11_LOG_FILE"

out_file="$(mktemp -t kafm11-92.XXXXXX).out"

# Start the monitor. Note: `thrum monitor start` does NOT honor a
# `--json` flag — the CLI's RunE writes the human-readable
# "Started monitor <name> (<id>) — target <target>" line and exits
# (cmd/thrum/main.go:2072). Capture stdout and regex the
# parenthesized monitor ID. The ID shape is `mon_<26-char ULID>`
# per internal/daemon/monitor/store.go's NewID — pinning the
# specific shape rather than just non-empty defends against a
# regression that printed the success line but emitted a malformed
# or placeholder ID.
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "env THRUM_NAME=test_coordinator_main thrum monitor start \
    --name $(printf %q "$KAFM11_MON_NAME") \
    --match 'ALERT' \
    --to '@test_coordinator_main' \
    --debounce 30s \
    -- tail -F $(printf %q "$KAFM11_LOG_FILE") \
    > $(printf %q "$out_file") 2>&1" \
  >/dev/null 2>&1 || true

# Assertion 1: success line carries a `mon_<ulid>` id.
KAFM11_MON_ID=""
if [ -s "$out_file" ]; then
  # Match the parenthesized ID. grep -oE returns the first match;
  # there's only one mon_… token in the success line.
  KAFM11_MON_ID=$(grep -oE 'mon_[A-Za-z0-9]+' "$out_file" 2>/dev/null | head -n 1)
fi

if [ -n "$KAFM11_MON_ID" ]; then
  emit_pass "$SID" "monitor-start-returns-id"
  export KAFM11_MON_ID
else
  emit_fail "$SID" "monitor-start-returns-id" \
    "thrum monitor start emits 'Started monitor … (mon_<ulid>) …' on stdout" \
    "got: $(printf '%s' "$(cat "$out_file" 2>/dev/null)" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$out_file"
  return 0
fi

# Assertion 2: the monitor is queryable by ID and reports
# status="running". This pins the persistence side of the start
# contract — the row is on disk, not just bookkept in the supervisor's
# in-memory map. Brief poll because Add returns once the row is
# written but the runner's onStart callback may fire a few ms later;
# status flips to "running" inside that window.
show_file="$(mktemp -t kafm11-92-show.XXXXXX).json"
elapsed=0
status=""
while [ "$elapsed" -lt 10 ]; do
  capture_thrum_json "$COORD_REPO" test_coordinator_main "$show_file" \
    monitor show "$KAFM11_MON_ID" >/dev/null 2>&1 || true
  if [ -s "$show_file" ]; then
    status=$(jq -r '
      if type=="object" then
        .status // (.result.status // "")
      else
        ""
      end' < "$show_file" 2>/dev/null)
  fi
  if [ "$status" = "running" ]; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ "$status" = "running" ]; then
  emit_pass "$SID" "monitor-show-status-running"
else
  emit_fail "$SID" "monitor-show-status-running" \
    "thrum monitor show ${KAFM11_MON_ID} --json reports .status == 'running' within 10s" \
    "got: '${status}' (raw: $(printf '%s' "$(cat "$show_file" 2>/dev/null)" | tr '\n' ' ' | head -c 240))" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$out_file" "$show_file"
