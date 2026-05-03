#!/usr/bin/env bash
# Scenario: init-wizard-stealth-scripts
#
# Asserts that --stealth init writes BOTH `scripts/thrum-startup.sh` AND
# `scripts/thrum-check-inbox.sh` into `.git/info/exclude` and NOT into
# `.gitignore`. Pins the bundled stealth-mode bug-fix tracked in the
# wizard design doc § Stealth-mode bug-fix.

SID="103-init-wizard-stealth-scripts"
WIZ_DIR="$BASE/wiz-103"
WIZ_WT="$BASE/wiz-103-wt"

mkdir -p "$WIZ_DIR" "$WIZ_WT" \
  || { emit_fail "$SID" "setup-mkdir" "mkdir succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

(
  cd "$WIZ_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-103@thrum.local" \
    && git config user.name "Release Tests 103" \
    && echo "# wiz-103" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || { emit_fail "$SID" "setup-git" "git init succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$WIZ_DIR" --clean --timeout 60 -- \
  thrum --repo "$WIZ_DIR" init --no-daemon --stealth \
    --name stealth_103 \
    --role implementer \
    --module dx \
    --worktrees-root "$WIZ_WT" \
    --roles=skip \
    >"$BASE/wiz-103.out" 2>&1 \
  || { emit_fail "$SID" "wizard-exit" "thrum init exited 0" "see $BASE/wiz-103.out" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

emit_pass "$SID" "wizard-exit"

EXCLUDE_FILE="$WIZ_DIR/.git/info/exclude"
GITIGNORE_FILE="$WIZ_DIR/.gitignore"

if [ ! -f "$EXCLUDE_FILE" ]; then
  emit_fail "$SID" "exclude-file-exists" ".git/info/exclude present" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi
emit_pass "$SID" "exclude-file-exists"

for entry in "scripts/thrum-startup.sh" "scripts/thrum-check-inbox.sh"; do
  if grep -Fxq "$entry" "$EXCLUDE_FILE"; then
    emit_pass "$SID" "stealth-excludes-$(basename "$entry" .sh)"
  else
    emit_fail "$SID" "stealth-excludes-$(basename "$entry" .sh)" \
      "$entry present in $EXCLUDE_FILE" \
      "(missing)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
done

# Negative assertion: stealth mode must NOT touch .gitignore at all.
# Either the file shouldn't exist, or it shouldn't contain thrum entries.
if [ -f "$GITIGNORE_FILE" ] && grep -qE "^scripts/thrum-(startup|check-inbox)\.sh$" "$GITIGNORE_FILE"; then
  emit_fail "$SID" "stealth-gitignore-untouched" \
    "no thrum-* entries in .gitignore (stealth mode)" \
    "(found thrum entries)" \
    "scenarios/${SID}.test.sh:$LINENO"
else
  emit_pass "$SID" "stealth-gitignore-untouched"
fi
