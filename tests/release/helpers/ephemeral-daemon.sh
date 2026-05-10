#!/usr/bin/env bash
# tests/release/helpers/ephemeral-daemon.sh — minimal daemon start/stop
# for behavioral fixtures. Different from setup-repo.sh: no fixed-name
# agent panes, no THRUM_RELEASE_REPO_ROOT.
#
# Usage:
#   FIXTURE=$(mktemp -d)
#   ephemeral_daemon_start "$FIXTURE"     # exports FIXTURE_REPO/FIXTURE_THRUM/FIXTURE_WORKSPACES/FIXTURE_TE
#   trap 'ephemeral_daemon_stop' EXIT
#
# Implementation note: thrum's cross_worktree identity guard checks that the
# caller's PID ancestry does not pass through a Claude/codex process registered
# to a different worktree. When this helper is sourced from within a Claude
# session, bare thrum calls inherit the Claude PID chain and the guard fires,
# causing inbox and send to return empty/error. We break the chain via
# scripts/tmux-exec (a persistent pool pane whose ancestry ends at the tmux
# server). All fixture-agent thrum calls should go through ephemeral_te_exec,
# not bare thrum, to keep them on the clean PID chain.

ephemeral_daemon_start() {
  local fixture="$1"
  if [[ -z "$fixture" ]]; then
    echo "ephemeral_daemon_start: fixture path required" >&2
    return 2
  fi
  export FIXTURE_REPO="$fixture/repo"
  export FIXTURE_THRUM="$FIXTURE_REPO/.thrum"
  export FIXTURE_WORKSPACES="$fixture/workspaces"
  mkdir -p "$FIXTURE_REPO" "$FIXTURE_WORKSPACES"
  ( cd "$FIXTURE_REPO" && git init -q && git commit --allow-empty -q -m "fixture init" )

  # Locate tmux-exec relative to this file so callers don't need to know the
  # project layout. Resolves to scripts/tmux-exec in the behavioral-harness
  # worktree regardless of cwd.
  local te_dir
  te_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  export FIXTURE_TE="$te_dir/../../../scripts/tmux-exec"
  if [[ ! -x "$FIXTURE_TE" ]]; then
    echo "ephemeral_daemon_start: tmux-exec not found at $FIXTURE_TE" >&2
    return 2
  fi
  # Use a dedicated tmux socket for fixture commands so pool sessions from
  # different fixtures/test runs don't share a pane and interleave output.
  export FIXTURE_TE_SOCKET="ephemeral-fixture-$$"

  # Initialize thrum config; patch the worktree-base config so coord-spawned
  # worktrees land under FIXTURE_WORKSPACES (NOT nested under the repo,
  # which would cause path collisions — same gotcha setup-repo.sh handles).
  # thrum init flags as of v0.10.2: --runtime, --name, --no-daemon, --skills,
  # --stealth, --dry-run, --force, --module. (No --quiet.) We use --no-daemon
  # so the wizard doesn't start a daemon — this helper does that explicitly
  # three lines below — and --runtime/--name suppress interactive prompts.
  thrum --repo "$FIXTURE_REPO" init --runtime claude --name test_fixture --no-daemon 2>/dev/null || true
  local config="$FIXTURE_THRUM/config.json"
  if [[ -f "$config" ]]; then
    jq --arg p "$FIXTURE_WORKSPACES" '.worktrees.base_path = $p' "$config" > "$config.tmp" && mv "$config.tmp" "$config"
  fi

  thrum --repo "$FIXTURE_REPO" daemon start >/dev/null 2>&1 || true
  for _ in $(seq 1 30); do
    if thrum --repo "$FIXTURE_REPO" daemon status >/dev/null 2>&1; then
      _EPHEMERAL_DAEMON_REPO="$FIXTURE_REPO"
      return 0
    fi
    sleep 0.2
  done
  echo "ephemeral_daemon_start: daemon failed to start in $FIXTURE_REPO" >&2
  return 1
}

# ephemeral_te_exec <env=val...> -- <cmd...>
# Run a thrum command via tmux-exec to break the Claude PID ancestry chain.
# All THRUM_* env vars are stripped (--clean) then the ones passed as env=val
# arguments before -- are re-applied inside the subshell.
#
# Example:
#   ephemeral_te_exec THRUM_NAME=test_impl THRUM_ROLE=implementer -- thrum inbox --json --all
ephemeral_te_exec() {
  local env_pairs=()
  while [[ $# -gt 0 && "$1" != "--" ]]; do
    env_pairs+=("$1")
    shift
  done
  [[ "${1:-}" == "--" ]] && shift

  # Build env prefix string for the command
  local env_prefix=""
  for pair in "${env_pairs[@]}"; do
    env_prefix+="$pair "
  done

  # Build the command with proper quoting so arguments with spaces survive
  # the sh -c boundary (e.g. "Test prompt for billing" must stay as one arg).
  local cmd_q
  cmd_q=$(printf '%q ' "$@")

  TMUX_EXEC_SOCKET="$FIXTURE_TE_SOCKET" \
    "$FIXTURE_TE" exec --cwd "${FIXTURE_REPO:-.}" --clean \
      -- sh -c "${env_prefix}${cmd_q}"
}

ephemeral_daemon_stop() {
  if [[ -n "${_EPHEMERAL_DAEMON_REPO:-}" ]]; then
    thrum --repo "$_EPHEMERAL_DAEMON_REPO" daemon stop >/dev/null 2>&1 || true
    unset _EPHEMERAL_DAEMON_REPO
  fi
  # Clean up the fixture-specific tmux socket pool session
  if [[ -n "${FIXTURE_TE_SOCKET:-}" ]]; then
    tmux -L "$FIXTURE_TE_SOCKET" kill-server 2>/dev/null || true
    unset FIXTURE_TE_SOCKET
  fi
}
