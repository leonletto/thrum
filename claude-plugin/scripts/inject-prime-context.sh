#!/usr/bin/env bash
# SessionStart hook: inject `thrum prime` output as additionalContext.
#
# Replaces the prior "Run /thrum:prime" nudge. When an agent is registered
# for this project, we run `thrum prime` and emit the briefing through
# Claude Code's documented JSON output protocol —
#   {"hookSpecificOutput":{"hookEventName":"SessionStart",
#                          "additionalContext": "<assembled body>"}}
# `additionalContext` is routed straight into the model's context window
# with no size truncation and no persisted-output indirection. Plain
# stdout >2KB is otherwise persisted to a tool-results file with only the
# first 2KB previewed inline (thrum-tfrv: coordinator briefings around
# 60KB were dropping the entire session-context section past the cutoff).
#
# Hook-level timeout is enforced by plugin.json; this script does not
# need a portable `timeout` wrapper.
#
# Output ordering for a registered agent (top → bottom):
#   1. Identity banner — agent / role / worktree / branch / module
#      (thrum-2qe2). First thing the agent sees in context.
#   2. Loud auto-load directive (thrum-xupf). Tells the agent NOT to
#      run /thrum:prime — the briefing is already in context.
#   3. Loud restart-snapshot preamble (existing). Hoisted only when the
#      briefing carries a `# Previous Session Context` block.
#   4. Briefing envelope + full prime output.

set -uo pipefail

# emit_context "<body>" — final emission step. Prefers the JSON
# protocol (Claude Code routes additionalContext directly into the
# model's context window, no size cap). Falls back to plain stdout
# when jq isn't available — that path retains the historical
# system-reminder routing with the documented 2KB inline preview
# (degraded mode; we'd rather ship a partial briefing than abort
# session start). thrum-tfrv.
emit_context() {
  local body="$1"
  if command -v jq >/dev/null 2>&1; then
    jq -n --arg ctx "$body" \
      '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $ctx}}'
  else
    printf '%s' "$body"
  fi
}

# Project doesn't use thrum — silent no-op.
if ! command -v thrum >/dev/null 2>&1; then
  exit 0
fi

# Capture whoami JSON ONCE, extract identity fields downstream. The
# script ran a single `thrum whoami --json` previously; keeping the
# RPC count at one preserves session-start latency.
WHOAMI_JSON=""
AGENT_ID=""
if command -v jq >/dev/null 2>&1; then
  WHOAMI_JSON=$(thrum whoami --json 2>/dev/null || true)
  AGENT_ID=$(printf '%s' "$WHOAMI_JSON" \
    | jq -r 'select(.agent_id != null) | .agent_id // empty' 2>/dev/null \
    || true)
fi

if [ -z "$AGENT_ID" ]; then
  # No agent registered — preserve historical nudge so the user/agent
  # knows to prime manually after registration. Wrapped in the JSON
  # envelope so all branches share one protocol shape.
  emit_context "Run /thrum:prime to load your session context, identity, and any restart snapshots."
  exit 0
fi

# Extract additional banner fields. Each is best-effort: a missing
# field just renders as "unknown" in the banner, never aborts the hook.
AGENT_ROLE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.role // empty' 2>/dev/null || true)
AGENT_WORKTREE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.worktree // empty' 2>/dev/null || true)
AGENT_BRANCH=$(printf '%s' "$WHOAMI_JSON" | jq -r '.branch // empty' 2>/dev/null || true)
AGENT_MODULE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.module // empty' 2>/dev/null || true)

# Agent registered — assemble the full briefing body in $ASSEMBLED so
# we can wrap it in the JSON envelope at the end (rather than emitting
# piecemeal to stdout, which would be size-truncated above ~2KB).
PRIME_OUTPUT=$(thrum prime 2>/dev/null || true)

