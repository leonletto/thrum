#!/usr/bin/env bash
# Scenario: context-persists-restart (migrates full_test_plan.md § 9.5)
#
# Verifies thrum context survives a session kill + restart AND that
# the post-restart agent can recover that context via the
# /thrum:load-context slash command. Two sub-assertions:
#
#   1. context-survives-restart-cli — the storage-layer contract:
#      a marker saved before the restart is still readable via
#      `thrum context show --session` after the pane has been torn
#      down and recreated. Agent-identity-keyed (not session-scoped)
#      persistence.
#
#   2. context-survives-restart-slash — the user-facing recovery
#      chain: the post-restart pane runs /thrum:load-context, which
#      routes to `thrum prime`, whose bash-stdout includes the
#      restored session context — and that context still carries
#      the marker we saved pre-restart. Closes the chain the spec's
#      § 9.5 last `tmux send-keys ... /thrum:load-context` invocation
#      explicitly tests ("agent aware of previous work").
#
# Why both: scenario 20 covers /thrum:load-context invocation in
# isolation (no restart). Scenario 21 specifically tests the
# combined restart+recovery path the spec § 9.5 documents. The CLI
# sub-assertion catches storage-layer regressions; the slash
# sub-assertion catches skill-routing regressions in the
# post-restart context.
#
# Test approach:
#   1. Save a unique marker into the IMPL agent's context via
#      tmux-exec (out of pane → deterministic CLI write via --file).
#   2. Restart the IMPL pane via `thrum tmux restart impl --force`
#      (mirrors scenario 02's restart mechanism).
#   3. Wait for the new SessionStart attachment (proves the pane
#      came back up, claude is alive).
#   4. CLI sub-assertion: read context back via tmux-exec → assert
#      marker present.
#   5. Slash sub-assertion: send /thrum:load-context to the new
#      pane → assert claude invokes `thrum prime` AND that the
#      bash-stdout of that invocation contains the marker.
#
# Deviation from markdown § 9.5: spec uses bare `tmux kill-session`
# + `tmux new-session` to nuke and rebuild the coord pane manually.
# We use `thrum tmux restart impl --force` because (i) it's the
# framework-supported restart path that real users invoke,
# (ii) it preserves the snapshot/restore plumbing scenarios 02/03
# already cover, and (iii) a manual kill+new-session would orphan
# the daemon's session bookkeeping and could corrupt subsequent
# runs. We use IMPL pane instead of COORD because COORD was just
# driven by scenarios 17 and 20 — IMPL gives a cleaner pane state
# and parallels the impl-restart pattern from scenario 02.
#
# Fixture mutation: writes context for test_implementer, restarts
# impl pane. Other scenarios that depend on impl pane stability
# should run BEFORE 21.

SID="21-context-persists-restart"
PANE="$IMPL_PANE"
REPO="$IMPL_REPO"
MARKER="kafm5-21-marker-${RUNID}"

_run_scenario_21() {

# Pre-step: save the marker into IMPL's context store via --file
# (tmux-exec runs commands inside an ephemeral pane, so shell-pipe
# stdin doesn't reach the inner command).
marker_file="$(mktemp -t kafm5-21.XXXXXX)"
printf '%s\n' "$MARKER" > "$marker_file"
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$REPO" --clean -- \
  env THRUM_NAME=test_implementer thrum context save --file "$marker_file" \
  >/dev/null 2>&1 || true
rm -f "$marker_file"

# Sanity check the precondition: the marker should be in IMPL's
# context BEFORE the restart. If this fails we can't tell whether
# the restart broke persistence or save itself broke.
local pre_check
pre_check="$(
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$REPO" --clean -- \
    env THRUM_NAME=test_implementer thrum context show --session 2>&1 || true
)"
if ! echo "$pre_check" | grep -q "$MARKER"; then
  emit_fail "$SID" "marker-saved-precondition" \
    "context show --session output containing '${MARKER}' BEFORE restart" \
    "(marker not present in pre-restart show output)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Restart IMPL via the framework-supported restart path. Driver-side
# thrum calls must wrap through tmux-exec to break the PID chain
# (same rationale as scenario 02).
"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$REPO" --clean --timeout 60 -- \
  thrum tmux restart "$IMPL_PANE" --force >/dev/null 2>&1 || true

# Wait for the NEW SessionStart attachment to land in IMPL JSONL.
# Same race-condition guard as scenarios 02/03: 5s sleep before
# polling so the new claude has time to create its new JSONL file.
sleep 5
if ! wait_for_session_start "$REPO" 60; then
  emit_fail "$SID" "post-restart-session-start" \
    "new SessionStart attachment within 60s of restart" \
    "(none observed)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Sub-assertion 1: storage-layer survival via CLI. Same out-of-pane
# tmux-exec invocation as the pre-check — proves agent-identity-
# keyed persistence (not session-scoped).
local post_check
post_check="$(
  "$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec --cwd "$REPO" --clean -- \
    env THRUM_NAME=test_implementer thrum context show --session 2>&1 || true
)"
if echo "$post_check" | grep -q "$MARKER"; then
  emit_pass "$SID" "context-survives-restart-cli"
