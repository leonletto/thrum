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

set -uo pipefail

# Project doesn't use thrum — silent no-op.
if ! command -v thrum >/dev/null 2>&1; then
  exit 0
fi

# Is an agent registered for this cwd?
AGENT_ID=""
if command -v jq >/dev/null 2>&1; then
  AGENT_ID=$(thrum whoami --json 2>/dev/null \
    | jq -r 'select(.agent_id != null) | .agent_id // empty' 2>/dev/null \
    || true)
fi

if [ -z "$AGENT_ID" ]; then
  # No agent registered — preserve historical nudge so the user/agent
  # knows to prime manually after registration.
  echo "Run /thrum:prime to load your session context, identity, and any restart snapshots."
  exit 0
fi

# Agent registered — inject the briefing inline.
PRIME_OUTPUT=$(thrum prime 2>/dev/null || true)

if [ -z "$PRIME_OUTPUT" ]; then
  # Prime failed (daemon down, slow, etc.) — fall back to the manual nudge
  # so session start never blocks on a broken thrum.
  echo "Run /thrum:prime to load your session context, identity, and any restart snapshots."
  echo "(Auto-injection failed — daemon may be unreachable. Run \`thrum daemon status\` to check.)"
  exit 0
fi

cat <<EOF
# Thrum Session Briefing (auto-loaded)

The complete \`thrum prime\` output is included below. You do NOT need to run \`/thrum:prime\` or \`thrum prime\` again this session — the briefing is already in your context. Read it in full; the session context section at the end is the most important.

Only spawn additional commands if the inbox section shows unread messages that need processing.

---

$PRIME_OUTPUT
EOF
