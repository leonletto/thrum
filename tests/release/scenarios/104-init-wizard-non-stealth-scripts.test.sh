#!/usr/bin/env bash
# Scenario: init-wizard-non-stealth-scripts
#
# Mirror of 103 without --stealth. Asserts that BOTH
# `scripts/thrum-startup.sh` AND `scripts/thrum-check-inbox.sh` land in
# `.gitignore`. The check-inbox entry was the bug fix bundled with the
# wizard work (design doc § Stealth-mode bug-fix).

SID="104-init-wizard-non-stealth-scripts"
WIZ_DIR="$BASE/wiz-104"
WIZ_WT="$BASE/wiz-104-wt"

mkdir -p "$WIZ_DIR" "$WIZ_WT" \
  || { emit_fail "$SID" "setup-mkdir" "mkdir succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

(
  cd "$WIZ_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-104@thrum.local" \
    && git config user.name "Release Tests 104" \
    && echo "# wiz-104" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || { emit_fail "$SID" "setup-git" "git init succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$WIZ_DIR" --clean --timeout 60 -- \
  thrum --repo "$WIZ_DIR" init --no-daemon \
    --name nonstealth_104 \
    --role implementer \
    --module dx \
    --worktrees-root "$WIZ_WT" \
    --roles=skip \
    >"$BASE/wiz-104.out" 2>&1 \
  || { emit_fail "$SID" "wizard-exit" "thrum init exited 0" "see $BASE/wiz-104.out" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

emit_pass "$SID" "wizard-exit"

GITIGNORE_FILE="$WIZ_DIR/.gitignore"

if [ ! -f "$GITIGNORE_FILE" ]; then
  emit_fail "$SID" "gitignore-exists" ".gitignore present" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi
emit_pass "$SID" "gitignore-exists"

for entry in "scripts/thrum-startup.sh" "scripts/thrum-check-inbox.sh"; do
  if grep -Fxq "$entry" "$GITIGNORE_FILE"; then
    emit_pass "$SID" "non-stealth-gitignore-$(basename "$entry" .sh)"
  else
    emit_fail "$SID" "non-stealth-gitignore-$(basename "$entry" .sh)" \
      "$entry present in .gitignore" \
      "(missing)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
done
