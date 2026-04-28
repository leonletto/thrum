#!/usr/bin/env bash
# Scenario: monitor-show (migrates full_test_plan.md § 10F.4)
#
# Pins the daemon-side `thrum monitor show <id> --json` projection:
# the per-monitor read returns the full spec the start RPC accepted —
# name, regex pattern, target, and status — addressable by the same
# ID list returned. Distinct from scenario 93's list-side projection;
# show pulls a single row by ID and exercises the daemon's GetByID
# path (vs. List's full enumerate).
#
# Read-only, no fixture mutation. JSON shape: monitor.show returns
# MonitorJobView (object) per internal/cli/monitor.go:184. EmitJSON
# may graft a `hints` key into the object when slog records fired,
# but the spec fields stay at top-level (object bodies are
# unmarshal+graft+remarshal — fields preserved).
#
# Depends on scenario 92.

SID="94-monitor-show"

if [ -z "${KAFM11_MON_NAME:-}" ] || [ -z "${KAFM11_MON_ID:-}" ]; then
  emit_fail "$SID" "shared-fixture-exports-present" \
    "KAFM11_MON_NAME + KAFM11_MON_ID exported by scenario 92" \
    "(missing — scenario 92 likely failed before the export)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

out_file="$(mktemp -t kafm11-94.XXXXXX).json"

capture_thrum_json "$COORD_REPO" test_coordinator_main "$out_file" \
  monitor show "$KAFM11_MON_ID" >/dev/null 2>&1 || true

if [ ! -s "$out_file" ]; then
  emit_fail "$SID" "show-returns-payload" \
    "thrum monitor show ${KAFM11_MON_ID} --json produces non-empty output" \
    "(empty file)" \
    "scenarios/${SID}.test.sh:$LINENO"
  rm -f "$out_file"
  return 0
fi

emit_pass "$SID" "show-returns-payload"

# Single jq pass extracts the four fields we want to pin. Output as
# tab-separated so we can read into shell vars without re-running jq
# four times.
got_name=""; got_match=""; got_target=""; got_status=""
read -r got_name got_match got_target got_status < <(
  jq -r '
    [.name // "", .match // "", .target // "", .status // ""]
    | @tsv' < "$out_file" 2>/dev/null
) || true

if [ "$got_name" = "$KAFM11_MON_NAME" ]; then
  emit_pass "$SID" "show-name-matches"
else
  emit_fail "$SID" "show-name-matches" \
    ".name == '${KAFM11_MON_NAME}'" \
    "got: '${got_name}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Match was passed as the literal string "ALERT" at start time.
# Reading it back round-trips the regex storage layer.
if [ "$got_match" = "ALERT" ]; then
  emit_pass "$SID" "show-match-pattern"
else
  emit_fail "$SID" "show-match-pattern" \
    ".match == 'ALERT'" \
    "got: '${got_match}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

if [ "$got_target" = "@test_coordinator_main" ]; then
  emit_pass "$SID" "show-target-coordinator"
else
  emit_fail "$SID" "show-target-coordinator" \
    ".target == '@test_coordinator_main'" \
    "got: '${got_target}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

if [ "$got_status" = "running" ]; then
  emit_pass "$SID" "show-status-running"
else
  emit_fail "$SID" "show-status-running" \
    ".status == 'running'" \
    "got: '${got_status}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

rm -f "$out_file"
