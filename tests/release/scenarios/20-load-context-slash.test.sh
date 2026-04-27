#!/usr/bin/env bash
# Scenario: load-context-slash (migrates full_test_plan.md § 9.4)
#
# Verifies that typing the /thrum:load-context slash command into a
# claude pane causes claude to invoke `thrum prime` via its Bash tool —
# the literal contract spelled out by the skill body
# (claude-plugin/commands/load-context.md). When that round trip
# completes, the agent is reprimed from saved project + session
# context, restoring the work it was doing pre-compaction.
#
# Why it matters: /thrum:load-context is the primary recovery path
# after auto-compaction. A regression here (slash command not
# recognized, skill body changed and no longer routes to thrum prime,
# Bash tool invocation suppressed) would silently break compaction
# recovery — agents would compact and not re-load their state.
#
# Test approach:
#   1. Create a /tmp/thrum-pre-compact-<identity>-*.md backup via the
#      pre-compact-save-context.sh script — so the precondition the
#      spec § 9.4 expects (a /tmp backup is present at the time
#      /thrum:load-context runs) is actually met. Without this step,
#      we'd be testing /thrum:load-context against an empty backup
#      directory.
#   2. Send the slash command via send_slash_command (drive.sh helper).
#   3. Poll JSONL for an assistant tool_use entry where .name=="Bash"
#      and .input.command starts with "thrum prime". Code-path-anchored
#      evidence the skill ran.
#
# Spec drift note (§ 9.4): the markdown claims /thrum:load-context
# "displays both the thrum context AND the /tmp backup." Inspection of
# the skill body + the prime CLI shows prime does NOT read or render
# /tmp/thrum-pre-compact-*.md files. So the /tmp-backup-display claim
# is not currently true. Tracked in thrum-eq6q (P3) — until the spec
# or skill is reconciled, this scenario asserts only what's actually
# implemented: that the slash command routes to `thrum prime`. We DO
# create the /tmp file as a precondition so the skill-render path can
# observe it if/when thrum-eq6q lands.
#
# Deviation from markdown § 9.4 assertion shape: the spec captures
# the pane and greps free-form claude prose for "thrum context" /
# "/tmp backup" markers. We assert against the underlying tool
# invocation instead, mirroring scenario 05's whoami --json deviation
# rationale: deterministic and grep-friendly vs claude's free-form
# output, and code-path-anchored vs surface-prose-anchored.
#
# Driven against COORD pane (matches markdown). Read-only at the
# fixture level — claude reading its own context doesn't mutate it.
# The /tmp backup file is removed at scenario end.

SID="20-load-context-slash"
PANE="$COORD_PANE"
REPO="$COORD_REPO"
PRECOMPACT_SCRIPT="$THRUM_RELEASE_REPO_ROOT/claude-plugin/scripts/pre-compact-save-context.sh"
BACKUP_GLOB="/tmp/thrum-pre-compact-test_coordinator_main-coordinator-all-*.md"

_run_scenario_20() {

# Step 1: precondition. Create a /tmp backup so the file the spec
# expects is present at slash-command invocation time. Same
# invocation pattern as scenario 19.
rm -f $BACKUP_GLOB 2>/dev/null || true
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$COORD_REPO" --clean -- \
  env -u THRUM_HOME THRUM_NAME=test_coordinator_main bash "$PRECOMPACT_SCRIPT" \
  >/dev/null 2>&1 || true
sleep 1
# shellcheck disable=SC2086 — intentional glob expansion
backup_files=( $BACKUP_GLOB )
if [ ! -e "${backup_files[0]}" ]; then
  emit_fail "$SID" "precondition-backup-present" \
    "/tmp backup matching ${BACKUP_GLOB} created by pre-compact hook" \
    "(no file present after pre-compact invocation)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Step 2: settle the coord pane. COORD's prime output is large +
# scenario 17 left chat content in flight; allow up to 90s.
wait_for_pane_idle "$PANE" 90

# Capture an RFC3339 floor timestamp so we only match NEW assistant
# entries — without this we'd match the original /thrum:prime call
# fired by setup-repo.sh's tmux start. JSONL timestamps are RFC3339Z;
# our floor (no fractional seconds, no Z) sorts lexicographically
# before anything that includes a fractional/Z suffix at the same
# clock-second, which is the desired behavior.
local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

# Step 3: send the slash command via the drive.sh helper.
send_slash_command "$PANE" "/thrum:load-context"

# Step 4: poll JSONL for the routed Bash tool call.
local filter='.type == "assistant"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | type == "array")
        and (.message.content
             | map(select(.type == "tool_use"
                          and .name == "Bash"
                          and (.input.command | tostring | startswith("thrum prime"))))
             | length > 0)'

if wait_for_jsonl_match "$REPO" "$filter" 90 >/dev/null; then
  emit_pass "$SID" "skill-invokes-thrum-prime"
else
  emit_fail "$SID" "skill-invokes-thrum-prime" \
    'assistant tool_use Bash call with command starting "thrum prime" within 90s' \
    "(no matching JSONL entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_20

_run_scenario_20

# Cleanup: remove the /tmp backup we created.
rm -f $BACKUP_GLOB 2>/dev/null || true
