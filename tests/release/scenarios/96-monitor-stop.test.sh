#!/usr/bin/env bash
# Scenario: monitor-stop (migrates full_test_plan.md § 10F.6)
#
# Pins the daemon-side stop contract:
#   1. `thrum monitor stop <id>` returns success.
#   2. The default `monitor list` (running-only) no longer contains
#      the stopped monitor.
#   3. `monitor list --all` (which retains stopped/dead rows younger
#      than a week) DOES still contain the row, with status="stopped".
#
# The third assertion is load-bearing for scenario 97 — restart
# requires the row to be persisted across stop, not deleted. The
# inline CLI help text on `thrum monitor stop` says "Stop and remove",
# but the supervisor implementation calls store.MarkStopped (not
# store.Delete) — see internal/daemon/monitor/supervisor.go:297.
# Pinning the post-stop visibility under --all defends against a
# regression that flipped to genuine deletion (which would silently
# break restart).
#
# Read-only after the stop call. The daemon stops the tail child as
# part of the stop RPC; no external cleanup needed. Driven via
# capture_thrum_json out-of-pane.
#
# Depends on scenario 92.

SID="96-monitor-stop"

if [ -z "${KAFM11_MON_NAME:-}" ] || [ -z "${KAFM11_MON_ID:-}" ]; then
  emit_fail "$SID" "shared-fixture-exports-present" \
    "KAFM11_MON_NAME + KAFM11_MON_ID exported by scenario 92" \
    "(missing — scenario 92 likely failed before the export)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
stop_file="$(mktemp -t kafm11-96-stop.XXXXXX).out"

# Assertion 1: stop succeeds. The CLI's RunE prints
# "Stopped monitor <id>" on success and ignores `--json` (it's a
# persistent global flag, but `cmd/thrum/main.go`'s monitor stop
# RunE does not check it — same handler shape as restart and
# logs). Parse the human stdout shape directly. The daemon-side
# RPC contract (returns {"status":"stopped"}) is exercised by the
# unit tests in internal/daemon/rpc/monitor_test.go; the
# scenario's value is the end-to-end CLI → RPC → CLI pretty-print
# round-trip.
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "env THRUM_NAME=test_coordinator_main thrum monitor stop $(printf %q "$KAFM11_MON_ID") \
    > $(printf %q "$stop_file") 2>&1" \
  >/dev/null 2>&1 || true

if grep -qE "Stopped monitor ${KAFM11_MON_ID}" "$stop_file" 2>/dev/null; then
  emit_pass "$SID" "stop-success-line"
else
  emit_fail "$SID" "stop-success-line" \
    "monitor stop stdout contains 'Stopped monitor ${KAFM11_MON_ID}'" \
    "got: $(printf '%s' "$(cat "$stop_file" 2>/dev/null)" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$stop_file"

# Assertion 2: default list (running-only) does NOT contain the
# stopped monitor. Brief poll because supervisor.Stop waits for the
# runner to exit (10s timeout in supervisor.go:295) before returning,
# so by stop's success the runner is gone — but the store.MarkStopped
# write happens after the runner exits, and the list filter reads
# from the store.
list_running="$(mktemp -t kafm11-96-running.XXXXXX).json"
capture_thrum_json "$COORD_REPO" test_coordinator_main "$list_running" \
  monitor list >/dev/null 2>&1 || true

still_running=$(jq -c --arg id "$KAFM11_MON_ID" '
  (if type=="array" then . else .result // [] end)
  | map(select(.id == $id))
  | .[0] // null' < "$list_running" 2>/dev/null)

if [ "$still_running" = "null" ] || [ -z "$still_running" ]; then
  emit_pass "$SID" "default-list-omits-stopped"
else
  emit_fail "$SID" "default-list-omits-stopped" \
    "monitor list (running-only) does not contain id=${KAFM11_MON_ID}" \
    "got row: $(printf '%s' "$still_running" | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$list_running"

# Assertion 3: --all list DOES contain the stopped row, with
# status="stopped". This pins the row-persists-across-stop contract
# scenario 97's restart path depends on. Use the helper's args
# straight through — `--all` is a flag, not a positional, so it
# slots in cleanly before the auto-appended `--json`.
list_all="$(mktemp -t kafm11-96-all.XXXXXX).json"
capture_thrum_json "$COORD_REPO" test_coordinator_main "$list_all" \
  monitor list --all >/dev/null 2>&1 || true

all_id=""; all_status=""
read -r all_id all_status < <(
  jq -r --arg id "$KAFM11_MON_ID" '
    (if type=="array" then . else .result // [] end)
    | map(select(.id == $id))
    | [(.[0].id // ""), (.[0].status // "")]
    | @tsv' < "$list_all" 2>/dev/null
) || true

if [ "$all_id" = "$KAFM11_MON_ID" ] && [ "$all_status" = "stopped" ]; then
  emit_pass "$SID" "all-list-shows-stopped-row"
else
  emit_fail "$SID" "all-list-shows-stopped-row" \
    "monitor list --all contains id=${KAFM11_MON_ID} with status='stopped'" \
    "got id='${all_id}', status='${all_status}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$list_all"
