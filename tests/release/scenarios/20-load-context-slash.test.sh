#!/usr/bin/env bash
# Scenario: load-context-slash (migrates full_test_plan.md § 9.4)
#
# Verifies that typing the /thrum:load-context slash command into a
# claude pane is RECOGNIZED and ROUTED — i.e. Claude Code's UI
# converts the keystrokes into a `<command-name>/thrum:load-context
# </command-name>` user message in the agent's JSONL. That's the
# routing contract the MCP→CLI parity story depends on; everything
# downstream of the slash registration (skill body running thrum
# prime, prime rendering session context) is covered by scenario 21
# which invokes the slash command on a freshly-restarted pane where
# claude doesn't optimize.
#
# Why ROUTING-only here (not skill-body execution): under the
# fixture's claude-opus model, when the IMPL pane already has a
# warm SessionStart briefing in context, opus reasons "no need to
# re-run thrum prime — context already loaded" and replies in chat
# instead of executing the skill body. Observed in fixture: the
# pane shows `❯ /thrum:load-context` followed by `⏺ Session context
# already loaded via SessionStart hook briefing — no need to re-run`.
# That's a valid model-side optimization, not a regression. Asserting
# on the tool_use call would couple this scenario to a particular
# model's eagerness to obey skill bodies and produce flakes that
# look like routing failures but aren't. Routing-only assertion
# eliminates the model-eagerness coupling.
#
# Why it matters: /thrum:load-context is the primary recovery path
# after auto-compaction. A regression in slash-command registration
# (skill file deleted, plugin not installed, slash parsing broken)
# would surface here as a missing `<command-name>` user message.
# Skill-body-execution regressions (skill body no longer says
# `thrum prime`) are covered by scenario 21's post-restart variant.
#
# Test approach:
#   1. Create a /tmp/thrum-pre-compact-<identity>-*.md backup via the
#      pre-compact-save-context.sh script — so the precondition the
#      spec § 9.4 expects (a /tmp backup is present at the time
#      /thrum:load-context runs) is actually met. Without this step,
#      we'd be testing /thrum:load-context against an empty backup
#      directory.
#   2. Send the slash command via send_slash_command (drive.sh helper).
#   3. Poll JSONL for a user message whose .message.content contains
#      `<command-name>/thrum:load-context</command-name>` — Claude
#      Code's slash-routing signature.
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
# Pane choice: IMPL pane. The spec § 9.4 drives coordinator, but
# scenario 17 (now exercising the heavyweight /thrum:update-project
# skill — Agent + multiple Edit tool calls) leaves COORD mid-render
# for 60-180s post-completion, which intermittently caused 20's
# slash keystrokes to land during render and miss the slash
# mode-switch (observed flake under full-suite ordering). IMPL was
# last directly driven in scenario 07 and has been quiet since,
# giving a deterministic settled pane state. The contract under
# test (slash command → thrum prime tool_use) is pane-agnostic, so
# the deviation doesn't affect coverage.
#
# Read-only at the fixture level — claude reading its own context
# doesn't mutate it. The /tmp backup file is removed at scenario
# end. Scenario 21 immediately after this restarts IMPL anyway, so
# any post-slash render state in IMPL is wiped before later
# scenarios run.

SID="20-load-context-slash"
PANE="$IMPL_PANE"
REPO="$IMPL_REPO"
PRECOMPACT_SCRIPT="$THRUM_RELEASE_REPO_ROOT/claude-plugin/scripts/pre-compact-save-context.sh"
# /tmp backup filename now keys on IMPL agent identity (test_implementer
# + implementer + all). Match scenario 19's pattern adjusted for the
# impl identity.
BACKUP_GLOB="/tmp/thrum-pre-compact-test_implementer-implementer-all-*.md"

_run_scenario_20() {

# Step 1: precondition. Create a /tmp backup so the file the spec
# expects is present at slash-command invocation time. Same
# invocation pattern as scenario 19.
rm -f $BACKUP_GLOB 2>/dev/null || true
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$REPO" --clean -- \
  env -u THRUM_HOME THRUM_NAME=test_implementer bash "$PRECOMPACT_SCRIPT" \
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

# Step 2: settle the impl pane. IMPL has been quiet since scenario
# 07; allow 60s for any residual render to flush.
wait_for_pane_idle "$PANE" 60

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

# Step 4: poll JSONL for the slash-routing signature — a user
# message whose content contains `<command-name>/thrum:load-context
# </command-name>`. Claude Code's UI emits this exact tag when it
# recognizes a slash command and converts the keystrokes into a
# routed user message; absence means the slash didn't register.
local filter='.type == "user"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | tostring | contains("<command-name>/thrum:load-context</command-name>"))'

if wait_for_jsonl_match "$REPO" "$filter" 60 >/dev/null; then
  emit_pass "$SID" "slash-command-registered"
else
  emit_fail "$SID" "slash-command-registered" \
    'user message containing "<command-name>/thrum:load-context</command-name>" within 60s' \
    "(no matching JSONL entry — slash command did not register)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_20

_run_scenario_20

# Cleanup: remove the /tmp backup we created.
rm -f $BACKUP_GLOB 2>/dev/null || true
