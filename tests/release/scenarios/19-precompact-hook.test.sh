#!/usr/bin/env bash
# Scenario: precompact-hook (migrates full_test_plan.md § 9.3)
#
# Verifies the PreCompact hook script (claude-plugin/scripts/
# pre-compact-save-context.sh) writes a /tmp backup file whose
# filename embeds the agent identity (test_coordinator_main /
# coordinator / all) and whose body contains the expected
# state-snapshot sections (git, beads, thrum status).
#
# Why it matters: this hook is the safety net that captures
# mechanical state right before claude auto-compacts. Without it,
# a compacted session loses the git/beads/thrum context that the
# narrative summary in /thrum:update-project doesn't cover.
#
# Test approach (per markdown § 9.3 + dispatch guidance — "Easiest
# of the 5"): invoke the script directly via bash with controlled
# env. No claude involvement — the script is plain bash, the test
# scrubs THRUM_HOME (per the markdown env note) and pins
# THRUM_NAME=test_coordinator_main so the script's `thrum whoami`
# resolves the right agent inside the fixture.
#
# Backup filename pattern (from script tail):
#   /tmp/thrum-pre-compact-${AGENT_NAME}-${AGENT_ROLE}-${AGENT_MODULE}-<epoch>.md
# For our fixture: thrum-pre-compact-test_coordinator_main-coordinator-all-*.md
#
# Cleanup-after-return: the script tail itself prunes prior backups
# for the same identity, but we additionally remove the file at
# scenario end so subsequent runs start clean.
#
# Read-only at the fixture level — the script writes to /tmp + the
# coord agent's thrum context (the latter is non-disruptive: a
# pre-compact append, not a wipe).

SID="19-precompact-hook"
SCRIPT="$THRUM_RELEASE_REPO_ROOT/claude-plugin/scripts/pre-compact-save-context.sh"
BACKUP_GLOB="/tmp/thrum-pre-compact-test_coordinator_main-coordinator-all-*.md"

_run_scenario_19() {

if [ ! -f "$SCRIPT" ] || [ ! -x "$SCRIPT" ]; then
  emit_fail "$SID" "script-present" "pre-compact-save-context.sh at $SCRIPT (executable)" \
    "(file missing or not executable)" "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Pre-clean any backup from a prior run.
rm -f $BACKUP_GLOB 2>/dev/null || true

# Invoke per markdown § 9.3: env -u THRUM_HOME (per env-note guidance —
# leak from caller would resolve identity to wrong repo) + THRUM_NAME
# pinned so the script's whoami probe lands the right agent.
# tmux-exec breaks the PID ancestry chain (see setup-repo.sh rationale),
# matching how a real plugin invocation would be a fresh shell with no
# claude in the parent chain.
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean -- \
  env -u THRUM_HOME THRUM_NAME=test_coordinator_main bash "$SCRIPT" \
  >/dev/null 2>&1 || true

# Allow filesystem flush before globbing.
sleep 1

# Assertion 1: backup file exists with the identity-shaped filename.
# shellcheck disable=SC2086 — intentional glob expansion
backup_files=( $BACKUP_GLOB )
if [ -e "${backup_files[0]}" ]; then
  emit_pass "$SID" "backup-file-present"
  BACKUP_FILE="${backup_files[0]}"
else
  emit_fail "$SID" "backup-file-present" \
    "backup file matching ${BACKUP_GLOB}" \
    "(no file present after invocation)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Assertion 2: body contains git state section.
if grep -q "Git State" "$BACKUP_FILE"; then
  emit_pass "$SID" "body-has-git-section"
else
  emit_fail "$SID" "body-has-git-section" \
    "backup body containing 'Git State' heading" \
    "(heading missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 3: body contains beads state section.
if grep -q "Beads State" "$BACKUP_FILE"; then
  emit_pass "$SID" "body-has-beads-section"
else
  emit_fail "$SID" "body-has-beads-section" \
    "backup body containing 'Beads State' heading" \
    "(heading missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Assertion 4: body contains thrum agent status section.
if grep -q "Thrum Agent Status" "$BACKUP_FILE"; then
  emit_pass "$SID" "body-has-thrum-section"
else
  emit_fail "$SID" "body-has-thrum-section" \
    "backup body containing 'Thrum Agent Status' heading" \
    "(heading missing)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_19

_run_scenario_19

# Teardown: remove the backup file we just created. `|| true` so a
# missing-glob doesn't pollute EXIT.
rm -f $BACKUP_GLOB 2>/dev/null || true
