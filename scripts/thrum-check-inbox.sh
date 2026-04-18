#!/usr/bin/env bash
# thrum-check-inbox.sh — prototype hook script for listener-free message
# delivery. Reads pending spool files from .thrum/spool/ and emits a
# nudge-style prompt directing the agent to run `thrum inbox --unread`.
#
# Output format mirrors the tmux nudge so behavior is identical across
# both delivery paths. We do NOT inject message bodies — the daemon
# remains the canonical message store, and the agent reads via
# `thrum inbox` which also handles mark-read correctly.
#
# Behavior depends on the calling event (set via HOOK_EVENT env from
# settings.json):
#   HOOK_EVENT=Stop  → emit {"decision":"block","reason":"<nudge>"}
#                      so the Stop is countermanded and the agent sees
#                      the nudge as the next prompt.
#   else             → emit {"hookSpecificOutput":{"hookEventName":"<event>",
#                            "additionalContext":"<nudge>"}}
#                      so the runtime injects the nudge as added context.
#
# Spool envelope (one file per message, written by daemon — prototype
# files are dropped by hand for now):
#   { "msg_id": "...", "from": "@sender", "body": "...",
#     "received_at": "ISO-8601" }
#
# Idempotent: each spool file is deleted after it's surfaced.

set -euo pipefail

HOOK_EVENT="${HOOK_EVENT:-PostToolUse}"

thrum_dir="${THRUM_DIR:-.thrum}"
spool_dir="$thrum_dir/spool"
[[ -d "$spool_dir" ]] || exit 0

shopt -s nullglob
files=("$spool_dir"/*.json)
[[ ${#files[@]} -eq 0 ]] && exit 0

# Build the nudge text. Mirror tmux nudge format closely so agents
# learn one phrase, not two.
count=${#files[@]}
senders="$(for f in "${files[@]}"; do jq -r '.from // "?"' "$f"; done | sort -u | paste -sd ',' -)"

if [[ $count -eq 1 ]]; then
  nudge="New message from $senders -- run \`thrum inbox --unread\` to read"
else
  nudge="$count new messages from $senders -- run \`thrum inbox --unread\` to read"
fi

if [[ "$HOOK_EVENT" == "Stop" ]]; then
  out="$(jq -nc --arg r "$nudge" '{decision:"block",reason:$r}')"
else
  out="$(jq -nc --arg e "$HOOK_EVENT" --arg c "$nudge" \
    '{hookSpecificOutput:{hookEventName:$e,additionalContext:$c}}')"
fi
printf '%s' "$out"

# Consume: delete spool files so the same nudge isn't re-emitted on the
# next hook fire. (Real implementation: daemon-side cleanup keyed off
# `thrum message read` would replace this.)
for f in "${files[@]}"; do
  rm -f "$f"
done
