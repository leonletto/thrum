#!/usr/bin/env bash
# Scenario: quickstart-flags (migrates full_test_plan.md § 4.16)
#
# Verifies `thrum quickstart --no-init` (skips runtime config
# generation) and `thrum quickstart --preamble-file` (composes a
# custom preamble). Sub-fixture isolates the quickstart re-runs
# from the run-level fixture's coord/impl identities.
#
# Three assertions:
#   1. --no-init exits 0 AND its stdout does NOT include the
#      runtime-config sentinel lines (`✓ .claude/settings.json`,
#      `✓ scripts/thrum-startup.sh`)
#   2. --preamble-file exits 0
#   3. After --preamble-file quickstart, `thrum context preamble`
#      contains the custom marker from the supplied preamble file
#
# au7k discipline: sub-daemon stopped at scenario end.

SID="42-quickstart-flags"
SUB_REPO="$BASE/kafm1-42-quickstart"
SUB_AGENT="kafm1_42_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_42() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-42@thrum.local" \
    && git config user.name "Release Tests 42" \
    && echo "# 42" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-thrum-init" "thrum init" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

# A1: --no-init quickstart.
no_init_out="$(mktemp -t kafm1-42-noinit.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "No-init test" \
    --no-init \
    --force \
  > "$no_init_out" 2>&1
no_init_rc=$?
if [ "$no_init_rc" -eq 0 ] \
   && ! grep -q "✓ \.claude/settings\.json" "$no_init_out" \
   && ! grep -q "✓ scripts/thrum-startup\.sh" "$no_init_out"; then
  emit_pass "$SID" "no-init-skips-runtime-config"
else
  got="$(tr '\n' ' ' < "$no_init_out" | head -c 240)"
  emit_fail "$SID" "no-init-skips-runtime-config" \
    "exit 0 + no '✓ .claude/settings.json' / '✓ scripts/thrum-startup.sh' lines" \
    "rc=${no_init_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$no_init_out"

# A2: --preamble-file quickstart with custom marker.
PREAMBLE_MARKER="kafm1-42-custom-${RUNID}"
PREAMBLE_FILE="$BASE/kafm1-42-preamble.md"
echo "# Custom Test Preamble (${PREAMBLE_MARKER})" > "$PREAMBLE_FILE"

pf_out="$(mktemp -t kafm1-42-pf.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "Preamble test" \
    --preamble-file "$PREAMBLE_FILE" \
    --force \
  > "$pf_out" 2>&1
pf_rc=$?
if [ "$pf_rc" -eq 0 ]; then
  emit_pass "$SID" "preamble-file-success"
else
  got="$(tr '\n' ' ' < "$pf_out" | head -c 240)"
  emit_fail "$SID" "preamble-file-success" \
    "thrum quickstart --preamble-file exits 0" \
    "rc=${pf_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$pf_out"

# A3: context preamble contains the marker.
ctx_out="$(mktemp -t kafm1-42-ctx.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env "THRUM_NAME=$SUB_AGENT" thrum context preamble \
  > "$ctx_out" 2>&1
ctx_rc=$?
if [ "$ctx_rc" -eq 0 ] && grep -q "$PREAMBLE_MARKER" "$ctx_out"; then
  emit_pass "$SID" "preamble-contains-custom-marker"
else
  got="$(tr '\n' ' ' < "$ctx_out" | head -c 240)"
  emit_fail "$SID" "preamble-contains-custom-marker" \
    "exit 0 + 'context preamble' output containing '${PREAMBLE_MARKER}'" \
    "rc=${ctx_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$ctx_out" "$PREAMBLE_FILE"

}  # _run_scenario_42

_run_scenario_42

# au7k cleanup.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum daemon stop >/dev/null 2>&1 || true
