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
#   - Clean pane → raw `tmux send-keys -t <sess>:0.0 '/reload-plugins' Enter`
#     (atomic text + Enter in one call). Keystrokes queue if the agent is
#     busy and fire when the REPL returns to idle (verified empirically
#     2026-05-18). Note: `thrum tmux send` was tested first and found to
#     silently drop keystrokes for some idle sessions — raw tmux send-keys
#     is the reliable primitive here.
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

  # Send: raw tmux send-keys with text + Enter atomically. Keystrokes queue
  # cleanly while the REPL is busy and fire when it returns to idle.
  if ! tmux send-keys -t "${sess}:0.0" '/reload-plugins' Enter 2>/dev/null; then
    skipped+=("$sess  [tmux send-keys failed — pane vanished or session mismatch]")
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
  echo "  their next REPL idle. Spot-check with:"
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
