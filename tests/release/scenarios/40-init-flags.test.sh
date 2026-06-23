#!/usr/bin/env bash
# Scenario: init-flags (migrates full_test_plan.md § 4.14)
#
# Verifies `thrum init --dry-run` writes nothing AND `thrum init
# --stealth --runtime claude` excludes .thrum/ via
# .git/info/exclude (NOT .gitignore). Sub-fixture is a fresh
# scratch git repo with NO prior thrum init.
#
# Five assertions:
#   1. --dry-run exits 0
#   2. After --dry-run, .thrum/ does NOT exist
#   3. --stealth --runtime claude exits 0
#   4. .gitignore was NOT modified (no .thrum line)
#   5. .git/info/exclude contains a .thrum entry
#
# au7k discipline: --stealth init auto-starts a daemon — stop it at
# scenario end.

SID="40-init-flags"
SCRATCH="$BASE/kafm1-40-init-scratch"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_40() {

# Build a fresh git repo with NO thrum init.
rm -rf "$SCRATCH"
mkdir -p "$SCRATCH"
(
  cd "$SCRATCH" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-40@thrum.local" \
    && git config user.name "Release Tests 40" \
    && echo "# 40 init flags" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "scratch-git-init" "git init in $SCRATCH" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

# A1: --dry-run exits 0. Pin --runtime claude so --dry-run skips the
# interactive runtime-selection prompt (which would block on tmux-exec
# stdin and time out).
dry_out="$(mktemp -t kafm1-40-dry.XXXXXX).txt"
"$TE" exec --cwd "$SCRATCH" --clean -- thrum init --dry-run --runtime claude \
  > "$dry_out" 2>&1
dry_rc=$?
if [ "$dry_rc" -eq 0 ]; then
  emit_pass "$SID" "dry-run-exits-zero"
else
  got="$(tr '\n' ' ' < "$dry_out" | head -c 240)"
  emit_fail "$SID" "dry-run-exits-zero" \
    "thrum init --dry-run exits 0" \
    "rc=${dry_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$dry_out"

# A2: --dry-run wrote nothing.
if [ ! -d "$SCRATCH/.thrum" ]; then
  emit_pass "$SID" "dry-run-writes-nothing"
else
  emit_fail "$SID" "dry-run-writes-nothing" \
    ".thrum/ NOT created during --dry-run" \
    "(directory exists at $SCRATCH/.thrum)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# A3: --stealth --runtime claude.
stealth_out="$(mktemp -t kafm1-40-stealth.XXXXXX).txt"
"$TE" exec --cwd "$SCRATCH" --clean -- thrum init --non-interactive --stealth --runtime claude \
  > "$stealth_out" 2>&1
stealth_rc=$?
if [ "$stealth_rc" -eq 0 ]; then
  emit_pass "$SID" "stealth-init-success"
else
  got="$(tr '\n' ' ' < "$stealth_out" | head -c 240)"
  emit_fail "$SID" "stealth-init-success" \
    "thrum init --non-interactive --stealth --runtime claude exits 0" \
    "rc=${stealth_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$stealth_out"

# A4: .gitignore was NOT modified — no .thrum entry.
if [ -f "$SCRATCH/.gitignore" ]; then
  if grep -q "\.thrum" "$SCRATCH/.gitignore" 2>/dev/null; then
    emit_fail "$SID" "gitignore-untouched" \
      ".gitignore does NOT contain .thrum (stealth uses .git/info/exclude)" \
      "got: $(tr '\n' ' ' < "$SCRATCH/.gitignore" | head -c 200)" \
      "scenarios/${SID}.test.sh:$LINENO"
  else
    emit_pass "$SID" "gitignore-untouched"
  fi
else
  # No .gitignore at all means stealth left it untouched (it didn't exist
  # before init either).
  emit_pass "$SID" "gitignore-untouched"
fi

# A5: .git/info/exclude contains .thrum.
if [ -f "$SCRATCH/.git/info/exclude" ] \
   && grep -q "\.thrum" "$SCRATCH/.git/info/exclude"; then
  emit_pass "$SID" "exclude-contains-thrum"
else
  got=""
  if [ -f "$SCRATCH/.git/info/exclude" ]; then
    got="$(tr '\n' ' ' < "$SCRATCH/.git/info/exclude" | head -c 200)"
  fi
  emit_fail "$SID" "exclude-contains-thrum" \
    ".git/info/exclude contains a .thrum entry" \
    "${got:-<file missing>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_40

_run_scenario_40

# au7k cleanup: stop the scratch daemon if it started.
"$TE" exec --cwd "$SCRATCH" --clean -- thrum daemon stop >/dev/null 2>&1 || true
rm -rf "$SCRATCH"
