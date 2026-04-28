#!/usr/bin/env bash
# SessionStart hook: inject `thrum prime` output into the agent's context.
#
# Emits the assembled banner+directive+briefing as plain stdout. Claude
# Code routes hook stdout through a size-gated path: small outputs
# (<~2KB) become inline `<system-reminder>` blocks delivered straight to
# the model; larger outputs get persisted to a `tool-results/<id>.txt`
# file with only the first ~2KB previewed inline (and a
# `<persisted-output>` wrapper showing the full file path).
#
# Field-test history (zarambp14):
#   - thrum-tfrv tried the documented JSON output protocol
#     (`hookSpecificOutput.additionalContext`) to bypass the size cap.
#     Claude Code captured the JSON to attachment.stdout but
#     attachment.additionalContext stayed null — the field is silently
#     ignored for SessionStart hooks. Reverted in thrum-a6sw (this
#     change).
#   - thrum-a6sw: keep plain stdout, but make the directive
#     SIZE-AWARE. Small briefings get the original "auto-loaded, do
#     not re-prime" directive (xupf+2qe2). Large briefings get a
#     MUST-READ directive that points the agent at the path inside
#     the `<persisted-output>` wrapper Claude Code already shows in
#     the first 2KB preview, turning the truncation into a forcing
#     function instead of a silent loss.
#
# Hook-level timeout is enforced by plugin.json; this script does not
# need a portable `timeout` wrapper.
#
# Output ordering for a registered agent (top → bottom):
#   1. Identity banner — agent / role / worktree / branch / module
#      (thrum-2qe2). Always first; renders inside the 2KB preview.
#   2. Directive — size-aware:
#        - small body (< THRESHOLD bytes): "auto-loaded, do not re-prime"
#        - large body (>= THRESHOLD bytes): "MUST READ the persisted file"
#      Always second so it lands inside the 2KB preview.
#   3. Restart-snapshot preamble (existing). Hoisted only when the
#      briefing carries a `# Previous Session Context` block.
#   4. Briefing envelope + full prime output.

set -uo pipefail

# Threshold for choosing the small-body vs large-body directive.
# Claude Code's documented preview cap is ~2KB; using 1500 leaves
# headroom for the directive block itself + the banner so the
# directive lands cleanly inside the preview window.
SIZE_DIRECTIVE_THRESHOLD=1500

# Project doesn't use thrum — silent no-op.
if ! command -v thrum >/dev/null 2>&1; then
  exit 0
fi

# Capture whoami JSON ONCE, extract identity fields downstream. The
# script ran a single `thrum whoami --json` previously; keeping the
# RPC count at one preserves session-start latency.
WHOAMI_JSON=""
AGENT_ID=""
if command -v jq >/dev/null 2>&1; then
  WHOAMI_JSON=$(thrum whoami --json 2>/dev/null || true)
  AGENT_ID=$(printf '%s' "$WHOAMI_JSON" \
    | jq -r 'select(.agent_id != null) | .agent_id // empty' 2>/dev/null \
    || true)
fi

if [ -z "$AGENT_ID" ]; then
  # No agent registered — preserve historical nudge so the user/agent
  # knows to prime manually after registration.
  echo "Run /thrum:prime to load your session context, identity, and any restart snapshots."
  exit 0
fi

# Extract additional banner fields. Each is best-effort: a missing
# field just renders as "unknown" in the banner, never aborts the hook.
AGENT_ROLE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.role // empty' 2>/dev/null || true)
AGENT_WORKTREE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.worktree // empty' 2>/dev/null || true)
AGENT_BRANCH=$(printf '%s' "$WHOAMI_JSON" | jq -r '.branch // empty' 2>/dev/null || true)
AGENT_MODULE=$(printf '%s' "$WHOAMI_JSON" | jq -r '.module // empty' 2>/dev/null || true)

PRIME_OUTPUT=$(thrum prime 2>/dev/null || true)

if [ -z "$PRIME_OUTPUT" ]; then
  # Prime failed (daemon down, slow, etc.) — fall back to the manual
  # nudge so session start never blocks on a broken thrum.
  echo "Run /thrum:prime to load your session context, identity, and any restart snapshots."
  echo "(Auto-injection failed — daemon may be unreachable. Run \`thrum daemon status\` to check.)"
  exit 0
fi

# Two-phase build: assemble BANNER, RESTART_PREAMBLE, and BRIEFING into
# separate variables, total their byte count, then choose the
# size-appropriate directive and emit in the canonical order.
append_to() { local _name="$1"; shift; printf -v "$_name" '%s%s' "${!_name}" "$1"; }

# 1. Identity banner — always first; lands in the 2KB preview.
BANNER=""
append_to BANNER "# 🎯 You are: @${AGENT_ID}"$'\n'
append_to BANNER $'\n'
append_to BANNER "- **Role:** ${AGENT_ROLE:-unknown}"$'\n'
append_to BANNER "- **Worktree:** ${AGENT_WORKTREE:-unknown}"$'\n'
append_to BANNER "- **Branch:** ${AGENT_BRANCH:-unknown}"$'\n'
if [ -n "$AGENT_MODULE" ] && [ "$AGENT_MODULE" != "$AGENT_ROLE" ]; then
  append_to BANNER "- **Module:** ${AGENT_MODULE}"$'\n'
