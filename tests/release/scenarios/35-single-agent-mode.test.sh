#!/usr/bin/env bash
# Scenario: single-agent-mode (migrates full_test_plan.md § 4.9)
#
# Verifies the `thrum single-agent-mode` toggle: read-current,
# enable, confirm-enabled, disable, confirm-disabled. The toggle
# changes a daemon-level flag that disables messaging infrastructure
# (listener, inbox checks). Toggling it on the run-level fixture's
# daemon would silently break subsequent scenarios that rely on
# multi-agent messaging, so this scenario uses a sub-fixture.
#
# au7k discipline: sub-daemon stopped at scenario end.
#
# Five assertions:
#   1. bare invocation reports current mode (default: enabled —
#      `thrum init` ships with single_agent_mode=true)
#   2. `single-agent-mode true` exits 0
#   3. bare invocation now reports "enabled"
#   4. `single-agent-mode false` exits 0
#   5. bare invocation now reports "disabled (multi-agent)"

SID="35-single-agent-mode"
SUB_REPO="$BASE/kafm1-35-singlemode"
SUB_AGENT="kafm1_35_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_35() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-35@thrum.local" \
    && git config user.name "Release Tests 35" \
    && echo "# 35 single-agent sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum init --runtime claude >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "Release test 35" >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-quickstart" "thrum quickstart in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

_run_sm() {
  local out
  out="$(mktemp -t kafm1-35.XXXXXX).txt"
  "$TE" exec --cwd "$SUB_REPO" --clean -- \
    env "THRUM_NAME=$SUB_AGENT" thrum single-agent-mode "$@" \
    > "$out" 2>&1
  local rc=$?
  echo "$rc:$out"
}

# A1: bare → "single-agent mode: enabled" or "...disabled".
res="$(_run_sm)"
rc="${res%%:*}"; out="${res#*:}"
if [ "$rc" -eq 0 ] && grep -qE "single-agent mode: (enabled|disabled)" "$out"; then
  emit_pass "$SID" "bare-reports-current-mode"
else
  got="$(tr '\n' ' ' < "$out" | head -c 240)"
  emit_fail "$SID" "bare-reports-current-mode" \
    "exit 0 + 'single-agent mode: enabled|disabled'" \
    "rc=${rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out"

# A2: enable → exit 0.
res="$(_run_sm true)"
rc="${res%%:*}"; out="${res#*:}"
if [ "$rc" -eq 0 ]; then
  emit_pass "$SID" "enable-exits-zero"
else
  got="$(tr '\n' ' ' < "$out" | head -c 240)"
  emit_fail "$SID" "enable-exits-zero" \
    "thrum single-agent-mode true exits 0" \
    "rc=${rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out"

# A3: bare → "enabled".
res="$(_run_sm)"
rc="${res%%:*}"; out="${res#*:}"
if [ "$rc" -eq 0 ] && grep -q "single-agent mode: enabled" "$out"; then
  emit_pass "$SID" "confirm-enabled"
else
  got="$(tr '\n' ' ' < "$out" | head -c 240)"
  emit_fail "$SID" "confirm-enabled" \
    "exit 0 + 'single-agent mode: enabled'" \
    "rc=${rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out"

# A4: disable → exit 0.
res="$(_run_sm false)"
rc="${res%%:*}"; out="${res#*:}"
if [ "$rc" -eq 0 ]; then
  emit_pass "$SID" "disable-exits-zero"
else
  got="$(tr '\n' ' ' < "$out" | head -c 240)"
  emit_fail "$SID" "disable-exits-zero" \
    "thrum single-agent-mode false exits 0" \
    "rc=${rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out"

# A5: bare → "disabled (multi-agent)".
res="$(_run_sm)"
rc="${res%%:*}"; out="${res#*:}"
if [ "$rc" -eq 0 ] && grep -q "single-agent mode: disabled (multi-agent)" "$out"; then
  emit_pass "$SID" "confirm-disabled"
else
  got="$(tr '\n' ' ' < "$out" | head -c 240)"
  emit_fail "$SID" "confirm-disabled" \
    "exit 0 + 'single-agent mode: disabled (multi-agent)'" \
    "rc=${rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$out"

}  # _run_scenario_35

_run_scenario_35

# au7k cleanup.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum daemon stop >/dev/null 2>&1 || true
