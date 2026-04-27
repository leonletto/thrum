#!/usr/bin/env bash
# tests/release/helpers/drive.sh — drive panes (send keystrokes) + poll JSONL
# for matching entries. Depends on paths.sh (sourced via helpers/all.sh).

# send_command <pane-name> <text>
# Sends `text` followed by Enter into the named tmux pane.
# Pane name is the tmux session name (e.g. "coord", "impl").
#
# Uses raw `tmux send-keys` (not `thrum tmux send`) for two reasons:
#
#   1. `thrum tmux send` delivers text-only (no Enter), so the next send
#      would concatenate on the same input line and never execute.
#
#   2. Claude Code's bash-prefix mode (where `! cmd` runs as a sub-shell
#      and emits `<bash-stdout>...</bash-stdout>` markers — what
#      assert_jsonl reads) is a keystroke-time UI switch: typing `!` as
#      the FIRST character changes input mode BEFORE the rest is typed.
#      Sending the whole `! cmd` line in one batch makes Claude treat it
#      as chat content and miss the bash-prefix entirely. Sending `!`
#      discretely (with a brief pause for the UI to switch), then the
#      rest of the line, triggers the mode change correctly.
send_command() {
  local pane="$1"
  shift
  local text="$*"
  if [[ "$text" == "! "* ]]; then
    tmux send-keys -t "$pane" "!"
    sleep 0.3
    tmux send-keys -t "$pane" "${text#! }" Enter
  else
    tmux send-keys -t "$pane" "$text" Enter
  fi
}

# wait_for_jsonl_match <repo-path> <jq-filter> [timeout-seconds]
# Polls all .jsonl files under the agent's Claude project dir for the first
# line where <jq-filter> evaluates truthy. Echoes the matching line on success,
# exit 0. Empty + exit 1 on timeout. Default timeout: 30s.
#
# Searches ALL .jsonl files in the project dir (not just the newest) because
# Claude Code can write multiple JSONLs in parallel — a main-session file
# plus agent-*.jsonl files for sub-agent runs (e.g. SessionStart hooks that
# spawn skill agents). The entry we want may be in any of them, and the
# "newest by mtime" at poll-start may not be the one that ends up carrying
# the bash-stdout entry once the conversation lands.
wait_for_jsonl_match() {
  local repo="$1" filter="$2" timeout="${3:-30}"
  local project_dir="$HOME/.claude/projects/$(encode_cwd "$repo")"
  local elapsed=0
  local interval=1
  while [ "$elapsed" -lt "$timeout" ]; do
    if [ -d "$project_dir" ]; then
      local match
      match=$(jq -c "select($filter)" "$project_dir"/*.jsonl 2>/dev/null | head -n1 || true)
      if [ -n "$match" ]; then
        printf '%s' "$match"
        return 0
      fi
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