fi
append_to BANNER $'\n---\n\n'

# 3. Restart-snapshot preamble (if `thrum prime` carries a Previous
# Session Context block). Built before directive so the size check
# below sees the total prefix bytes.
RESTART_PREAMBLE=""
if printf '%s' "$PRIME_OUTPUT" | grep -q '^# Previous Session Context'; then
  append_to RESTART_PREAMBLE '# 🛑 ACTION REQUIRED — Instructions From Your Previous Session'$'\n'
  append_to RESTART_PREAMBLE $'\n'
  append_to RESTART_PREAMBLE '**You restarted from a prior session and left yourself a Resume Plan.** It is in the **`# Previous Session Context`** section of the briefing below. That plan is not background reading — it is your own message-to-self with concrete next steps.'$'\n'
  append_to RESTART_PREAMBLE $'\n'
  append_to RESTART_PREAMBLE '**Before doing anything else:**'$'\n'
  append_to RESTART_PREAMBLE $'\n'
  append_to RESTART_PREAMBLE '1. Scroll to the `# Previous Session Context` section of the briefing.'$'\n'
  append_to RESTART_PREAMBLE '2. Read the **`## Resume Plan`** sub-section in full.'$'\n'
  append_to RESTART_PREAMBLE '3. Execute its numbered steps in order.'$'\n'
  append_to RESTART_PREAMBLE '4. Only then continue to the rest of the briefing or the user'\''s prompt.'$'\n'
  append_to RESTART_PREAMBLE $'\n'
  append_to RESTART_PREAMBLE 'The Resume Plan was written by *you* in the previous session specifically because you knew this future you would need it. Trust it and act on it.'$'\n'
  append_to RESTART_PREAMBLE $'\n---\n\n'
fi

# 4. Briefing envelope + full prime output.
BRIEFING=""
append_to BRIEFING '# Thrum Session Briefing (auto-loaded)'$'\n'
append_to BRIEFING $'\n'
append_to BRIEFING 'The complete `thrum prime` output is included below. You do NOT need to run `/thrum:prime` or `thrum prime` again this session — the briefing is already in your context. Read it in full; the session context section at the end is the most important.'$'\n'
append_to BRIEFING $'\n'
append_to BRIEFING 'Only spawn additional commands if the inbox section shows unread messages that need processing.'$'\n'
append_to BRIEFING $'\n---\n\n'
append_to BRIEFING "$PRIME_OUTPUT"$'\n'

# Choose the directive based on the total non-directive body size. The
# directive itself is short (~400B for either variant), so adding it
# pushes a borderline body over the cap — but that's fine since the
# directive is the highest-priority content and lands inside the
# preview either way.
TOTAL_BODY_SIZE=$(( ${#BANNER} + ${#RESTART_PREAMBLE} + ${#BRIEFING} ))

DIRECTIVE=""
if [ "$TOTAL_BODY_SIZE" -ge "$SIZE_DIRECTIVE_THRESHOLD" ]; then
  # Large-body case: briefing exceeds the 2KB preview. Tell the agent
  # to Read the persisted file path Claude Code shows in the
  # `<persisted-output>` wrapper above the preview. Truncation
  # becomes a forcing function instead of a silent loss.
  append_to DIRECTIVE '> 🛑 **BRIEFING TRUNCATED — YOU MUST READ THE PERSISTED FILE** 🛑'$'\n'
  append_to DIRECTIVE '>'$'\n'
  append_to DIRECTIVE '> The full session briefing exceeded the ~2KB inline preview. Use the **Read** tool against the path shown in the `<persisted-output>` wrapper above (`Full output saved to: ...`) to load:'$'\n'
  append_to DIRECTIVE '>'$'\n'
  append_to DIRECTIVE '> - Your inbox (unread messages may need processing)'$'\n'
  append_to DIRECTIVE '> - Project state + recent commits'$'\n'
  append_to DIRECTIVE '> - Session context for restart recovery'$'\n'
  append_to DIRECTIVE '>'$'\n'
  append_to DIRECTIVE '> Do NOT skip this. Do NOT run `thrum prime` manually — that would double-prime. Just Read the file shown above.'$'\n'
  append_to DIRECTIVE $'\n'
else
  # Small-body case: full briefing fits in the inline preview, so the
  # original auto-loaded directive applies (xupf+2qe2 phrasing).
  append_to DIRECTIVE '> ✅ **Context auto-loaded by SessionStart hook.**'$'\n'
  append_to DIRECTIVE '>'$'\n'
  append_to DIRECTIVE '> **Do NOT run `/thrum:prime` or `thrum prime` — the full briefing is already in your context below.**'$'\n'
  append_to DIRECTIVE '> Only invoke them manually if this hook fell through to a degraded "auto-injection failed" notice.'$'\n'
  append_to DIRECTIVE $'\n'
fi

# Emit in canonical order: banner → directive → restart preamble →
# briefing. Banner + directive always land inside the 2KB preview.
printf '%s' "$BANNER"
printf '%s' "$DIRECTIVE"
printf '%s' "$RESTART_PREAMBLE"
printf '%s' "$BRIEFING"
