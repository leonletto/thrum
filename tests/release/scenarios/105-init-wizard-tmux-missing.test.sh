#!/usr/bin/env bash
# Scenario: init-wizard-tmux-missing
#
# Asserts that when tmux is not on PATH the wizard's Step 0 gate exits
# with a non-zero status, prints a brew/macports/apt suggestion, and
# does NOT scaffold .thrum/. Uses expect(1) to provide a PTY because
# the test process strips PATH down to /usr/bin (which has neither tmux
# nor /Users/leon/.workspaces, so no wizard-skipping fallback fires).

SID="105-init-wizard-tmux-missing"
WIZ_DIR="$BASE/wiz-105"
WIZ_WT="$BASE/wiz-105-wt"

mkdir -p "$WIZ_DIR" "$WIZ_WT" \
  || { emit_fail "$SID" "setup-mkdir" "mkdir succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

(
  cd "$WIZ_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-105@thrum.local" \
    && git config user.name "Release Tests 105" \
    && echo "# wiz-105" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || { emit_fail "$SID" "setup-git" "git init succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

THRUM_BIN="$(command -v thrum)"
if [ -z "$THRUM_BIN" ]; then
  emit_fail "$SID" "setup-find-thrum" "thrum on PATH" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Drive thrum init under a stripped PATH (/usr/bin only — no tmux). Pass
# the absolute thrum binary path so the test process itself can still
# launch it. Pre-fill every prompt so no input is needed; the gate fires
# before any prompt would have rendered.
EXPECT_OUT="$BASE/wiz-105.expect.log"
expect <<EOF >"$EXPECT_OUT" 2>&1
log_user 1
set timeout 30
set env(PATH) "/usr/bin"
spawn "$THRUM_BIN" --repo "$WIZ_DIR" init --no-daemon \
  --name tmuxmiss_105 \
  --role implementer \
  --module dx \
  --worktrees-root "$WIZ_WT" \
  --roles=skip
expect eof
catch wait result
puts "EXIT_STATUS=[lindex \$result 3]"
EOF

if grep -q "EXIT_STATUS=0" "$EXPECT_OUT"; then
  emit_fail "$SID" "wizard-fails" "non-zero exit when tmux missing" "EXIT_STATUS=0" "scenarios/${SID}.test.sh:$LINENO"
else
  emit_pass "$SID" "wizard-fails"
fi

# Suggestion message contains at least one package-manager hint.
if grep -Eq "brew|apt|macports|pacman" "$EXPECT_OUT"; then
  emit_pass "$SID" "tmux-install-suggestion"
else
  emit_fail "$SID" "tmux-install-suggestion" \
    "stderr contains brew/apt/macports/pacman suggestion" \
    "(no suggestion found)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Scaffolding must NOT have happened — the gate fires before Init().
if [ -d "$WIZ_DIR/.thrum" ]; then
  emit_fail "$SID" "no-thrum-scaffold" \
    "no .thrum/ created when tmux gate fails" \
    "(.thrum/ exists)" \
    "scenarios/${SID}.test.sh:$LINENO"
else
  emit_pass "$SID" "no-thrum-scaffold"
fi
