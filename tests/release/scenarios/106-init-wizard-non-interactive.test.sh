#!/usr/bin/env bash
# Scenario: init-wizard-non-interactive
#
# Asserts that --non-interactive routes to the legacy silent path even on
# a TTY. The wizard does NOT fire: no identity registration, no role
# templates, no Worktrees.BasePath. Existing CI/script invocations of
# `thrum init --non-interactive` keep their pre-wizard behavior bit-for-bit.

SID="106-init-wizard-non-interactive"
WIZ_DIR="$BASE/wiz-106"

mkdir -p "$WIZ_DIR" \
  || { emit_fail "$SID" "setup-mkdir" "mkdir succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

(
  cd "$WIZ_DIR" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-106@thrum.local" \
    && git config user.name "Release Tests 106" \
    && echo "# wiz-106" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || { emit_fail "$SID" "setup-git" "git init succeeded" "(failed)" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

# Run via tmux-exec for a TTY-like environment plus --non-interactive.
# Both paths (--non-interactive AND piped stdin) MUST route to the silent
# path; this scenario covers --non-interactive on a TTY explicitly because
# that's the case existing CI scripts on PTY-providing runners hit.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$WIZ_DIR" --clean --timeout 60 -- \
  thrum --repo "$WIZ_DIR" init --non-interactive --runtime cli-only \
    >"$BASE/wiz-106.out" 2>&1 \
  || { emit_fail "$SID" "wizard-exit" "thrum init exited 0" "see $BASE/wiz-106.out" "scenarios/${SID}.test.sh:$LINENO"; return 0; }

emit_pass "$SID" "wizard-exit"

if [ -f "$WIZ_DIR/.thrum/config.json" ]; then
  emit_pass "$SID" "silent-init-scaffolded"
else
  emit_fail "$SID" "silent-init-scaffolded" ".thrum/config.json present" "(missing)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Negative assertions: the wizard's side effects must NOT be present.
if [ -d "$WIZ_DIR/.thrum/role_templates" ]; then
  emit_fail "$SID" "no-role-templates" \
    "no .thrum/role_templates/ directory in silent mode" \
    "(role_templates/ exists)" \
    "scenarios/${SID}.test.sh:$LINENO"
else
  emit_pass "$SID" "no-role-templates"
fi

# The silent path uses inferWorktreeBasePath to populate Worktrees.BasePath
# with the new ~/.thrum/worktrees/<repo> default (migration target). The
# wizard's interactive prompt would have offered to override; here we just
# confirm the silent path didn't leak a custom path from somewhere.
EXPECTED_INFER_BP="$HOME/.thrum/worktrees/wiz-106"
GOT_BP="$(jq -r '.worktrees.base_path // ""' "$WIZ_DIR/.thrum/config.json" 2>/dev/null || true)"
if [ "$GOT_BP" = "$EXPECTED_INFER_BP" ]; then
  emit_pass "$SID" "worktrees-base-path-inferred"
else
  emit_fail "$SID" "worktrees-base-path-inferred" "$EXPECTED_INFER_BP" "$GOT_BP" "scenarios/${SID}.test.sh:$LINENO"
fi

# Identity must NOT be registered — silent path doesn't call quickstart.
IDENTS_DIR="$WIZ_DIR/.thrum/identities"
if [ -d "$IDENTS_DIR" ] && [ -n "$(ls "$IDENTS_DIR" 2>/dev/null)" ]; then
  emit_fail "$SID" "no-identity-registered" \
    "empty .thrum/identities/ in silent mode" \
    "($(ls "$IDENTS_DIR" | tr '\n' ' '))" \
    "scenarios/${SID}.test.sh:$LINENO"
else
  emit_pass "$SID" "no-identity-registered"
fi
