#!/usr/bin/env bash
# Scenario: init-wizard-force-reinit
#
# Pre-creates .thrum/ with a custom Worktrees.BasePath value, then re-runs
# `thrum init --force` and presses enter through every prompt. The wizard's
# loadReInitDefaults must seed each prompt's default from the existing
# config so enter-only input preserves the user's prior choice — the
# resulting config.json should still carry the custom BasePath unchanged.

SID="107-init-wizard-force-reinit"
WIZ_DIR="$BASE/wiz-107"
WIZ_HOME="$BASE/wiz-107-home"
CUSTOM_BP="$BASE/wiz-107-custom-wt"

mkdir -p "$WIZ_DIR" "$WIZ_HOME" "$CUSTOM_BP" \
  || { emit_fail "$SID" "setup-mkdir" "mkdir succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

(
  cd "$WIZ_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-107@thrum.local" \
    && git config user.name "Release Tests 107" \
    && echo "# wiz-107" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || { emit_fail "$SID" "setup-git" "git init succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

# Bootstrap the existing .thrum/ with the custom Worktrees.BasePath. Use
# `thrum init --non-interactive` so the silent path scaffolds the dir,
# then patch the config to record the custom path.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$WIZ_DIR" --clean --timeout 60 -- \
  thrum --repo "$WIZ_DIR" init --non-interactive --runtime cli-only \
    >"$BASE/wiz-107.bootstrap.out" 2>&1 \
  || { emit_fail "$SID" "bootstrap-init" "silent init succeeded" "see $BASE/wiz-107.bootstrap.out" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

# Patch worktrees.base_path into the bootstrap config.
PATCHED_CONFIG="$BASE/wiz-107.config.patched.json"
jq --arg bp "$CUSTOM_BP" '.worktrees = {base_path: $bp, beads_enabled: true, thrum_enabled: true}' \
  "$WIZ_DIR/.thrum/config.json" > "$PATCHED_CONFIG" \
  && cp "$PATCHED_CONFIG" "$WIZ_DIR/.thrum/config.json" \
  || { emit_fail "$SID" "patch-config" "patched .thrum/config.json with custom base_path" "(jq/cp failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

# Re-run with --force, drive every prompt with enter-only input.
EXPECT_OUT="$BASE/wiz-107.expect.log"
expect <<EOF >"$EXPECT_OUT" 2>&1
log_user 1
set timeout 30
set env(HOME) "$WIZ_HOME"
spawn thrum --repo "$WIZ_DIR" init --force --no-daemon
expect "Agent name"  { send -- "\r" }
expect "Role"        { send -- "\r" }
expect "Module"      { send -- "\r" }
expect "Where should agent worktrees live" { send -- "\r" }
expect "Choose"      { send -- "\r" }
expect "skipping daemon"
expect eof
catch wait result
puts "EXIT_STATUS=[lindex \$result 3]"
EOF

if grep -q "EXIT_STATUS=0" "$EXPECT_OUT"; then
  emit_pass "$SID" "wizard-reinit-exits-cleanly"
else
  emit_fail "$SID" "wizard-reinit-exits-cleanly" "EXIT_STATUS=0 in expect log" "$(grep EXIT_STATUS= "$EXPECT_OUT" | tail -1)" "scenarios/${SID}.test.sh:$LINENO"
fi

GOT_BP="$(jq -r '.worktrees.base_path // ""' "$WIZ_DIR/.thrum/config.json" 2>/dev/null || true)"
if [ "$GOT_BP" = "$CUSTOM_BP" ]; then
  emit_pass "$SID" "custom-base-path-preserved"
else
  emit_fail "$SID" "custom-base-path-preserved" "$CUSTOM_BP" "$GOT_BP" "scenarios/${SID}.test.sh:$LINENO"
fi
