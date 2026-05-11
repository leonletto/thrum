#!/usr/bin/env bash
# thrum-check-inbox.sh — hook script for listener-free message
# delivery. Reads pending spool files from
# <THRUM_DIR>/spool/<AGENT_ID>/ and emits a nudge-style notification
# directing the agent to run `thrum inbox --unread`.
#
# If the agent's tmux session is alive (reported by the daemon via
# `thrum whoami --field tmux_alive`), the hook SILENTLY consumes the
# spool because the tmux nudge path already notified the agent. This
# prevents duplicate notifications when both paths fire.
#
# Output envelope depends on HOOK_EVENT env var set from settings.json:
#   HOOK_EVENT=Stop  → {"decision":"block","reason":"<nudge>"}
#   else             → {"hookSpecificOutput":{"hookEventName":"<event>","additionalContext":"<nudge>"}}
#
# Dependencies: thrum binary, bash 3.2+. No jq, no ps, no tmux.

set -euo pipefail

HOOK_EVENT="${HOOK_EVENT:-PostToolUse}"

# Derive THRUM_DIR from the script's own location (same pattern as
# thrum-startup.sh). This makes the hook CWD-independent: Claude Code
# may fire lifecycle hooks with a CWD that isn't the repo root (e.g.
# subagent workflows, manually-cd'd tmux sessions). Using BASH_SOURCE
# rather than a relative ".thrum" keeps the spool dir correctly
# anchored regardless.
if [[ -z "${THRUM_DIR:-}" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  DEFAULT_THRUM_HOME="$(cd "$SCRIPT_DIR/.." && pwd)"
  THRUM_DIR="${THRUM_HOME:-$DEFAULT_THRUM_HOME}/.thrum"
fi

agent_id="$(thrum whoami --field agent_id 2>/dev/null || true)"
[[ -z "$agent_id" ]] && exit 0

spool_dir="$THRUM_DIR/spool/$agent_id"
[[ -d "$spool_dir" ]] || exit 0

shopt -s nullglob
files=("$spool_dir"/*.json)
[[ ${#files[@]} -eq 0 ]] && exit 0

# thrum-kfn3: defense-in-depth filter for self-echo. The daemon should
# never write a spool entry whose `from` matches the receiving agent —
# but historical bugs in role-group expansion and cross-daemon sync
# mirrors have leaked the sender into Recipients. Drop any self-echo
# files unconditionally so a daemon-side regression can't reach the
# user-visible "New message from @<self>" nudge.
filtered=()
for f in "${files[@]}"; do
  from="$(sed -n 's/.*"from"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$f")"
  if [[ "$from" == "@$agent_id" || "$from" == "$agent_id" ]]; then
    rm -f "$f"
    continue
  fi
  filtered+=("$f")
done
files=("${filtered[@]}")
[[ ${#files[@]} -eq 0 ]] && exit 0

# If tmux is alive, tmux nudge already delivered. Silently consume
# spool files and exit with no output.
tmux_alive="$(thrum whoami --field tmux_alive 2>/dev/null || echo false)"
if [[ "$tmux_alive" == "true" ]]; then
  for f in "${files[@]}"; do rm -f "$f"; done
  exit 0
fi

# Build the nudge text. Parse senders using POSIX tools (no jq).
# Envelope shape: {"msg_id":"...","from":"@sender","received_at":"..."}
count=${#files[@]}
senders="$(
  for f in "${files[@]}"; do
    sed -n 's/.*"from"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$f"
  done | sort -u | paste -sd ',' -
)"

if [[ $count -eq 1 ]]; then
  nudge="New message from $senders -- run \`thrum inbox --unread\` to read"
else
  nudge="$count new messages from $senders -- run \`thrum inbox --unread\` to read"
fi

# Escape user-controlled strings for embedding in JSON output.
# Only backslash and double-quote need escaping; newlines don't appear
# in the nudge text by construction.
escape_json() {
  local s="$1"
  s="${s//\\/\\\\}"
  s="${s//\"/\\\"}"
  printf '%s' "$s"
}

nudge_escaped="$(escape_json "$nudge")"

if [[ "$HOOK_EVENT" == "Stop" ]]; then
  printf '{"decision":"block","reason":"%s"}' "$nudge_escaped"
else
  event_escaped="$(escape_json "$HOOK_EVENT")"
  printf '{"hookSpecificOutput":{"hookEventName":"%s","additionalContext":"%s"}}' "$event_escaped" "$nudge_escaped"
fi

# Consume: delete spool files so the same nudge isn't re-emitted on
# the next hook fire.
for f in "${files[@]}"; do
  rm -f "$f"
done