if [ -z "$PRIME_OUTPUT" ]; then
  # Prime failed (daemon down, slow, etc.) — fall back to the manual
  # nudge so session start never blocks on a broken thrum. JSON
  # envelope same as the no-agent branch above for protocol uniformity.
  fallback_body=$'Run /thrum:prime to load your session context, identity, and any restart snapshots.\n(Auto-injection failed — daemon may be unreachable. Run `thrum daemon status` to check.)'
  emit_context "$fallback_body"
  exit 0
fi

# Build $ASSEMBLED via incremental append. printf-into-variable rather
# than the previous `cat <<EOF` to-stdout pattern — same content, but
# now redirected through emit_context so the >2KB truncation gate
# can't fire.
ASSEMBLED=""
append() { ASSEMBLED+="$1"; }

# 1. Identity banner.
append "# 🎯 You are: @${AGENT_ID}"$'\n'
append $'\n'
append "- **Role:** ${AGENT_ROLE:-unknown}"$'\n'
append "- **Worktree:** ${AGENT_WORKTREE:-unknown}"$'\n'
append "- **Branch:** ${AGENT_BRANCH:-unknown}"$'\n'
# Module is optional — only show it when set and it isn't a redundant
# echo of role (some setups default module=role).
if [ -n "$AGENT_MODULE" ] && [ "$AGENT_MODULE" != "$AGENT_ROLE" ]; then
  append "- **Module:** ${AGENT_MODULE}"$'\n'
fi
append $'\n---\n\n'

# 2. Loud auto-load directive. The agent should NOT consider running
# /thrum:prime or `thrum prime` again — the briefing is in context.
# Blockquote framing matches the heaviness of the restart-snapshot
# block below so this directive isn't visually outranked.
append '> ✅ **Context auto-loaded by SessionStart hook.**'$'\n'
append '>'$'\n'
append '> **Do NOT run `/thrum:prime` or `thrum prime` — the full briefing is already in your context below.**'$'\n'
append '> Only invoke them manually if this hook fell through to a degraded "auto-injection failed" notice.'$'\n'
append $'\n'

# 3. Detect a restart snapshot embedded in the briefing. `thrum prime`
# includes it under the `# Previous Session Context` heading when
# `.thrum/restart/<agent>.md` exists. Without prominent framing, the
# agent treats it as background reading and skips the actionable
# Resume Plan inside. Hoist a loud action-required block above the
# briefing so the directive is impossible to miss.
if printf '%s' "$PRIME_OUTPUT" | grep -q '^# Previous Session Context'; then
  append '# 🛑 ACTION REQUIRED — Instructions From Your Previous Session'$'\n'
  append $'\n'
  append '**You restarted from a prior session and left yourself a Resume Plan.** It is in the **`# Previous Session Context`** section of the briefing below. That plan is not background reading — it is your own message-to-self with concrete next steps.'$'\n'
  append $'\n'
  append '**Before doing anything else:**'$'\n'
  append $'\n'
  append '1. Scroll to the `# Previous Session Context` section of the briefing.'$'\n'
  append '2. Read the **`## Resume Plan`** sub-section in full.'$'\n'
  append '3. Execute its numbered steps in order.'$'\n'
  append '4. Only then continue to the rest of the briefing or the user'\''s prompt.'$'\n'
  append $'\n'
  append 'The Resume Plan was written by *you* in the previous session specifically because you knew this future you would need it. Trust it and act on it.'$'\n'
  append $'\n---\n\n'
fi

# 4. Briefing envelope + full prime output.
append '# Thrum Session Briefing (auto-loaded)'$'\n'
append $'\n'
append 'The complete `thrum prime` output is included below. You do NOT need to run `/thrum:prime` or `thrum prime` again this session — the briefing is already in your context. Read it in full; the session context section at the end is the most important.'$'\n'
append $'\n'
append 'Only spawn additional commands if the inbox section shows unread messages that need processing.'$'\n'
append $'\n---\n\n'
append "$PRIME_OUTPUT"$'\n'

emit_context "$ASSEMBLED"
