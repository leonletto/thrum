#!/usr/bin/env bash
# tests/release/helpers/assert-tmux.sh — tmux/process predicates for the
# behavioral harness. Each function returns 0 on pass, non-zero on fail.

assert_tmux_session_exists() {
  local name="$1"
  if tmux has-session -t "$name" 2>/dev/null; then return 0; fi
  echo "assert-tmux.session_exists: no session '$name'" >&2
  return 1
}

assert_tmux_pane_running_runtime() {
  local session="$1" runtime="$2"
  if ! tmux has-session -t "$session" 2>/dev/null; then
    echo "assert-tmux.pane_running_runtime: session '$session' missing" >&2
    return 1
  fi
  local pane_pid
  pane_pid=$(tmux display-message -p -t "$session" '#{pane_pid}' 2>/dev/null || true)
  if [[ -z "$pane_pid" ]]; then
    echo "assert-tmux.pane_running_runtime: could not get pane_pid for '$session'" >&2
    return 1
  fi
  # Walk the process tree under pane_pid; pgrep -P only returns direct
  # children, so we walk one level deeper for shells that fork their own
  # processes (e.g. zsh wraps then execs).
  # Retry up to ~2s to handle exec-transition races on macOS where ps -o comm=
  # transiently returns empty while the shell is execing the target binary.
  local attempt
  for attempt in 1 2 3 4; do
    local children
    children=$(pgrep -P "$pane_pid" 2>/dev/null || true)
    for child in $children; do
      local comm
      comm=$(ps -o comm= -p "$child" 2>/dev/null | xargs basename 2>/dev/null || true)
      if [[ "$comm" == "$runtime" ]]; then return 0; fi
      # one more level
      local grandkids
      grandkids=$(pgrep -P "$child" 2>/dev/null || true)
      for gk in $grandkids; do
        local gkcomm
        gkcomm=$(ps -o comm= -p "$gk" 2>/dev/null | xargs basename 2>/dev/null || true)
        if [[ "$gkcomm" == "$runtime" ]]; then return 0; fi
      done
    done
    [[ $attempt -lt 4 ]] && sleep 0.5
  done
  echo "assert-tmux.pane_running_runtime: no process '$runtime' under pane_pid=$pane_pid in '$session'" >&2
  return 1
}

assert_tmux_pane_contains() {
  local session="$1" pattern="$2"
  if ! tmux has-session -t "$session" 2>/dev/null; then
    echo "assert-tmux.pane_contains: session '$session' missing" >&2
    return 1
  fi
  if tmux capture-pane -p -t "$session" 2>/dev/null | grep -q -- "$pattern"; then return 0; fi
  echo "assert-tmux.pane_contains: pattern '$pattern' not in pane of '$session'" >&2
  return 1
}

# runtime_version <binary> — emit the runtime's version string on stdout.
# On any failure (binary missing, --version unsupported), emits "unknown".
runtime_version() {
  local binary="$1"
  if ! command -v "$binary" >/dev/null 2>&1 && [[ ! -x "$binary" ]]; then
    echo "unknown"
    return 0
  fi
  local out
  out=$("$binary" --version 2>/dev/null | head -1 || true)
  if [[ -n "$out" ]]; then
    echo "$out"
  else
    echo "unknown"
  fi
}
