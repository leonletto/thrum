#!/usr/bin/env bash
# SessionStart hook: inject `thrum prime` output as additionalContext.
#
# Replaces the prior "Run /thrum:prime" nudge. When an agent is registered
# for this project, we run `thrum prime` here and emit the briefing to
# stdout — Claude Code surfaces hook stdout to the model as a
# <system-reminder>, so the agent receives the full briefing as
# conversation context with no Bash/Read round-trip.
#
# This avoids the 25KB Read-tool truncation that caused coordinator
# briefings (~63KB) to be partial-read, dropping the session-context
# section at the end of the doc.
#
# Hook-level timeout is enforced by plugin.json; this script does not
# need a portable `timeout` wrapper.
#
# Output ordering for a registered agent (top → bottom):
#   1. Identity banner — agent / role / worktree / branch / module
#      (thrum-2qe2). First thing the agent and any user watching the
#      tmux pane see.
#   2. Loud auto-load directive (thrum-xupf). Tells the agent NOT to
#      run /thrum:prime — the briefing is already in context.
#   3. Loud restart-snapshot preamble (existing). Hoisted only when the
#      briefing carries a `# Previous Session Context` block.
#   4. Briefing envelope + full prime output.

set -uo pipefail

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
  # knows to prime manually after registration.
  echo "Run /thrum:prime to load your session context, identity, and any restart snapshots."
  exit 0
fi

# Extract additional banner fields. Each is best-effort: a missing
# field just renders as "unknown" in the banner, never aborts the hook.
AGENT_ROLE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.role // empty' 2>/dev/null || true)
AGENT_WORKTREE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.worktree // empty' 2>/dev/null || true)
AGENT_BRANCH=$(printf '%s' "$WHOAMI_JSON" | jq -r '.branch // empty' 2>/dev/null || true)
AGENT_MODULE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.module // empty' 2>/dev/null || true)

# Agent registered — inject the briefing inline.
PRIME_OUTPUT=$(thrum prime 2>/dev/null || true)

if [ -z "$PRIME_OUTPUT" ]; then
  # Prime failed (daemon down, slow, etc.) — fall back to the manual nudge
  # so session start never blocks on a broken thrum.
  echo "Run /thrum:prime to load your session context, identity, and any restart snapshots."
  echo "(Auto-injection failed — daemon may be unreachable. Run \`thrum daemon status\` to check.)"
  exit 0
fi

# 1. Identity banner. First thing the agent's context window shows AND
# the first thing a human watching the tmux pane sees when the hook
# fires. Markdown header keeps it scannable in both surfaces.
cat <<EOF
# 🎯 You are: @${AGENT_ID}

- **Role:** ${AGENT_ROLE:-unknown}
- **Worktree:** ${AGENT_WORKTREE:-unknown}
- **Branch:** ${AGENT_BRANCH:-unknown}
EOF
# Module is optional — only show it when set and it isn't a redundant
# echo of role (some setups default module=role).
if [ -n "$AGENT_MODULE" ] && [ "$AGENT_MODULE" != "$AGENT_ROLE" ]; then
  echo "- **Module:** ${AGENT_MODULE}"
fi
cat <<'EOF'

---

EOF

# 2. Loud auto-load directive. The agent should NOT consider running
# /thrum:prime or `thrum prime` again — the briefing is in context.
# Blockquote framing matches the heaviness of the restart-snapshot
# block below so this directive isn't visually outranked.
cat <<'EOF'
> ✅ **Context auto-loaded by SessionStart hook.**
>
> **Do NOT run `/thrum:prime` or `thrum prime` — the full briefing is already in your context below.**
> Only invoke them manually if this hook fell through to a degraded "auto-injection failed" notice.

EOF

# 3. Detect a restart snapshot embedded in the briefing. `thrum prime`
# includes it under the `# Previous Session Context` heading when
# `.thrum/restart/<agent>.md` exists. Without prominent framing, the
# agent treats it as background reading and skips the actionable
# Resume Plan inside. Hoist a loud action-required block above the
# briefing so the directive is impossible to miss.
HAS_RESTART=0
if printf '%s' "$PRIME_OUTPUT" | grep -q '^# Previous Session Context'; then
  HAS_RESTART=1
fi

if [ "$HAS_RESTART" = "1" ]; then
  cat <<'EOF'
# 🛑 ACTION REQUIRED — Instructions From Your Previous Session

**You restarted from a prior session and left yourself a Resume Plan.** It is in the **`# Previous Session Context`** section of the briefing below. That plan is not background reading — it is your own message-to-self with concrete next steps.

**Before doing anything else:**

1. Scroll to the `# Previous Session Context` section of the briefing.
2. Read the **`## Resume Plan`** sub-section in full.
3. Execute its numbered steps in order.
4. Only then continue to the rest of the briefing or the user's prompt.

The Resume Plan was written by *you* in the previous session specifically because you knew this future you would need it. Trust it and act on it.

---

EOF
fi

# 4. Briefing envelope + full prime output.
cat <<EOF
# Thrum Session Briefing (auto-loaded)

The complete \`thrum prime\` output is included below. You do NOT need to run \`/thrum:prime\` or \`thrum prime\` again this session — the briefing is already in your context. Read it in full; the session context section at the end is the most important.

Only spawn additional commands if the inbox section shows unread messages that need processing.

---

$PRIME_OUTPUT
EOF
