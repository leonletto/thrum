#!/usr/bin/env bash
# Scenario: monitor-list (migrates full_test_plan.md § 10F.3)
#
# Pins the daemon-side `thrum monitor list --json` projection: the
# fixture monitor scenario 92 started shows up in the running-only
# default list with the expected (name, status, target) tuple.
#
# Read-only RPC against the run-level daemon's monitor store. No
# fixture mutation. Driven via capture_thrum_json out-of-pane with
# THRUM_NAME pinned.
#
# JSON shape: monitor.list returns []MonitorJobView per
# internal/cli/monitor.go:170. EmitJSON emits the array verbatim
# unless slog hints fire mid-call, in which case it wraps as
# {"result": [...], "hints": [...]}. The `(if type=="array" then .
# else .result // [] end)` guard handles both shapes.
#
# Depends on scenario 92 (KAFM11_MON_NAME, KAFM11_MON_ID exports).

SID="93-monitor-list"

if [ -z "${KAFM11_MON_NAME:-}" ] || [ -z "${KAFM11_MON_ID:-}" ]; then
  emit_fail "$SID" "shared-fixture-exports-present" \
    "KAFM11_MON_NAME + KAFM11_MON_ID exported by scenario 92" \
    "(missing — scenario 92 likely failed before the export)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

out_file="$(mktemp -t kafm11-93.XXXXXX).json"

capture_thrum_json "$COORD_REPO" test_coordinator_main "$out_file" \
  monitor list >/dev/null 2>&1 || true

if [ ! -s "$out_file" ]; then
  emit_fail "$SID" "fixture-monitor-listed" \
    "thrum monitor list --json produces non-empty output" \
    "(empty file — daemon RPC returned nothing)" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$out_file"
  return 0
fi

# Single jq pass extracts the row tuple. Defensive shape unwrap +
# select-by-id so we pin the EXACT row scenario 92 created (not just
# "any row with this name happens to exist", which would false-pass
# if a stale monitor from a prior run shared the name — our
# RUNID-anchored name makes that unlikely but the ID match is
# stronger).
row_json=$(jq -c --arg id "$KAFM11_MON_ID" '
  (if type=="array" then . else .result // [] end)
  | map(select(.id == $id))
  | .[0] // {}' < "$out_file" 2>/dev/null)

if [ -z "$row_json" ] || [ "$row_json" = "{}" ]; then
  emit_fail "$SID" "fixture-monitor-listed" \
    "monitor list --json contains a row with id=${KAFM11_MON_ID}" \
    "got list: $(printf '%s' "$(cat "$out_file" 2>/dev/null)" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$out_file"
  return 0
fi

emit_pass "$SID" "fixture-monitor-listed"

# Read the three row fields we want to pin against — name, status,
# target. Reading them out of $row_json (already filtered to the one
# row) avoids re-iterating the full list.
name=$(printf '%s' "$row_json"   | jq -r '.name   // ""')
status=$(printf '%s' "$row_json" | jq -r '.status // ""')
target=$(printf '%s' "$row_json" | jq -r '.target // ""')

if [ "$name" = "$KAFM11_MON_NAME" ]; then
  emit_pass "$SID" "row-name-matches"
else
  emit_fail "$SID" "row-name-matches" \
    "row .name == '${KAFM11_MON_NAME}'" \
    "got: '${name}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

if [ "$status" = "running" ]; then
  emit_pass "$SID" "row-status-running"
else
  emit_fail "$SID" "row-status-running" \
    "row .status == 'running'" \
    "got: '${status}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Target is rendered with the leading "@" the user passed at start
# time. The daemon does not strip it (per send_payload.Mentions
# round-trip in internal/daemon/monitor/delivery.go:60-62). Pin both
# the @-prefixed and bare forms in the failure detail so a future
# refactor that drops the prefix is debuggable.
if [ "$target" = "@test_coordinator_main" ]; then
  emit_pass "$SID" "row-target-coordinator"
else
  emit_fail "$SID" "row-target-coordinator" \
    "row .target == '@test_coordinator_main'" \
    "got: '${target}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$out_file"
