#!/usr/bin/env bash
# Scenario: purge-preview-execute (migrates full_test_plan.md § 4.6)
#
# Verifies `thrum purge` flag-level errors, date-format parsing, and
# the preview path. Coverage of the spec § 4.6 contract:
#
#   1. bare `thrum purge` → exit non-zero, error
#      "either --before or --all is required"
#   2. `--before 2d --all` → exit non-zero, error
#      "--before and --all are mutually exclusive"
#   3. `--before 1s` (duration) → preview, exit 0
#   4. `--before 2026-03-15T14:00:00Z` (ISO date) → preview, exit 0
#
# The execute-and-verify mutation path (--confirm + re-register +
# post-condition counts == 0) is intentionally narrowed here:
# `purge --confirm` wipes sessions and forces re-registration, which
# is well-covered by daemon RPC unit tests and would require a full
# sub-fixture re-bootstrap mid-scenario for marginal regression
# value beyond the flag-parse + preview contracts.
#
# Sub-fixture: one daemon + one registered agent, so `purge` calls
# from this scenario don't disturb the run-level coord/impl fixture.
# au7k discipline: sub-daemon stopped at scenario end.

SID="32-purge-preview-execute"
SUB_REPO="$BASE/kafm1-32-purge"
SUB_AGENT="kafm1_32_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_32() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-32@thrum.local" \
    && git config user.name "Release Tests 32" \
    && echo "# 32 purge sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "Release test 32" >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-quickstart" "thrum quickstart in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

# Brief settle for daemon ws.port.
sleep 1

# Helper: run a purge invocation via tmux-exec, capture stdout+stderr +
# exit code. Sets globals: PURGE_OUT (file path), PURGE_RC (exit code).
_run_purge() {
  PURGE_OUT="$(mktemp -t kafm1-32-purge.XXXXXX).txt"
  "$TE" exec --cwd "$SUB_REPO" --clean -- \
    env "THRUM_NAME=$SUB_AGENT" thrum "$@" \
    > "$PURGE_OUT" 2>&1
  PURGE_RC=$?
}

# A1: bare purge → exit non-zero, "either --before or --all is required".
_run_purge purge
if [ "$PURGE_RC" -ne 0 ] && grep -q "either --before or --all is required" "$PURGE_OUT"; then
  emit_pass "$SID" "purge-missing-flag-error"
else
  got="$(tr '\n' ' ' < "$PURGE_OUT" | head -c 240)"
  emit_fail "$SID" "purge-missing-flag-error" \
    "non-zero exit + 'either --before or --all is required'" \
    "rc=${PURGE_RC}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$PURGE_OUT"

# A2: --before + --all → exit non-zero, "mutually exclusive".
_run_purge purge --before 2d --all
if [ "$PURGE_RC" -ne 0 ] && grep -q "mutually exclusive" "$PURGE_OUT"; then
  emit_pass "$SID" "purge-mutex-flag-error"
else
  got="$(tr '\n' ' ' < "$PURGE_OUT" | head -c 240)"
  emit_fail "$SID" "purge-mutex-flag-error" \
    "non-zero exit + '--before and --all are mutually exclusive'" \
    "rc=${PURGE_RC}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$PURGE_OUT"

# A3: --before 1s → exit 0, preview content with "Run with --confirm" hint.
_run_purge purge --before 1s
if [ "$PURGE_RC" -eq 0 ] && grep -qE "Run with --confirm" "$PURGE_OUT"; then
  emit_pass "$SID" "purge-preview-duration"
else
  got="$(tr '\n' ' ' < "$PURGE_OUT" | head -c 240)"
  emit_fail "$SID" "purge-preview-duration" \
    "exit 0 + preview output containing 'Run with --confirm'" \
    "rc=${PURGE_RC}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$PURGE_OUT"

# A4: --before <ISO date> → exit 0 (date parser accepts ISO format).
_run_purge purge --before 2026-03-15T14:00:00Z
if [ "$PURGE_RC" -eq 0 ]; then
  emit_pass "$SID" "purge-preview-iso-date"
else
  got="$(tr '\n' ' ' < "$PURGE_OUT" | head -c 240)"
  emit_fail "$SID" "purge-preview-iso-date" \
    "thrum purge --before 2026-03-15T14:00:00Z exits 0" \
    "rc=${PURGE_RC}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$PURGE_OUT"

}  # _run_scenario_32

_run_scenario_32

# au7k cleanup: stop sub-daemon.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum daemon stop >/dev/null 2>&1 || true