else
  local got
  got="$(echo "$post_check" | tr '\n' ' ' | head -c 240)"
  emit_fail "$SID" "context-survives-restart-cli" \
    "context show --session output containing '${MARKER}' AFTER restart" \
    "${got:-<empty output>}" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

# Sub-assertion 2: user-facing recovery chain via /thrum:load-context.
# The post-restart pane auto-runs /thrum:prime as part of its boot
# (handled by scenario 02/03's restart-snapshot path), so we settle
# the pane before sending another slash command to avoid keystroke
# overlap with the auto-prime render.
wait_for_pane_idle "$PANE" 60

# Banner-race guard (thrum-6hqy.1): the daemon's HandleRestart
# spawns a post-launch goroutine that, ~10s after launchCmd plus
# waitForPaneReady time, sends the identity banner printf into the
# pane via sendKeysAndSubmit (internal/daemon/rpc/tmux.go:2304-2336).
# Without syncing on banner-emit completion, send_slash_command's
# text->Enter gap can overlap the banner emit's keystrokes,
# splicing the banner's `printf '%s\n' 'Agent: @...'` content INTO
# the in-flight `/thrum:load-context` text — producing concatenated
# input like `/thrum:load-contextprintf '%s\n' 'Agent: @...'` that
# claude rejects as "Unknown command" (root cause of this scenario's
# intermittent context-survives-restart-slash failures observed
# across iterations on the v0.10.6 release-test harness).
#
# Tolerate timeout silently — if the banner truly never fires (e.g.
# initial-launch sessionCwds skip, thrum-gdf8-class behavior on a
# different pane), the downstream tool_use assertion will catch the
# actual missing behavior with a clearer signal than a bare timeout
# here. Brief settle after the sentinel lands so claude finishes
# responding to the printf before we type into the same input.
wait_for_banner_emit "$PANE" 45 || true
wait_for_pane_idle "$PANE" 30

# Capture an RFC3339 floor timestamp scoped to AFTER the auto-prime
# settle so we only match the /thrum:load-context invocation, not
# the post-restart auto-prime.
local floor_ts
floor_ts="$(date -u +%Y-%m-%dT%H:%M:%S)"

send_slash_command "$PANE" "/thrum:load-context"

# Poll for the assistant tool_use Bash call to a context-loading
# command whose bash-stdout (delivered as the tool_result) contains
# the marker. The /thrum:load-context skill body
# (claude-plugin/commands/load-context.md) offers TWO commands as
# of v0.10.6:
#
#   - `thrum prime` — full briefing (project state + session context)
#   - `thrum context show --session` — session-only payload (the
#     marker-bearing slice)
#
# Post-SessionStart-hook, claude routinely picks the session-only
# command since the full briefing was already auto-injected by the
# hook — a rational choice that the skill body explicitly enables.
# Either tool_use satisfies the assertion's INTENT (slash command
# routes to autonomous bash invocation of context restoration).
#
# We can't filter on the tool_result content from the assistant's
# tool_use entry alone — instead, wait for the tool_use first to
# confirm the slash command routed correctly, then poll a separate
# user-message entry whose content is the bash-stdout containing
# the marker. Two-stage polling keeps the jq filters readable.
local tool_filter='.type == "assistant"
        and (.timestamp >= "'"$floor_ts"'")
        and (.message.content | type == "array")
        and (.message.content
             | map(select(.type == "tool_use"
                          and .name == "Bash"
                          and ((.input.command | tostring | startswith("thrum prime"))
                               or (.input.command | tostring | startswith("thrum context show")))))
             | length > 0)'
if ! wait_for_jsonl_match "$REPO" "$tool_filter" 90 >/dev/null; then
  emit_fail "$SID" "context-survives-restart-slash" \
    'assistant tool_use Bash call to thrum prime OR thrum context show within 90s after /thrum:load-context' \
    "(no matching JSONL entry)" \
    "scenarios/${SID}.test.sh:$LINENO"
  return 0
fi

# Now poll for the bash-stdout of that invocation containing the
# marker. The tool_result lands as a user message whose content
# starts with the prime CLI output (RFC3339 timestamp >= floor_ts
# again, scoped to NEW user entries).
local result_filter='.type == "user"
        and (.timestamp >= "'"$floor_ts"'")
        and ((.toolUseResult.stdout // "") | tostring | contains("'"$MARKER"'"))'
if wait_for_jsonl_match "$REPO" "$result_filter" 60 >/dev/null; then
  emit_pass "$SID" "context-survives-restart-slash"
else
  emit_fail "$SID" "context-survives-restart-slash" \
    "thrum prime tool_result stdout containing '${MARKER}' within 60s" \
    "(prime ran but marker not in its rendered output)" \
    "scenarios/${SID}.test.sh:$LINENO"
fi

}  # _run_scenario_21

_run_scenario_21
