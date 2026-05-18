#!/usr/bin/env bash
# reload-plugins-all.sh — fire /reload-plugins in every thrum agent's tmux pane
#
# Useful after a `/plugin marketplace add` + `/plugin install thrum@thrum` cycle
# bumps the installed plugin to a new RC (or post-promotion to stable). Each
# agent's Claude Code runtime needs /reload-plugins to pick up the new skill +
# command + hook definitions; doing it across N agents by hand is annoying.
#
# Behavior:
#   - Iterates every active thrum agent session (via tmux-agent-sweep.sh; all
#     roles, not just implementer)
#   - For each pane: capture the bottom 15 lines and check for overlay states
#     (agent-list nav, y/n permission prompts, file-picker, etc.)
#   - Clean pane → `thrum tmux send <sess> '/reload-plugins'` + Enter via
#     `tmux send-keys -t <sess>:0.0 Enter`. The thrum-wrapped primitive
#     goes through the daemon RPC path which adds audit + identity +
#     session-validity safeguards — slower (several seconds latency) but
#     correct. We use the wrapped primitive deliberately even though raw
#     `tmux send-keys` would work; bypassing thrum's safeguards for speed
#     is the wrong trade-off. Enter is sent via raw tmux because thrum
#     tmux send doesn't append a newline.
#     Keystrokes queue if the agent is busy and fire when the REPL returns
#     to idle (verified empirically 2026-05-18).
#   - Overlay/permission pane → SKIP and surface loudly at the end
#
# Exit code:
#   0 if all panes reloaded cleanly
#   1 if any pane was skipped (loud surface — user/agent must resolve)
#
# Usage:
#   bash scripts/reload-plugins-all.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWEEP_SCRIPT="${SCRIPT_DIR}/tmux-agent-sweep.sh"

if [[ ! -x "$SWEEP_SCRIPT" ]] && [[ ! -r "$SWEEP_SCRIPT" ]]; then
  echo "ERROR: tmux-agent-sweep.sh not found at $SWEEP_SCRIPT" >&2
  exit 2
fi

SWEEP_OUT="$(mktemp -t reload-plugins-sweep.XXXXXX)"
trap 'rm -f "$SWEEP_OUT"' EXIT

# All roles, default line count — we'll re-capture per session below for
# overlay detection, so we just need the session list from the sweep
bash "$SWEEP_SCRIPT" --out "$SWEEP_OUT" >/dev/null

# Extract session names — format is "tmux:      <session>:<window>.<pane>"
mapfile -t SESSIONS < <(awk '/^tmux:/{split($2,a,":"); print a[1]}' "$SWEEP_OUT" | sort -u)

if (( ${#SESSIONS[@]} == 0 )); then
  echo "No thrum agent sessions found via tmux-agent-sweep.sh — nothing to do."
  exit 0
fi

reloaded=()
skipped=()

# Overlay detection patterns — these intercept Enter with non-REPL semantics.
# Keep this list narrow; false positives skip work that would have succeeded.
OVERLAY_PATTERN='↑/↓ to select|to view|\(y/n\)|\(Y/N\)|Continue\?|Approve\?|Do you want to|press y to|press n to|press ENTER to|approve this|Select an option|Search:'

for sess in "${SESSIONS[@]}"; do
  # Capture the last 15 lines for overlay detection
  if ! pane=$(tmux capture-pane -t "${sess}:0.0" -p 2>/dev/null | tail -15); then
    skipped+=("$sess  [tmux capture-pane failed — session may be detached or gone]")
    continue
  fi

  if echo "$pane" | grep -qE "$OVERLAY_PATTERN"; then
    skipped+=("$sess  [pane in overlay or permission prompt — Enter would mis-fire]")
    continue
  fi

  # Send: thrum tmux send delivers the text (via daemon RPC with audit +
  # identity safeguards), then raw tmux send-keys fires Enter to submit.
  # Two-step because thrum tmux send doesn't append a newline. Keystrokes
  # queue cleanly while the REPL is busy and fire when it returns to idle.
  if ! thrum tmux send "$sess" '/reload-plugins' >/dev/null 2>&1; then
    skipped+=("$sess  [thrum tmux send failed — daemon issue or session mismatch]")
    continue
  fi
  if ! tmux send-keys -t "${sess}:0.0" Enter 2>/dev/null; then
    skipped+=("$sess  [tmux send-keys Enter failed — pane vanished mid-send]")
    continue
  fi

  reloaded+=("$sess")
done

echo "─────────────────────────────────────────────────────"
echo "  PLUGIN RELOAD: ${#reloaded[@]} fired, ${#skipped[@]} skipped"
echo "─────────────────────────────────────────────────────"

if (( ${#reloaded[@]} > 0 )); then
  for s in "${reloaded[@]}"; do
    echo "  ✓ $s"
  done
fi

if (( ${#skipped[@]} == 0 )); then
  echo ""
  echo "  All clear — fires queue automatically; busy panes will reload on"
  echo "  their next REPL idle. Delivery goes through the thrum daemon RPC,"
  echo "  so allow ~15-30s before spot-checking with:"
  echo "    tmux capture-pane -t <session>:0.0 -p | grep -A1 '/reload-plugins'"
  exit 0
fi

# Loud surface — if an agent is running this script, surface to the user.
echo ""
echo "╔═════════════════════════════════════════════════════════════╗"
echo "║                                                             ║"
echo "║  ⚠️  MANUAL ACTION REQUIRED — SKIPPED PANES BELOW           ║"
echo "║                                                             ║"
echo "║  The script could not safely send /reload-plugins to these  ║"
echo "║  sessions because the pane was in a non-REPL state          ║"
echo "║  (agent-list overlay, permission prompt, file-picker, etc). ║"
echo "║  Pressing Enter via the script would have mis-fired the     ║"
echo "║  overlay action instead of submitting the slash command.    ║"
echo "║                                                             ║"
echo "║  Resolve each pane's overlay manually, then either:         ║"
echo "║    1. Re-run this script — it will pick up the cleared    ║"
echo "║       panes and skip the ones that fired earlier (idempotent║"
echo "║       — repeat /reload-plugins is harmless).               ║"
echo "║    2. Or attach to each pane below and type                 ║"
echo "║       '/reload-plugins' yourself.                           ║"
echo "║                                                             ║"
echo "╚═════════════════════════════════════════════════════════════╝"
echo ""
for s in "${skipped[@]}"; do
  echo "  ✗ $s"
done
echo ""

exit 1
