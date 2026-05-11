#!/usr/bin/env bash
# tests/release/helpers/ephemeral-daemon.sh — minimal daemon start/stop
# for behavioral fixtures. Different from setup-repo.sh: no tmux-exec
# dependency, no fixed-name agent panes, no THRUM_RELEASE_REPO_ROOT.
#
# Usage:
#   FIXTURE=$(mktemp -d /tmp/bh-XXXXXX)
#   ephemeral_daemon_start "$FIXTURE"   # exports FIXTURE_REPO/FIXTURE_THRUM/FIXTURE_WORKSPACES
#   trap 'ephemeral_daemon_stop' EXIT
#
# How identity guards are tamed: thrum's cross_worktree guard fires when
# the caller's tmux-pane session basename doesn't match the target
# worktree's basename — fixture daemons sit at /tmp/bh-XXXXXX/repo while
# self-tests run from inside a Claude pane, so strict mode would block
# every send/inbox. ephemeral_daemon_start patches the fixture's
# .thrum/config.json to set identity_guard.cross_worktree=warn (the
# documented escape hatch in internal/identity/guard/load.go), which
# emits a warn hint but allows the call to proceed. Combined with
# `cd "$FIXTURE_REPO"` + `unset THRUM_HOME` in the call sites
# (see assert-daemon.sh::_thrum_as), this lets the harness operate
# without scripts/tmux-exec.

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
  ( cd "$FIXTURE_REPO" \
    && git -c user.email=fixture@example.com -c user.name=fixture init -q \
    && git -c user.email=fixture@example.com -c user.name=fixture commit --allow-empty -q -m "fixture init" )

  # Initialize thrum config; patch the worktree-base config so coord-spawned
  # worktrees land under FIXTURE_WORKSPACES (NOT nested under the repo,
  # which would cause path collisions — same gotcha setup-repo.sh handles).
  # thrum init flags as of v0.10.2: --runtime, --name, --no-daemon, --skills,
  # --stealth, --dry-run, --force, --module. (No --quiet.) We use --no-daemon
  # so the wizard doesn't start a daemon — this helper does that explicitly
  # below — and --runtime/--name suppress interactive prompts.
  thrum --repo "$FIXTURE_REPO" init --runtime claude --name test_fixture --no-daemon </dev/null >/dev/null 2>&1 || true
  local config="$FIXTURE_THRUM/config.json"
  if [[ -f "$config" ]]; then
    # (1) Worktree base path so spawned worktrees go to FIXTURE_WORKSPACES.
    # (2) identity_guard.cross_worktree=warn so the fixture daemon accepts
    #     calls from a Claude pane with a non-matching tmux session basename.
    jq --arg p "$FIXTURE_WORKSPACES" \
       '.worktrees.base_path = $p
        | .identity_guard = ((.identity_guard // {}) + {"cross_worktree":"warn"})' \
       "$config" > "$config.tmp" && mv "$config.tmp" "$config"
  fi

  thrum --repo "$FIXTURE_REPO" daemon start </dev/null >/dev/null 2>&1 || true
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

ephemeral_daemon_stop() {
  if [[ -n "${_EPHEMERAL_DAEMON_REPO:-}" ]]; then
    thrum --repo "$_EPHEMERAL_DAEMON_REPO" daemon stop >/dev/null 2>&1 || true
    unset _EPHEMERAL_DAEMON_REPO
  fi
}
