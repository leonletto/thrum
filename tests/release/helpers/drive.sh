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
  # Wait for the pane to be idle (no rendering activity) before sending.
  # When scenarios fire ! commands rapid-fire, the next ! can land while
  # claude is still rendering the previous response. The ! must hit at
  # column 0 of an empty input line to trigger Claude Code's bash-prefix
  # mode; if it lands during a render, it gets typed as a literal char
  # in the input area instead and bash mode never engages. Measured: a
  # bash command in coord/impl finishes rendering in ~1.4s.
  wait_for_pane_idle "$pane" 10
  # Send text and Enter in SEPARATE tmux send-keys calls with a brief pause.
  # Bundling them as `tmux send-keys -t <pane> "<text>" Enter` delivers the
  # Enter key while Claude Code is still rendering the long input — claude
  # sees the Enter as part of pending render-state input, not as a submit.
  # Splitting them, with a short settle delay between, lets Enter land on
  # the rendered prompt and trigger submit. Verified empirically: bundled
  # Enter never produced a bash-stdout entry; separate Enter does.
  if [[ "$text" == "! "* ]]; then
    tmux send-keys -t "$pane" "!"
    sleep 0.3
    tmux send-keys -t "$pane" "${text#! }"
    sleep 0.5
    tmux send-keys -t "$pane" Enter
  else
    tmux send-keys -t "$pane" "$text"
    sleep 0.5
    tmux send-keys -t "$pane" Enter
  fi
}

# wait_for_bash_stdout_contains <repo-path> <substring> [timeout-seconds]
# Specialization of wait_for_jsonl_match for the most common assertion
# shape: "wait for a `!`-prefix bash command's <bash-stdout> entry whose
# content contains <substring>". Uses jq --arg so <substring> can contain
# any characters (quotes, parens, etc.) without escaping headaches.
# Echoes the matching JSONL line on success, exit 0. Empty + exit 1 on timeout.
# Default timeout: 60s.
wait_for_bash_stdout_contains() {
  local repo="$1" substring="$2" timeout="${3:-60}"
  local project_dir="$HOME/.claude/projects/$(encode_cwd "$repo")"
  local elapsed=0
  local interval=1
  while [ "$elapsed" -lt "$timeout" ]; do
    if [ -d "$project_dir" ]; then
      local match
      match=$(jq -c --arg sub "$substring" \
        'select(.type == "user" and (.message.content | type == "string") and (.message.content | startswith("<bash-stdout>")) and (.message.content | contains($sub)))' \
        "$project_dir"/*.jsonl 2>/dev/null | head -n1 || true)
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

# send_bash_and_wait <pane> <repo-path> <bash-cmd> <expected-substring> [timeout]
# Sends `! <bash-cmd>` to <pane> via send_command (which handles the
# discrete-! pattern, the wait_for_pane_idle gate, and the separate-Enter
# trick) and then waits for a bash-stdout JSONL entry that contains
# <expected-substring>. Returns 0 on match, 1 on timeout.
#
# Use this instead of inlining `tmux send-keys ... Enter` + a verbose
# wait_for_jsonl_match jq filter — too easy to typo the filter and break
# silently. Default timeout: 60s.
send_bash_and_wait() {
  local pane="$1" repo="$2" cmd="$3" expected="$4" timeout="${5:-60}"
  send_command "$pane" "! $cmd"
  wait_for_bash_stdout_contains "$repo" "$expected" "$timeout" >/dev/null
}

# wait_for_pane_idle <pane> [max-seconds]
# Polls `tmux capture-pane` every 500ms; returns once the captured content
# is unchanged across 2 consecutive samples (≈1s of stability). Returns
# with success on timeout too — we'd rather over-send than block forever.
wait_for_pane_idle() {
  local pane="$1"
  local max_seconds="${2:-10}"
  local prev_hash=""
  local stable=0
  local elapsed_ms=0
  local max_ms=$((max_seconds * 1000))
  while [ "$elapsed_ms" -lt "$max_ms" ]; do
    local hash
    hash=$(tmux capture-pane -t "$pane" -p | md5sum | cut -d' ' -f1)
    if [ "$hash" = "$prev_hash" ]; then
      stable=$((stable + 1))
      if [ "$stable" -ge 2 ]; then
        return 0
      fi
    else
      stable=0
      prev_hash="$hash"
    fi
    sleep 0.5
    elapsed_ms=$((elapsed_ms + 500))
  done
  return 0
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
