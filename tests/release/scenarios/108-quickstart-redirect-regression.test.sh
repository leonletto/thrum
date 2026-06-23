#!/usr/bin/env bash
# Scenario: quickstart-redirect-regression (thrum-tc4w)
#
# Verifies that `thrum quickstart` from inside a redirect-using worktree
# writes the new agent's identity to the CALLING worktree, not to
# THRUM_HOME. The v0.10.0 regression: PersistentPreRunE applied
# EffectiveRepoPath unconditionally, so every command (including
# quickstart) had its flagRepo silently rewritten to $THRUM_HOME. With
# THRUM_HOME pointing at the redirect target (the main repo), quickstart
# wrote the identity into the main repo's .thrum/identities/ and stamped
# THRUM_HOME as the agent's worktree field.
#
# Sub-fixture pattern (reference: 89-config-keys-init.test.sh) so this
# scenario doesn't disturb the run-level coord+impl tmux fixture. Steps:
#   1. Build a fresh sub-fixture parent repo + thrum init.
#   2. Create a child worktree via `thrum worktree create` (sets up
#      .thrum/redirect → parent/.thrum).
#   3. cd into the child worktree (CWD-pwd, NOT --cwd) and run
#      `THRUM_HOME=$SUB_REPO thrum quickstart ...` — the THRUM_HOME
#      override is what triggers the regression on v0.10.0.
#   4. Assert the identity file is in the CHILD's identities dir.
#   5. Assert the worktree field equals the child path, not THRUM_HOME.
#
# Sub-daemon stopped at end (au7k discipline).

SID="108-quickstart-redirect-regression"
SUB_REPO="$BASE/tc4w-108-repo"
SUB_WT_BASE="$BASE/tc4w-108-wt"
SUB_AGENT="tc4w_108_parent"
WT_NAME="tc4w-108-child"
WT_AGENT="tc4w_108_child"
# After thrum's auto-append of repo basename:
WT_PATH="$SUB_WT_BASE/$(basename "$SUB_REPO")/${WT_NAME}"
TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"

