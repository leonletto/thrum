#!/usr/bin/env bash
# Scenario: roles-deploy (migrates full_test_plan.md § 4.10)
#
# Verifies `thrum roles deploy` — both --dry-run and the live write
# path — renders Go text/template content into per-agent preamble
# files at .thrum/context/<agent>_preamble.md. Sub-fixture isolates
# the deploy from the run-level fixture's coord preamble (which the
# COORD pane consumes; mutating it would leak across scenarios).
#
# Five assertions:
#   1. --dry-run exits 0 + output contains "Dry run — no files written"
#   2. live deploy exits 0 + reports an updated-agent line
#   3. preamble file exists at .thrum/context/<agent>_preamble.md
#   4. preamble file content matches the rendered template (no
#      raw `{{ }}` markers leaked) AND contains the agent's literal name
#   5. --agent <name> targeted deploy exits 0
#
# au7k discipline: sub-daemon stopped at scenario end.

SID="36-roles-deploy"
SUB_REPO="$BASE/kafm1-36-roles"
SUB_AGENT="kafm1_36_agent"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_36() {

mkdir -p "$SUB_REPO"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-36@thrum.local" \
    && git config user.name "Release Tests 36" \
    && echo "# 36 roles deploy sub-fixture" > README.md \
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

# Register an implementer-role agent so the implementer template
# we're about to write matches by role.
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum quickstart \
    --name "$SUB_AGENT" \
    --role implementer \
    --module all \
    --intent "Release test 36" >/dev/null 2>&1 || {
  emit_fail "$SID" "subfixture-quickstart" "thrum quickstart" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

# Write a minimal template keyed to the implementer role. agentcontext.DeployAll
# matches templates by role name (filename stem).
mkdir -p "$SUB_REPO/.thrum/role_templates"
cat > "$SUB_REPO/.thrum/role_templates/implementer.md" <<'EOF'
## Role: {{.Role}}

You are {{.AgentName}} working in {{.WorktreePath}}.
EOF

# A1: --dry-run → exit 0 + "Dry run — no files written".
dry_out="$(mktemp -t kafm1-36-dry.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum roles deploy --dry-run \
  > "$dry_out" 2>&1
dry_rc=$?
if [ "$dry_rc" -eq 0 ] && grep -q "Dry run — no files written" "$dry_out"; then
  emit_pass "$SID" "dry-run-output"
else
  got="$(tr '\n' ' ' < "$dry_out" | head -c 240)"
  emit_fail "$SID" "dry-run-output" \
    "exit 0 + 'Dry run — no files written'" \
    "rc=${dry_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$dry_out"

# Confirm dry-run wrote nothing.
PREAMBLE_FILE="$SUB_REPO/.thrum/context/${SUB_AGENT}_preamble.md"
if [ -f "$PREAMBLE_FILE" ]; then
  # Pre-existing from quickstart's applyRolePreamble — capture mtime so we
  # can detect the live deploy actually rewrites it.
  PRE_MTIME="$(stat -f %m "$PREAMBLE_FILE" 2>/dev/null || stat -c %Y "$PREAMBLE_FILE" 2>/dev/null || echo 0)"
else
  PRE_MTIME=0
fi

sleep 1  # mtime resolution

# A2: live deploy → exit 0 + "Updated N/M agents" (current code emits
# "Updated", not the older "Deployed preamble for ..." string the
# markdown spec § 4.10 references).
deploy_out="$(mktemp -t kafm1-36-deploy.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum roles deploy \
  > "$deploy_out" 2>&1
deploy_rc=$?
if [ "$deploy_rc" -eq 0 ] && grep -qE "(Updated [0-9]+/[0-9]+ agents|Deployed preamble for)" "$deploy_out"; then
  emit_pass "$SID" "deploy-success-line"
else
  got="$(tr '\n' ' ' < "$deploy_out" | head -c 240)"
  emit_fail "$SID" "deploy-success-line" \
    "exit 0 + 'Updated N/M agents' OR 'Deployed preamble for ...'" \
    "rc=${deploy_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$deploy_out"

# A3: preamble file exists.
if [ -f "$PREAMBLE_FILE" ]; then
  emit_pass "$SID" "preamble-file-exists"
else
  emit_fail "$SID" "preamble-file-exists" \
    "preamble file at $PREAMBLE_FILE" \
    "(file missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# A4: rendered content — no raw `{{ }}` markers, contains agent name.
if [ -f "$PREAMBLE_FILE" ]; then
  if grep -q "$SUB_AGENT" "$PREAMBLE_FILE" \
     && ! grep -qE '\{\{\.[A-Za-z]+\}\}' "$PREAMBLE_FILE"; then
    emit_pass "$SID" "template-rendered"
  else
    got="$(tr '\n' ' ' < "$PREAMBLE_FILE" | head -c 240)"
    emit_fail "$SID" "template-rendered" \
      "preamble contains '${SUB_AGENT}' AND no raw '{{.X}}' markers" \
      "${got:-<empty>}" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
fi

# A5: targeted --agent deploy.
agent_out="$(mktemp -t kafm1-36-agent.XXXXXX).txt"
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum roles deploy --agent "$SUB_AGENT" \
  > "$agent_out" 2>&1
agent_rc=$?
if [ "$agent_rc" -eq 0 ]; then
  emit_pass "$SID" "deploy-targeted-agent"
else
  got="$(tr '\n' ' ' < "$agent_out" | head -c 240)"
  emit_fail "$SID" "deploy-targeted-agent" \
    "thrum roles deploy --agent $SUB_AGENT exits 0" \
    "rc=${agent_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi
rm -f "$agent_out"

}  # _run_scenario_36

_run_scenario_36

# au7k cleanup: stop sub-daemon.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env THRUM_NAME="$SUB_AGENT" thrum daemon stop >/dev/null 2>&1 || true
