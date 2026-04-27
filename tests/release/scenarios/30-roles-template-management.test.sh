#!/usr/bin/env bash
# Scenario: roles-template-management (migrates full_test_plan.md § 4.4)
#
# Verifies `thrum roles list` reports both the empty and populated
# states correctly. The fresh fixture has no `.thrum/role_templates/`
# directory at all, so the empty path returns the canonical
# "No role templates found in .thrum/role_templates/" line. After
# creating a template file, list should surface its name.
#
# Driven via tmux-exec out-of-pane against $COORD_REPO. The
# fixture's main repo is also where coord pane is rooted, so any
# template we create lands in the run-level .thrum/. Cleanup
# removes the template at scenario end so subsequent scenarios
# (36-roles-deploy in particular) start clean.

SID="30-roles-template-management"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
TPL_DIR="$COORD_REPO/.thrum/role_templates"
TPL_FILE="$TPL_DIR/kafm1-30-test.md"

# Assertion 1: empty state.
list_out="$(mktemp -t kafm1-30-empty.XXXXXX).txt"
"$TE" exec --cwd "$COORD_REPO" --clean -- thrum roles list \
  > "$list_out" 2>&1
list_rc=$?

if [ "$list_rc" -eq 0 ] && grep -q "No role templates found" "$list_out"; then
  emit_pass "$SID" "list-empty-state"
else
  got="$(tr '\n' ' ' < "$list_out" | head -c 240)"
  emit_fail "$SID" "list-empty-state" \
    "exit 0 + stdout containing 'No role templates found'" \
    "rc=${list_rc}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Create a minimal template so the populated path lights up.
mkdir -p "$TPL_DIR"
cat > "$TPL_FILE" <<'EOF'
## Role: {{.Role}}

You are {{.AgentName}} working in {{.WorktreePath}}.
EOF

# Assertion 2: populated state — output contains the template name.
list_out2="$(mktemp -t kafm1-30-pop.XXXXXX).txt"
"$TE" exec --cwd "$COORD_REPO" --clean -- thrum roles list \
  > "$list_out2" 2>&1
list_rc2=$?

if [ "$list_rc2" -eq 0 ] && grep -q "kafm1-30-test" "$list_out2"; then
  emit_pass "$SID" "list-populated-state"
else
  got="$(tr '\n' ' ' < "$list_out2" | head -c 240)"
  emit_fail "$SID" "list-populated-state" \
    "exit 0 + stdout containing 'kafm1-30-test' template name" \
    "rc=${list_rc2}; output: ${got:-<empty>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Cleanup: remove the template + the dir if we created it empty.
rm -f "$TPL_FILE" "$list_out" "$list_out2"
rmdir "$TPL_DIR" 2>/dev/null || true
