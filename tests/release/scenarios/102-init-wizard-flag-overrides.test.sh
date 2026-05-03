#!/usr/bin/env bash
# Scenario: init-wizard-flag-overrides
#
# Pre-fills every wizard prompt via flags (--name, --role, --module,
# --worktrees-root, --roles, --no-daemon). The wizard runs end-to-end
# without firing a single prompt even on a TTY (PTY supplied by the
# tmux-exec pool pane). Asserts that flag values land in the resulting
# .thrum/config.json verbatim.
#
# --no-daemon keeps the test fixture clean — see 101-init-wizard-happy-path
# for the full prompt-driving variant.

SID="102-init-wizard-flag-overrides"
WIZ_DIR="$BASE/wiz-102"
WIZ_WT="$BASE/wiz-102-wt"

mkdir -p "$WIZ_DIR" "$WIZ_WT" \
  || { emit_fail "$SID" "setup-mkdir" "mkdir succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

(
  cd "$WIZ_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-102@thrum.local" \
    && git config user.name "Release Tests 102" \
    && echo "# wiz-102" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || { emit_fail "$SID" "setup-git" "git init succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

# Drive via the tmux-exec pool pane so stdin/stdout are PTYs and the
# wizard's isInteractive() check returns true.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$WIZ_DIR" --clean --timeout 60 -- \
  thrum --repo "$WIZ_DIR" init --no-daemon \
    --name flagsoverride_102 \
    --role implementer \
    --module dx \
    --worktrees-root "$WIZ_WT" \
    --roles=enhanced \
    >"$BASE/wiz-102.out" 2>&1 \
  || { emit_fail "$SID" "wizard-exit" "thrum init exited 0" "see $BASE/wiz-102.out" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

emit_pass "$SID" "wizard-exit"

if [ -f "$WIZ_DIR/.thrum/config.json" ]; then
  emit_pass "$SID" "thrum-dir-scaffolded"
else
  emit_fail "$SID" "thrum-dir-scaffolded" ".thrum/config.json present" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

GOT_BP="$(jq -r '.worktrees.base_path // ""' "$WIZ_DIR/.thrum/config.json" 2>/dev/null || true)"
if [ "$GOT_BP" = "$WIZ_WT" ]; then
  emit_pass "$SID" "worktrees-base-path-from-flag"
else
  emit_fail "$SID" "worktrees-base-path-from-flag" "$WIZ_WT" "$GOT_BP" "scenarios/${SID}.test.sh:$LINENO"
fi

# --roles=enhanced writes all three templates; the prompt should never have
# fired (no interactive choice = no chance to pick differently).
for tmpl in coordinator implementer orchestrator; do
  if [ -f "$WIZ_DIR/.thrum/role_templates/$tmpl.md" ]; then
    emit_pass "$SID" "role-template-$tmpl"
  else
    emit_fail "$SID" "role-template-$tmpl" ".thrum/role_templates/$tmpl.md present" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
  fi
done

# The wizard's pre-quickstart skip message confirms we hit the --no-daemon
# branch end-to-end (vs. failing earlier and silently rolling back).
if grep -q "skipping daemon" "$BASE/wiz-102.out"; then
  emit_pass "$SID" "no-daemon-message"
else
  emit_fail "$SID" "no-daemon-message" "wizard logged --no-daemon skip line" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
fi
