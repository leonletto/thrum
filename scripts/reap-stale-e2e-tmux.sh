#!/usr/bin/env bash
# reap-stale-e2e-tmux.sh — reclaim ptys leaked by E2E test runs.
#
# The E2E harness (tests/e2e/helpers/tmux-exec.ts) creates a per-run tmux
# server on socket `thrum-e2e-<pid>` but only ever kills the CURRENT run's
# pid-keyed socket in global-setup preflight — so every prior run's server
# leaks forever, each holding a bash-pane pty against the macOS
# kern.tty.ptmx_max ceiling (default 511).
#
# This reaper kills ALL `thrum-e2e-*` servers regardless of pid. It is safe
# alongside a live agent fleet: those run on the `default` tmux socket, which
# is NEVER matched by the `thrum-e2e-*` glob. Dead socket files are also
# removed.
#
# Usage:
#   scripts/reap-stale-e2e-tmux.sh           # reap
#   scripts/reap-stale-e2e-tmux.sh --dry-run # list what would be reaped
set -euo pipefail

DRY_RUN=0
[ "${1:-}" = "--dry-run" ] && DRY_RUN=1

TMUXDIR="${TMUX_TMPDIR:-/private/tmp/tmux-$(id -u)}"
[ -d "$TMUXDIR" ] || { echo "no tmux dir at $TMUXDIR — nothing to reap"; exit 0; }

alive=0 dead=0 panes=0
for s in "$TMUXDIR"/thrum-e2e-*; do
  [ -e "$s" ] || continue
  # Guard: never touch the live-fleet default socket (defensive; glob excludes it anyway).
  case "$(basename "$s")" in default|cli|tmux-exec) continue ;; esac
  if [ -S "$s" ] && tmux -S "$s" list-sessions >/dev/null 2>&1; then
    n=$(tmux -S "$s" list-panes -a 2>/dev/null | wc -l | tr -d ' ')
    panes=$((panes + n)); alive=$((alive + 1))
    if [ "$DRY_RUN" = 1 ]; then
      echo "WOULD reap (alive): $(basename "$s") — $n pane(s)"
    else
      tmux -S "$s" kill-server 2>/dev/null || true
      rm -f "$s" 2>/dev/null || true
    fi
  else
    dead=$((dead + 1))
    if [ "$DRY_RUN" = 1 ]; then
      echo "WOULD remove (dead socket file): $(basename "$s")"
    else
      rm -f "$s" 2>/dev/null || true
    fi
  fi
done

verb="reaped"; [ "$DRY_RUN" = 1 ] && verb="would reap"
echo "$verb $alive alive thrum-e2e server(s) holding ~$panes pane-pty(s); $dead dead socket file(s)"
echo "ptys allocated now: $(ls /dev/ttys* 2>/dev/null | wc -l | tr -d ' ')/$(sysctl -n kern.tty.ptmx_max)"
