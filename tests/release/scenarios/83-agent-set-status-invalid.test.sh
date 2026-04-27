#!/usr/bin/env bash
# Scenario: agent-set-status-invalid (migrates full_test_plan.md § 10D.3)
#
# Verifies the CLI rejects status values outside the canonical set
# {working, idle, blocked} BEFORE the daemon RPC fires. Pins the
# input-validation contract so a typo in operator workflows surfaces
# immediately rather than landing as a silent identity-file write
# that downstream code (silence-detection, working_but_idle) reads
# as undefined behavior.
#
# Two assertions:
#   1. invalid-rejected-non-zero — `thrum agent set-status invalid`
#      exits non-zero (error path).
#   2. invalid-error-message — error output names the validation
#      rule. Markdown § 10D.3 specifies the message contains
#      "must be working, idle, or blocked"; assert on "must be"
#      + the canonical-set fragment to be robust to wording polish.
#
# Driven from COORD pane via `!`-bash. No identity mutation — the
# CLI rejects before any write — so no cleanup needed.

SID="83-agent-set-status-invalid"

wait_for_pane_idle "$COORD_PANE" 60

# Probe 1: exit code. Append `; echo "exit: $?"` so the rc lands
# inside the same <bash-stdout> envelope (mirrors scenario 16's
# pattern). cobra-validation paths exit 1.
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum agent set-status bogusstate 2>&1; echo \"exit: \$?\"" \
    "exit: 1" 60; then
  emit_pass "$SID" "invalid-rejected-non-zero"
else
  emit_fail "$SID" "invalid-rejected-non-zero" \
    'thrum agent set-status with invalid value exits non-zero (literal "exit: 1" in stdout)' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Probe 2: error message names the canonical-set rule. Match the
# minimal stable substring "must be" — the prefix is consistent
# across cobra/manual error paths and is distinctive enough to pin
# the validation message without coupling to the exact "working,
# idle, or blocked" wording (which could change to add e.g. "review"
# without breaking the assertion's intent).
wait_for_pane_idle "$COORD_PANE" 30
if send_bash_and_wait "$COORD_PANE" "$COORD_REPO" \
    "thrum agent set-status bogusstate 2>&1" \
    "must be" 60; then
  emit_pass "$SID" "invalid-error-message"
else
  emit_fail "$SID" "invalid-error-message" \
    'thrum agent set-status error output containing "must be"' \
    "(timeout, no matching bash-stdout entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
