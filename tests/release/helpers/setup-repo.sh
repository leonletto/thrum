#!/usr/bin/env bash
# tests/release/helpers/setup-repo.sh — run-level setup (spec § 4 steps A-C).
# Brings up a complete coord+impl multi-agent fixture and exports
# BASE / REPO / RUNID / COORD_PANE / IMPL_PANE / COORD_REPO / IMPL_REPO.

# run_setup → exports the env vars above. Returns non-zero on any setup failure.
run_setup() {
  # Capture tmux-exec path BEFORE step A unsets all THRUM_* env vars (which
  # would also strip THRUM_RELEASE_REPO_ROOT set by run.sh).
  #
  # Driver-side thrum calls run through scripts/tmux-exec to break the PID
  # ancestry chain (spec § 4 lines 206-212). Otherwise thrum walks up from
  # the driver's bash → claude (this script's parent) and adopts the wrong
  # identity / fires cross-worktree guards. tmux-exec runs each command in
  # an ephemeral tmux pane whose ancestry chain ends at the tmux server, no
  # claude in sight.
  local TE="$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec"
  if [ ! -x "$TE" ]; then
    echo "ERROR: $TE missing or not executable" >&2
    return 1
  fi

  # A.preflight. Nuke leftover state from a prior crashed/killed run before
  # starting. Without this, a SIGKILL'd run leaves coord/impl tmux sessions
  # alive plus an ephemeral daemon, and the next `thrum tmux start --name
  # coord` mixes with the stale session causing the whoami probe to fail
  # against the wrong claude. Idempotent: harmless if there's nothing to
  # clean.
  tmux kill-session -t coord 2>/dev/null || true
  tmux kill-session -t impl 2>/dev/null || true
  # shellcheck disable=SC2009
  ps -eo pid,command 2>/dev/null \
    | grep -E "thrum.*daemon.*\.thrum_release_tests" \
    | grep -v grep \
    | awk '{print $1}' \
    | xargs -r kill 2>/dev/null \
    || true

  # A. Prep
  RUNID="$(date +%Y%m%dT%H%M%S)-$$"
  BASE="$HOME/.thrum_release_tests/$RUNID"
  REPO="$BASE/repo"
  # WORKTREE_BASE is a SEPARATE root for the impl worktree, intentionally not
  # nested inside BASE. thrum worktree create auto-appends the repo's basename
  # ("repo") to worktrees.base_path (cmd/thrum/main.go:2680-2683), which makes
  # base_path collide with $REPO if WORKTREE_BASE were under $BASE. Putting it
  # at a different parent path matches how the real dev coord uses
  # ~/.workspaces (separate from /Users/leon/dev/opensource/thrum).
  WORKTREE_BASE="$HOME/.thrum_release_test_worktrees/$RUNID"
  mkdir -p "$REPO" "$WORKTREE_BASE"
  # Strip THRUM_* env vars (THRUM_HOME, THRUM_NAME, etc.) to avoid the script's
  # parent shell leaking identity hints into the ephemeral fixture. Preserve
  # framework-internal THRUM_RELEASE_* vars (THRUM_RELEASE_REPO_ROOT used by
  # scenarios, THRUM_RELEASE_NO_TEARDOWN debug toggle).
  while IFS= read -r v; do
    [ -n "$v" ] && unset "$v"
  done < <(env | grep -E '^THRUM_' | grep -v '^THRUM_RELEASE_' | cut -d= -f1)

  (
    cd "$REPO" || exit 1
    git init --initial-branch=main >/dev/null
    git config user.email release-tests@thrum.local
    git config user.name "Release Tests"
    echo "# Release test repo $RUNID" > README.md
    git add . && git commit -m "Initial commit" >/dev/null
  ) || { echo "ERROR: B/git init failed" >&2; return 1; }

  # --non-interactive forces the legacy silent path even though the
  # tmux-exec pane provides a TTY. v0.9.3 added the wizard, which would
  # otherwise prompt and hang fixture setup. --runtime claude still
  # picks the runtime explicitly.
  "$TE" exec --cwd "$REPO" --clean -- thrum init --non-interactive --runtime claude \
    || { echo "ERROR: B/thrum init failed" >&2; return 1; }

  "$TE" exec --cwd "$REPO" --clean -- thrum quickstart \
      --name test_coordinator_main \
      --role coordinator \
      --module all \
      --intent "Release test coordinator" \
    || { echo "ERROR: B/thrum quickstart failed" >&2; return 1; }

  # thrum tmux start creates the session, launches claude, then auto-attaches.
  # The attach blocks until the tty closes; tmux-exec's --timeout bounds it.
  # Session + runtime launch happen synchronously before the attach attempt.
  : > /tmp/zh4p-tmux-start.log
  "$TE" exec --cwd "$REPO" --clean --timeout 30 -- thrum tmux start --name coord \
    > /tmp/zh4p-tmux-start.log 2>&1 || true

  # Verify coord identity from inside the pane. send_bash_and_wait handles
  # the discrete-`!`, separate-Enter, and pane-idle gating; we just supply
  # the bash command and a substring we expect in the bash-stdout entry.
  #
  # Note: we don't wait_for_session_start before this. claude at its welcome
  # screen writes ZERO JSONL until it receives user input that starts a
  # session; polling for a SessionStart attachment before the first `!` send
  # would be a chicken-and-egg deadlock. This whoami send IS what kicks the
  # session alive.
  #
  # Retry-with-bounded-resend (thrum-vjqn): on saturated dev boxes (~60+
  # tmux sessions, long daemon uptime) claude's interactive-input handler
  # may not be bound when wait_for_pane_idle's 10s gate fires, so the `!`
  # keystrokes get eaten and the bash-stdout JSONL line never appears.
  # A pure timeout bump can't recover from a missed-keystroke race — but
  # resending the idempotent `thrum whoami --json` after each round gives
  # a swallowed keystroke a fresh chance once claude has had time to
  # finish booting. 3 attempts × 30s = 90s budget, generous but bounded.
  local attempt=1
  while [ "$attempt" -le 3 ]; do
    if send_bash_and_wait coord "$REPO" "thrum whoami --json" "test_coordinator_main" 30; then
      break
    fi
    attempt=$((attempt + 1))
  done
  if [ "$attempt" -gt 3 ]; then
    echo "ERROR: coord whoami did not return expected bash-stdout entry across 3 attempts (90s total)" >&2
    return 1
  fi

  # C. Implementer worktree
  # C.1 patch worktrees config so C.2 lands under $WORKTREE_BASE. quickstart
  # populated base_path from the user's real config (~/.workspaces); without
  # this patch the new worktree would land there.
  #
  # Note: thrum auto-appends the repo's basename ("repo") to base_path
  # (cmd/thrum/main.go:2680), so the effective path is $WORKTREE_BASE/repo.
  # The impl worktree therefore lands at $WORKTREE_BASE/repo/impl.
  jq --arg bp "$WORKTREE_BASE/" \
    '.worktrees = {"base_path": $bp, "beads_enabled": false, "thrum_enabled": true}' \
    "$REPO/.thrum/config.json" > "$REPO/.thrum/config.json.tmp" \
    && mv "$REPO/.thrum/config.json.tmp" "$REPO/.thrum/config.json" \
    || { echo "ERROR: C.1 worktrees config patch failed" >&2; return 1; }

  # The path thrum will actually create the impl worktree at, after auto-append.
  local IMPL_WT="$WORKTREE_BASE/repo/impl"

  # C.2 create the impl worktree FROM the coord pane (so the call runs with
  # coord's identity, mirroring real workflow). Plain send_command — we
  # don't need to wait for a specific bash-stdout substring here because
  # the wait below polls the filesystem for the worktree dir directly.
  send_command coord "! thrum worktree create impl -b feature/release-test-impl"
  local elapsed=0
  while [ ! -d "$IMPL_WT" ] && [ "$elapsed" -lt 30 ]; do
    sleep 1
    elapsed=$((elapsed + 1))
  done
  if [ ! -d "$IMPL_WT" ]; then
    echo "ERROR: C.2 worktree create did not produce $IMPL_WT/" >&2
    return 1
  fi

  # C.3 implementer's tmux session — $IMPL_WT IS a secondary worktree (just
  # created by C.2), so the not-a-worktree hint won't fire and `thrum tmux
  # create` accepts it. Inline quickstart (per spec § 4 lines 110-115) registers
  # the impl agent inside the new pane.
  "$TE" exec --cwd "$REPO" --clean -- thrum tmux create impl \
      --cwd "$IMPL_WT" \
      --name test_implementer \
      --role implementer \
      --module all \
      --intent "Release test implementer" \
    || { echo "ERROR: C.3 tmux create impl failed" >&2; return 1; }

  # thrum tmux create only registers the agent inline; claude isn't running
  # yet. Launch sends `claude` keystrokes (then /thrum:prime after 10s) via
  # the daemon's HandleLaunch goroutine.
  "$TE" exec --cwd "$REPO" --clean -- thrum tmux launch impl \
    || { echo "ERROR: C.3 tmux launch impl failed" >&2; return 1; }

  # Wait for the impl session to actually start in claude (SessionStart hook
  # firing means claude booted and processed /thrum:prime).
  wait_for_session_start "$IMPL_WT" 60 \
    || { echo "ERROR: impl SessionStart did not appear within 60s" >&2; return 1; }

  # Verify impl identity from inside the pane.
  #
  # Same retry-with-bounded-resend as the coord probe (thrum-vjqn). The impl
  # path is less prone to the missed-keystroke race because line above
  # already confirmed SessionStart fired, but a saturated box can still
  # delay the bash-prefix mode toggle past wait_for_pane_idle's 10s gate.
  # Retry is benign on the happy path (first attempt succeeds in <10s).
  attempt=1
  while [ "$attempt" -le 3 ]; do
    if send_bash_and_wait impl "$IMPL_WT" "thrum whoami --json" "test_implementer" 30; then
      break
    fi
    attempt=$((attempt + 1))
  done
  if [ "$attempt" -gt 3 ]; then
    echo "ERROR: impl whoami did not return expected bash-stdout entry across 3 attempts (90s total)" >&2
    return 1
  fi

  echo "=== setup A + B + C complete ==="
  echo "RUNID=$RUNID"
  echo "REPO=$REPO"
  echo "BASE=$BASE"
  echo "WORKTREE_BASE=$WORKTREE_BASE"
  echo "IMPL_WT=$IMPL_WT"
  echo "tmux sessions:"
  tmux list-sessions 2>&1 | grep -E "coord|impl" || true

  # Export per-scenario context
  export RUNID BASE WORKTREE_BASE REPO
  export COORD_PANE=coord
  export IMPL_PANE=impl
  export COORD_REPO="$REPO"
  export IMPL_REPO="$IMPL_WT"
  return 0
}
