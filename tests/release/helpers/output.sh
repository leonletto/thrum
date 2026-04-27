#!/usr/bin/env bash
# tests/release/helpers/output.sh — streaming pass/fail/skip emitters with
# end-of-run summary block. Format spec: dev-docs/specs/2026-04-26-release-test-framework-design.md § 5

# Globals tracked across a run (sourced once into run.sh's env).
RT_COUNT_PASS=${RT_COUNT_PASS:-0}
RT_COUNT_FAIL=${RT_COUNT_FAIL:-0}
RT_COUNT_SKIP=${RT_COUNT_SKIP:-0}
RT_COUNT_SCENARIOS=${RT_COUNT_SCENARIOS:-0}
RT_FAILURE_LINES=${RT_FAILURE_LINES:-}    # newline-joined "[FAIL] ...\nfile:line" pairs

emit_pass() {
  # emit_pass <scenario-id> <assertion-name>
  local sid="$1" name="$2"
  printf '[PASS] %s / %s\n' "$sid" "$name"
  RT_COUNT_PASS=$((RT_COUNT_PASS + 1))
}

emit_fail() {
  # emit_fail <scenario-id> <assertion-name> <expected> <got> <file:line>
  local sid="$1" name="$2" expected="$3" got="$4" loc="$5"
  printf '[FAIL] %s / %s\n' "$sid" "$name"
  printf '       → expected: %s\n' "$expected"
  printf '       → got:      %s\n' "$got"
  printf '       → file:     %s\n' "$loc"
  RT_COUNT_FAIL=$((RT_COUNT_FAIL + 1))
  RT_FAILURE_LINES+="${RT_FAILURE_LINES:+$'\n'}  [FAIL] $sid / $name"$'\n'"         $loc"
}

emit_skip() {
  # emit_skip <scenario-id> <assertion-name> <reason>
  local sid="$1" name="$2" reason="$3"
  printf '[SKIP] %s / %s\n' "$sid" "$name"
  printf '       → reason: %s\n' "$reason"
  RT_COUNT_SKIP=$((RT_COUNT_SKIP + 1))
}

scenario_start() {
  # scenario_start <scenario-id> <relative-path>
  local sid="$1" path="$2"
  printf '\n==> %s (%s)\n' "$sid" "$path"
  RT_SCENARIO_START_TIME=$(date +%s)
  RT_SCENARIO_START_PASS=$RT_COUNT_PASS
  RT_SCENARIO_START_FAIL=$RT_COUNT_FAIL
  RT_COUNT_SCENARIOS=$((RT_COUNT_SCENARIOS + 1))
}

scenario_end() {
  # scenario_end <scenario-id>
  local sid="$1"
  local p=$((RT_COUNT_PASS - RT_SCENARIO_START_PASS))
  local f=$((RT_COUNT_FAIL - RT_SCENARIO_START_FAIL))
  local dur=$(( $(date +%s) - RT_SCENARIO_START_TIME ))
  printf '<== %s: %d passed, %d failed (%ds)\n' "$sid" "$p" "$f" "$dur"
}

summary_block() {
  # summary_block <total-duration-seconds>
  local dur="$1"
  local total=$((RT_COUNT_PASS + RT_COUNT_FAIL + RT_COUNT_SKIP))
  printf '\n================================================================\n'
  printf 'SUMMARY\n'
  printf '================================================================\n'
  printf '  total:    %d assertions across %d scenarios\n' "$total" "$RT_COUNT_SCENARIOS"
  printf '  passed:   %d\n' "$RT_COUNT_PASS"
  printf '  failed:   %d\n' "$RT_COUNT_FAIL"
  printf '  skipped:  %d\n' "$RT_COUNT_SKIP"
  printf '  duration: %ds\n\n' "$dur"
  if [ "$RT_COUNT_FAIL" -gt 0 ]; then
    printf 'FAILURES:\n%s\n\n' "$RT_FAILURE_LINES"
    printf 'EXIT 1\n'
  else
    printf 'EXIT 0\n'
  fi
  printf '================================================================\n'
}
