#!/usr/bin/env bash
# Scenario: monitor-restart (migrates full_test_plan.md § 10F.7)
#
# Pins the daemon-side restart contract: `thrum monitor restart <id>`
# re-launches the persisted spec WHILE PRESERVING THE MONITOR ID
# (per supervisor.Restart's design — internal/daemon/monitor/
# supervisor.go:310-358). Distinct from a stop+add cycle which would
# allocate a fresh ID. The ID-preservation contract matters because
# downstream subscribers (HandleLogs's queries, scope filters keyed on
# {type:"monitor", value:<name>}) are stable across restart.
#
# Three assertions:
#   1. Restart RPC returns the SAME ID we passed in (--json payload).
#   2. After restart, monitor list (running-only) shows the row again.
#   3. The restored row's status is "running".
#
# Driven via capture_thrum_json out-of-pane. Read-only-after-restart;
# the restart call itself is the only mutation.
#
# Depends on scenarios 92 + 96 (96 stopped the monitor; this scenario
# brings it back to running).

SID="97-monitor-restart"

if [ -z "${KAFM11_MON_NAME:-}" ] || [ -z "${KAFM11_MON_ID:-}" ]; then
  emit_fail "$SID" "shared-fixture-exports-present" \
    "KAFM11_MON_NAME + KAFM11_MON_ID exported by scenario 92" \
    "(missing — scenario 92 likely failed before the export)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
restart_file="$(mktemp -t kafm11-97.XXXXXX).out"

# Assertion 1: restart returns the SAME id. The CLI's RunE prints
# "Restarted — ID: <id>" and ignores `--json` (same handler shape
# as `monitor stop` — see scenario 96 header for context). Parse
# the human stdout. The id-preservation contract pinned here is
# the end-to-end CLI shape; the daemon-internal MonitorStartResult
# round-trip is covered by the supervisor unit tests.
"$TE" exec --cwd "$COORD_REPO" --clean -- bash -c \
  "env THRUM_NAME=test_coordinator_main thrum monitor restart $(printf %q "$KAFM11_MON_ID") \
    > $(printf %q "$restart_file") 2>&1" \
  >/dev/null 2>&1 || true

restarted_id=""
if [ -s "$restart_file" ]; then
  restarted_id=$(grep -oE 'mon_[A-Za-z0-9]+' "$restart_file" 2>/dev/null | head -n 1)
fi

if [ "$restarted_id" = "$KAFM11_MON_ID" ]; then
  emit_pass "$SID" "restart-preserves-id"
else
  emit_fail "$SID" "restart-preserves-id" \
    "monitor restart stdout 'Restarted — ID: <id>' carries id=${KAFM11_MON_ID}" \
    "got: '${restarted_id}' (raw: $(printf '%s' "$(cat "$restart_file" 2>/dev/null)" | tr '\n' ' ' | head -c 240))" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$restart_file"
  return 0
fi
rm -f "$restart_file"

# Assertions 2 + 3: row is back in default (running-only) list with
# status="running". Brief poll: Restart's launch path is async (the
# DB update happens before the new runner's onStart fires).
list_file="$(mktemp -t kafm11-97-list.XXXXXX).json"
elapsed=0
listed_id=""
listed_status=""
while [ "$elapsed" -lt 10 ]; do
  capture_thrum_json "$COORD_REPO" test_coordinator_main "$list_file" \
    monitor list >/dev/null 2>&1 || true
  if [ -s "$list_file" ]; then
    listed_id=""; listed_status=""
    read -r listed_id listed_status < <(
      jq -r --arg id "$KAFM11_MON_ID" '
        (if type=="array" then . else .result // [] end)
        | map(select(.id == $id))
        | [(.[0].id // ""), (.[0].status // "")]
        | @tsv' < "$list_file" 2>/dev/null
    ) || true
  fi
  if [ "$listed_status" = "running" ]; then
    break
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done

if [ "$listed_id" = "$KAFM11_MON_ID" ]; then
  emit_pass "$SID" "running-list-includes-restored"
else
  emit_fail "$SID" "running-list-includes-restored" \
    "monitor list contains id=${KAFM11_MON_ID} after restart" \
    "got id='${listed_id}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

if [ "$listed_status" = "running" ]; then
  emit_pass "$SID" "restored-row-status-running"
else
  emit_fail "$SID" "restored-row-status-running" \
    "restored row .status == 'running' within 10s" \
    "got: '${listed_status}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$list_file"
