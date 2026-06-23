#!/usr/bin/env bash
# Scenario: runtime-management (migrates full_test_plan.md § 4.12)
#
# Verifies the `thrum runtime` group: list, show <name>, show
# <unknown> (error), set-default. Sub-fixture isolates the
# set-default mutation so the run-level fixture's config.json stays
# pinned to claude.
#
# Five assertions:
#   1. `runtime list` exits 0 + lists known runtimes (claude, codex)
#   2. `runtime show claude` exits 0 + prints details
#   3. `runtime show unknown-rt-xyz` exits non-zero (error path)
#   4. `runtime set-default codex` exits 0
#   5. `config show --json` after set-default reports primary=codex
#
# au7k discipline: sub-daemon stopped at scenario end.

SID="38-runtime-management"
SUB_REPO="$BASE/kafm1-38-runtime"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_38() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-38@thrum.local" \
    && git config user.name "Release Tests 38" \
    && echo "# 38" > README.md \
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

# A1: runtime list.
list_out="$(mktemp -t kafm1-38-list.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum runtime list \
  > "$list_out" 2>&1
list_rc=$?
if [ "$list_rc" -eq 0 ] && grep -q "claude" "$list_out" && grep -q "codex" "$list_out"; then
  emit_pass "$SID" "list-shows-runtimes"
else
  got="$(tr '\n' ' ' < "$list_out" | head -c 240)"
  emit_fail "$SID" "list-shows-runtimes" \
    "exit 0 + output containing 'claude' AND 'codex'" \
    "rc=${list_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$list_out"

# A2: runtime show claude.
show_out="$(mktemp -t kafm1-38-show.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum runtime show claude \
  > "$show_out" 2>&1
show_rc=$?
if [ "$show_rc" -eq 0 ] && [ -s "$show_out" ]; then
  emit_pass "$SID" "show-known-runtime"
else
  got="$(tr '\n' ' ' < "$show_out" | head -c 240)"
  emit_fail "$SID" "show-known-runtime" \
    "thrum runtime show claude exits 0 with non-empty output" \
    "rc=${show_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$show_out"

# A3: runtime show unknown.
unknown_out="$(mktemp -t kafm1-38-unknown.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum runtime show unknown-rt-xyz \
  > "$unknown_out" 2>&1
unknown_rc=$?
if [ "$unknown_rc" -ne 0 ]; then
  emit_pass "$SID" "show-unknown-errors"
else
  got="$(tr '\n' ' ' < "$unknown_out" | head -c 240)"
  emit_fail "$SID" "show-unknown-errors" \
    "thrum runtime show unknown-rt-xyz exits non-zero" \
    "rc=0; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$unknown_out"

# A4: set-default codex.
set_out="$(mktemp -t kafm1-38-set.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum runtime set-default codex \
  > "$set_out" 2>&1
set_rc=$?
if [ "$set_rc" -eq 0 ]; then
  emit_pass "$SID" "set-default-success"
else
  got="$(tr '\n' ' ' < "$set_out" | head -c 240)"
  emit_fail "$SID" "set-default-success" \
    "thrum runtime set-default codex exits 0" \
    "rc=${set_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$set_out"

# A5: config show --json reports primary=codex.
cfg_file="$(mktemp -t kafm1-38-cfg.XXXXXX).json"
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  bash -c "thrum config show --json > '${cfg_file}' 2>/dev/null" \
  >/dev/null 2>&1 || true

primary=""
if [ -s "$cfg_file" ]; then
  primary="$(jq -r '.runtime.primary // .Runtime.Primary // ""' "$cfg_file" 2>/dev/null)"
fi
if [ "$primary" = "codex" ]; then
  emit_pass "$SID" "config-reflects-set-default"
else
  emit_fail "$SID" "config-reflects-set-default" \
    "config show --json runtime.primary == 'codex'" \
    "got: '${primary}'" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$cfg_file"

}  # _run_scenario_38

_run_scenario_38

# au7k cleanup: stop sub-daemon.
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon stop >/dev/null 2>&1 || true
