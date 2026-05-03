#!/usr/bin/env bash
# Scenario: init-wizard-happy-path
#
# Drives `thrum init` through every interactive prompt with enter-only
# input, using expect(1) to provide a PTY (the wizard's TTY check would
# otherwise route to the silent path). --no-daemon short-circuits the
# wizard's final stepDaemon + quickstart subprocess so the test fixture
# stays clean — we don't need a running daemon to assert that the wizard
# scaffolded .thrum/, persisted Worktrees.BasePath, and wrote default role
# templates. (A scenario that exercises the daemon-start path would have
# to teardown the daemon at the end; the registration coverage we lose by
# skipping it is exercised in 102-init-wizard-flag-overrides via the
# flag-driven path.)

SID="101-init-wizard-happy-path"
WIZ_DIR="$BASE/wiz-101"
WIZ_HOME="$BASE/wiz-101-home"

mkdir -p "$WIZ_DIR" "$WIZ_HOME/.thrum/worktrees" \
  || { emit_fail "$SID" "setup-mkdir" "mkdir succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

(
  cd "$WIZ_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-101@thrum.local" \
    && git config user.name "Release Tests 101" \
    && echo "# wiz-101" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || { emit_fail "$SID" "setup-git" "git init succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

# Drive the wizard with an expect script. HOME is overridden so
# stepWorktreesRoot's default (~/.thrum/worktrees/<repo>) lands under $BASE
# instead of polluting the developer's actual home.
EXPECT_OUT="$BASE/wiz-101.expect.log"
expect <<EOF >"$EXPECT_OUT" 2>&1
log_user 1
set timeout 30
set env(HOME) "$WIZ_HOME"
spawn thrum --repo "$WIZ_DIR" init --no-daemon
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
  emit_pass "$SID" "wizard-exits-cleanly"
else
  emit_fail "$SID" "wizard-exits-cleanly" "EXIT_STATUS=0 in expect log" "$(grep EXIT_STATUS= "$EXPECT_OUT" | tail -1)" "scenarios/${SID}.test.sh:$LINENO"
fi

if [ -f "$WIZ_DIR/.thrum/config.json" ]; then
  emit_pass "$SID" "thrum-dir-scaffolded"
else
  emit_fail "$SID" "thrum-dir-scaffolded" ".thrum/config.json present" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
fi

# Default worktrees.base_path should resolve under the overridden HOME.
EXPECTED_BP="$WIZ_HOME/.thrum/worktrees/wiz-101"
GOT_BP="$(jq -r '.worktrees.base_path // ""' "$WIZ_DIR/.thrum/config.json" 2>/dev/null || true)"
if [ "$GOT_BP" = "$EXPECTED_BP" ]; then
  emit_pass "$SID" "worktrees-base-path-default"
else
  emit_fail "$SID" "worktrees-base-path-default" "$EXPECTED_BP" "$GOT_BP" "scenarios/${SID}.test.sh:$LINENO"
fi

# Default role-template choice (option 1 = enhanced) writes coordinator,
# implementer, orchestrator templates under .thrum/role_templates/.
for tmpl in coordinator implementer orchestrator; do
  if [ -f "$WIZ_DIR/.thrum/role_templates/$tmpl.md" ]; then
    emit_pass "$SID" "role-template-$tmpl"
  else
    emit_fail "$SID" "role-template-$tmpl" ".thrum/role_templates/$tmpl.md present" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
  fi
done