_run_scenario_108() {

# Build the sub-fixture: git repo + thrum init.
mkdir -p "$SUB_REPO" "$SUB_WT_BASE"
(
  cd "$SUB_REPO" \
    && git init --initial-branch=main >/dev/null \
    && git config user.email "release-tests-108@thrum.local" \
    && git config user.name "Release Tests 108" \
    && echo "# 108 sub-fixture" > README.md \
    && git add . && git commit -m "init" >/dev/null
) || {
  emit_fail "$SID" "subfixture-git-init" "git init in $SUB_REPO" "(failed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
}

"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum init --non-interactive --runtime claude >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-thrum-init" "thrum init in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Register the parent agent so worktree create has a registered caller.
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum quickstart \
    --name "$SUB_AGENT" \
    --role coordinator \
    --module all \
    --intent "Release test 108 parent" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-quickstart-parent" \
      "thrum quickstart parent in $SUB_REPO" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Patch worktrees config so the create lands under SUB_WT_BASE.
jq --arg bp "$SUB_WT_BASE/" \
  '.worktrees = {"base_path": $bp, "beads_enabled": false, "thrum_enabled": true}' \
  "$SUB_REPO/.thrum/config.json" > "$SUB_REPO/.thrum/config.json.tmp" \
  && mv "$SUB_REPO/.thrum/config.json.tmp" "$SUB_REPO/.thrum/config.json" \
  || { emit_fail "$SID" "config-patch" "patch worktrees.base_path" "(failed)" \
       "scenarios/${SID}.test.sh:$LINENO"
       return 0; }

# Create the child worktree (sets up .thrum/redirect → parent/.thrum).
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env "THRUM_NAME=$SUB_AGENT" thrum worktree create "$WT_NAME" >/dev/null 2>&1 || {
    emit_fail "$SID" "subfixture-worktree-create" \
      "thrum worktree create $WT_NAME" "(failed)" \
      "scenarios/${SID}.test.sh:$LINENO"
    return 0
  }

# Confirm the redirect was wired before we exercise the bug.
if [ ! -f "$WT_PATH/.thrum/redirect" ]; then
  emit_fail "$SID" "subfixture-redirect-present" \
    ".thrum/redirect at ${WT_PATH}/.thrum/redirect" \
    "(redirect missing — fixture pre-condition unmet)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Pre-stage a stale identity file in the PARENT's identities/ with the
# SAME name the child quickstart will use. This exercises two related
# THRUM_HOME isolation paths flagged in dual review:
#
#   - cli/quickstart.go G1a/G1b guard: the guard's IdentitiesDir must
#     resolve to the child's .thrum/identities/, NOT the parent's. The
#     pre-staged file below uses agent_pid: 0 (dead/unset, won't trigger
#     G1b's liveness check). If the guard reads the parent dir, this
#     stale file would falsely register as a sibling or be silently
#     quarantined — depending on how the wrong dir is loaded.
#   - config.LoadIdentityWithPath in Step 2.5: the load must read the
#     child's dir. If it reads the parent's, Step 2.5 picks up this
#     file's stale Intent ("PARENT-INTENT-SHOULD-NOT-LEAK") and writes
#     it into the child's new identity instead of the explicit --intent
#     value the caller passed.
#
# The intent assertion below proves both: child file's .intent must
# match what we passed at the CLI, not the parent's stale value.
mkdir -p "$SUB_REPO/.thrum/identities"
cat > "$SUB_REPO/.thrum/identities/${WT_AGENT}.json" <<JSON
{
  "version": 5,
  "agent": {"kind": "agent", "name": "${WT_AGENT}", "role": "implementer", "module": "child"},
  "worktree": "${SUB_REPO}",
  "intent": "PARENT-INTENT-SHOULD-NOT-LEAK",
  "agent_pid": 0
}
JSON

# THE TEST: quickstart from the child worktree's cwd, with THRUM_HOME
# set to the redirect target (the parent repo). On v0.10.0 this writes
# the identity to $SUB_REPO/.thrum/identities/. Post-fix it must land
# in the child's per-worktree identities dir.
qs_out=$(
  "$TE" exec --cwd "$WT_PATH" --clean -- \
    env "THRUM_HOME=$SUB_REPO" thrum quickstart \
      --name "$WT_AGENT" \
      --role implementer \
      --module child \
      --intent "Release test 108 child" \
      --runtime claude --force 2>&1
)
qs_rc=$?

if [ "$qs_rc" -ne 0 ]; then
  emit_fail "$SID" "quickstart-success" \
    "thrum quickstart in $WT_PATH exits 0" \
    "exit ${qs_rc}; output: $(printf '%s' "$qs_out" | tr '\n' ' ' | head -c 240)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 1: identity file lands in the CHILD's identities dir.
local child_id="$WT_PATH/.thrum/identities/${WT_AGENT}.json"
if [ -f "$child_id" ]; then
  emit_pass "$SID" "identity-in-child-worktree"
else
  emit_fail "$SID" "identity-in-child-worktree" \
    "identity file at ${child_id}" \
    "(file missing — quickstart wrote it elsewhere; check $SUB_REPO/.thrum/identities/)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2: pre-staged stale parent file is UNCHANGED. The pre-stage
# wrote intent="PARENT-INTENT-SHOULD-NOT-LEAK" and left updated_at
# absent; if quickstart hijacked to the parent, SaveIdentityFile would
# rewrite the file and stamp updated_at with the current time. So a
# preserved (absent or empty) updated_at + the stale intent value still
# present is the strongest "parent untouched" signal we can read.
local parent_id="$SUB_REPO/.thrum/identities/${WT_AGENT}.json"
if [ -f "$parent_id" ]; then
  local parent_intent parent_updated
  parent_intent=$(jq -r '.intent // ""' "$parent_id" 2>/dev/null)
  parent_updated=$(jq -r '.updated_at // ""' "$parent_id" 2>/dev/null)
  if [ "$parent_intent" = "PARENT-INTENT-SHOULD-NOT-LEAK" ] && [ -z "$parent_updated" ]; then
    emit_pass "$SID" "parent-identity-untouched"
  else
    emit_fail "$SID" "parent-identity-untouched" \
      "parent ${WT_AGENT}.json intent='PARENT-INTENT-SHOULD-NOT-LEAK', updated_at empty" \
      "intent='${parent_intent}', updated_at='${parent_updated}' (quickstart hijacked by THRUM_HOME)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_fail "$SID" "parent-identity-untouched" \
    "pre-staged ${parent_id} present" \
    "(file missing — fixture pre-condition unmet)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 2b: child identity's intent matches the explicit --intent
# value, NOT the stale parent's. This exercises Step 2.5 enrichment in
# cli/quickstart.go: LoadIdentityWithPath must read the CHILD's
# identities dir, not THRUM_HOME's. If it reads parent's, the new
# child file inherits the stale "PARENT-INTENT-SHOULD-NOT-LEAK" value.
if [ -f "$child_id" ]; then
  local child_intent
  child_intent=$(jq -r '.intent // ""' "$child_id" 2>/dev/null)
  if [ "$child_intent" = "Release test 108 child" ]; then
    emit_pass "$SID" "child-intent-not-inherited-from-parent"
  else
    emit_fail "$SID" "child-intent-not-inherited-from-parent" \
      "child intent == 'Release test 108 child'" \
      "got: '${child_intent}' (Step 2.5 enrichment may have read parent's identities dir)" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_skip "$SID" "child-intent-not-inherited-from-parent" \
    "child identity file missing — see assertion identity-in-child-worktree"
fi

# Assertion 3: worktree field on the (correctly-placed) identity file
# equals the child worktree path, not THRUM_HOME. EvalSymlinks via
# `cd && pwd` so /var → /private/var on macOS doesn't false-fail.
if [ -f "$child_id" ]; then
  local stored_wt expected_wt stored_resolved
  stored_wt=$(jq -r '.worktree // ""' "$child_id" 2>/dev/null)
  expected_wt=$(cd "$WT_PATH" 2>/dev/null && pwd)
  stored_resolved=$(cd "$stored_wt" 2>/dev/null && pwd)
  if [ -n "$stored_resolved" ] && [ "$stored_resolved" = "$expected_wt" ]; then
    emit_pass "$SID" "worktree-field-matches-child"
  else
    emit_fail "$SID" "worktree-field-matches-child" \
      "worktree field resolves to ${expected_wt}" \
      "got: '${stored_wt}' (resolved: '${stored_resolved}')" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  # Surface assertion 3 in the report when the child file is missing,
  # so test output makes it obvious the bug reproduced (rather than
  # silently dropping a row).
  emit_skip "$SID" "worktree-field-matches-child" \
    "child identity file missing — see assertion identity-in-child-worktree"
fi

# Reconcile-on-boot policy assertion (thrum-soj8): the daemon's boot
# reconcile pass treats every identity file on disk as authoritative.
# The stale parent identity at $SUB_REPO/.thrum/identities/${WT_AGENT}.json
# is therefore expected to have a session_ref pointing at $SUB_REPO. This
# is policy: cleanup of stale identities is the operator's job (mv to
# .deleted suffix). Documenting this post-v0.10.1 behavior so a future
# regression that DROPS the row would be caught.
#
# Restart the daemon so reconcile runs with the stale parent file
# already on disk; the parent daemon was started before pre-staging.
"$TE" exec --cwd "$SUB_REPO" --clean -- thrum daemon restart >/dev/null 2>&1 || true
sleep 1
local DB="$SUB_REPO/.thrum/var/messages.db"
if [ -f "$DB" ]; then
  local REF_COUNT
  REF_COUNT=$(sqlite3 "$DB" \
    "SELECT COUNT(*) FROM session_refs sr
       JOIN sessions s ON s.session_id = sr.session_id
      WHERE sr.ref_type='worktree' AND s.ended_at IS NULL
        AND sr.ref_value = '$SUB_REPO'
        AND s.agent_id = '$WT_AGENT';" 2>/dev/null || echo "ERR")
  if [ "$REF_COUNT" = "1" ]; then
    emit_pass "$SID" "stale-parent-reconciled" \
      "reconcile created session_ref for stale parent identity (expected post-v0.10.1)"
  else
    emit_fail "$SID" "stale-parent-reconciled" \
      "expected 1 session_ref for stale parent" "$REF_COUNT" \
      "scenarios/${SID}.test.sh:$LINENO"
  fi
else
  emit_skip "$SID" "stale-parent-reconciled" \
    "messages.db missing at ${DB}; cannot check post-reconcile state"
fi

}  # _run_scenario_108

_run_scenario_108

# Sub-fixture daemon cleanup (au7k discipline).
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  env "THRUM_NAME=$SUB_AGENT" thrum worktree teardown "$WT_NAME" >/dev/null 2>&1 || true
"$TE" exec --cwd "$SUB_REPO" --clean -- \
  thrum daemon stop >/dev/null 2>&1 || true
