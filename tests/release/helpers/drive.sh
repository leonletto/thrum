#!/usr/bin/env bash
# tests/release/helpers/drive.sh — drive panes (send keystrokes) + poll JSONL
# for matching entries. Depends on paths.sh (sourced via helpers/all.sh).

# send_command <pane-name> <text>
# Sends `text` followed by Enter into the named tmux pane via `thrum tmux send`.
# Pane name is the thrum-managed tmux session name (e.g. "coord", "impl").
send_command() {
  local pane="$1"
  shift
  thrum tmux send "$pane" "$*"
}

# wait_for_jsonl_match <repo-path> <jq-filter> [timeout-seconds]
# Polls the agent's JSONL for the first line where <jq-filter> evaluates truthy.
# Echoes the matching line on success, exit 0. Empty + exit 1 on timeout.
# Default timeout: 30s.
wait_for_jsonl_match() {
  local repo="$1" filter="$2" timeout="${3:-30}"
  local jsonl
  if ! jsonl=$(jsonl_for_repo "$repo"); then
    return 1
  fi
  local elapsed=0
  local interval=1
  while [ "$elapsed" -lt "$timeout" ]; do
    local match
    match=$(jq -c "select($filter)" "$jsonl" 2>/dev/null | head -n1 || true)
    if [ -n "$match" ]; then
      printf '%s' "$match"
      return 0
    fi
    sleep "$interval"
    elapsed=$((elapsed + interval))
  done
  return 1
}

# wait_for_session_start <repo-path> [timeout-seconds]
# Specialization: waits for the first SessionStart hook attachment to appear.
# Used after spawning a claude pane to know the SessionStart hooks have flushed.
wait_for_session_start() {
  local repo="$1" timeout="${2:-30}"
  wait_for_jsonl_match "$repo" \
    '.type == "attachment" and .attachment.hookEvent == "SessionStart"' \
    "$timeout" >/dev/null
}
