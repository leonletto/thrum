#!/usr/bin/env bash
# tests/release/helpers/self-isolate.sh — process-tree contamination detect
# + self-re-exec into a default-server tmux session.
#
# Why: the release harness fails subtly when invoked from inside a live thrum
# agent pane — the agent's pane identity leaks into the daemon's caller
# resolver (PID-walk for claude/codex ancestor), causing fixture dispatches
# to refuse with -32002 or cross-worktree errors before the test can even
# start. The seam is process ancestry, NOT env vars (THRUM_* stripping is
# insufficient). The proven isolation mechanism (scenario 29 green) is to
# re-exec the harness in a DETACHED default-server tmux pane with TMUX /
# TMUX_PANE stripped — the pane's parent becomes the tmux server (launchd /
# pid 1) so no claude/codex ancestor remains.
#
# Sentinel: THRUM_RELEASE_ISOLATED=1 marks the re-exec'd process so we never
# loop. If contaminated AND sentinel is already set, something is wrong with
# the re-exec (tmux nested, weird /usr/bin/ps state, ...) — fail loud rather
# than infinite-loop or silently degrade.
#
# Public API: source this file and call thrum_release_self_isolate at the
# top of every release-harness entrypoint (before any fixture setup).

# _thrum_release_has_agent_ancestor: walk ppid chain to pid 1 looking for a
# claude/codex ancestor process. Echoes the matched comm + returns 0 if
# contaminated; echoes nothing + returns 1 if clean.
_thrum_release_has_agent_ancestor() {
  local pid=$$
  local ppid comm
  while [ "$pid" -gt 1 ]; do
    if ! read -r ppid comm < <(ps -o ppid=,comm= -p "$pid" 2>/dev/null); then
      return 1
    fi
    [ -z "${ppid:-}" ] && return 1
    case "$comm" in
      *claude*|*codex*)
        printf '%s' "$comm"
        return 0
        ;;
    esac
    [ "$ppid" -le 1 ] && break
    pid="$ppid"
  done
  return 1
}

# _thrum_release_fail_loud <matched-ancestor> <reason>
# Part 2 preflight guard: print a precise error citing the contamination, the
# documented isolation mechanism, and the corrective invocation. Always
# exits non-zero so the harness never limps into the cryptic identity-guard
# error downstream.
_thrum_release_fail_loud() {
  local matched="$1"
  local reason="$2"
  cat >&2 <<EOF
ERROR: tests/release harness contaminated by an agent ancestor and cannot
       self-isolate ($reason).

Detected: process-tree ancestor matched '$matched'. Running the release
harness from inside a live claude/codex agent pane leaks the agent's pane
identity into the daemon's caller resolver (PID-walk + cross_worktree
guard), causing fixture dispatches to refuse with -32002 or wrong-worktree
errors before the test can even start. See tests/release/CLAUDE.md.

Run the harness from a clean shell (a terminal outside any agent session).
The harness will self-isolate into a detached default-server tmux session
on subsequent invocations if tmux is available.
EOF
  exit 2
}

# thrum_release_self_isolate <script-abs-path> [args...]
# Idempotent self-isolating launcher. Four branches:
#   1. THRUM_RELEASE_ISOLATED=1 + clean ancestry -> proceed normally (return 0)
#   2. THRUM_RELEASE_ISOLATED=1 + still contaminated -> FAIL LOUD (re-exec
#      didn't clean ancestry; refuse to limp into the downstream guard)
#   3. sentinel unset + clean ancestry -> proceed normally (return 0)
#   4. sentinel unset + contaminated:
#      a. tmux available -> re-exec into a detached default-server tmux
#         session, wait for it to finish, propagate exit. This function
#         does NOT return in this branch — it calls `exit`.
#      b. tmux missing -> FAIL LOUD.
thrum_release_self_isolate() {
  local script_abs="$1"; shift
  local matched=""
  local contaminated=0
  if matched="$(_thrum_release_has_agent_ancestor)"; then
    contaminated=1
  fi

  if [ "${THRUM_RELEASE_ISOLATED:-}" = "1" ]; then
    if [ "$contaminated" -eq 1 ]; then
      _thrum_release_fail_loud "$matched" "sentinel already set but ancestor still present"
    fi
    return 0
  fi

  if [ "$contaminated" -eq 0 ]; then
    return 0
  fi

  if ! command -v tmux >/dev/null 2>&1; then
    _thrum_release_fail_loud "$matched" "tmux not available to self-isolate"
  fi

  local sess="reltest-$$"
  local exit_file="/tmp/${sess}.exit"
  local wrapper="/tmp/${sess}.cmd"
  rm -f "$exit_file" "$wrapper"

  # Build the wrapper script that the detached tmux pane will execute. Using
  # a file (not an inline tmux command string) avoids shell-quoting tangles
  # for args that contain spaces/specials: printf '%q' inside the file
  # produces bash-safe escaping.
  {
    echo "#!/usr/bin/env bash"
    # No `set -e`: we want to capture the harness exit code regardless.
    printf 'env -u TMUX -u TMUX_PANE THRUM_RELEASE_ISOLATED=1 bash %q' "$script_abs"
    local arg
    for arg in "$@"; do
      printf ' %q' "$arg"
    done
    echo
    printf 'echo "$?" > %q\n' "$exit_file"
  } > "$wrapper"
  chmod +x "$wrapper"

  echo "tests/release: agent ancestor detected ('$matched'); self-isolating into detached tmux session '$sess' on the default server." >&2
  echo "  attach to watch: tmux attach -t $sess" >&2

  tmux new-session -d -s "$sess" "$wrapper"

  # Block + propagate exit. Polling tmux has-session is the portable way to
  # wait for a detached session to end (tmux wait-for needs an explicit
  # wake-signal from inside the session).
  while tmux has-session -t "$sess" 2>/dev/null; do
    sleep 1
  done

  local rc
  rc="$(cat "$exit_file" 2>/dev/null || echo 1)"
  rm -f "$wrapper" "$exit_file"

  echo "tests/release: detached session '$sess' exited with status $rc" >&2
  exit "$rc"
}
