#!/usr/bin/env bash
# Fixture-driven regression test for thrum-roeq: sweep script picks the JSONL
# with the most-recent FIRST-EVENT timestamp (session-birth time), not the
# most-recent mtime. Pre-fix, `ls -t … | head -1` would surface a stale
# pre-restart session whose mtime got touched after a fresh session was born,
# causing the sweep to report e.g. 81% ctx when the actual current session
# was at 18%.
#
# Executable assertion: in a fixture transcript dir with two JSONLs where the
# mtime order DISAGREES with the birth-ts order, the sweep emits ctx_used
# derived from the BIRTH-NEWEST JSONL (18%), NOT the MTIME-NEWEST one (81%).
#
# This test relies on:
#   - thrum-l9e6's THRUM_SWEEP_IDENTITY_GLOBS env override to point the sweep
#     at a fixture identity file (instead of production paths).
#   - A fake `tmux` shim on PATH so the script's alive-set check passes
#     without spinning a real tmux session (same pattern as
#     tests/integration/sweep_json_test.go).
#   - `jq` available (script dependency).

set -euo pipefail

if [[ ${BASH_VERSINFO[0]:-0} -lt 4 ]]; then
    echo "SKIP: requires bash 4+ (mapfile in script under test)"
    exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
    echo "SKIP: jq not available"
    exit 0
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SWEEP="$SCRIPT_DIR/error-and-context-agent-sweep.sh"

if [[ ! -f "$SWEEP" ]]; then
    echo "FAIL: sweep script not found at $SWEEP"
    exit 1
fi

FIXTURE_DIR=$(mktemp -d)
trap 'rm -rf "$FIXTURE_DIR"' EXIT

FAKE_HOME="$FIXTURE_DIR/home"
FAKE_BIN="$FIXTURE_DIR/bin"
IDENTITY_DIR="$FIXTURE_DIR/identities"
WORKTREE_PATH="/fake/sweep_roeq_wt"
ENCODED_WT="$(echo "$WORKTREE_PATH" | sed 's|[./]|-|g')"
TRANSCRIPT_DIR="$FAKE_HOME/.claude/projects/$ENCODED_WT"

mkdir -p "$FAKE_HOME" "$FAKE_BIN" "$IDENTITY_DIR" "$TRANSCRIPT_DIR"

# Fake tmux: list-sessions returns the test session name so the alive-set
# membership check passes; other subcommands degrade to no-op so the script's
# pane-capture + activity-age paths don't disrupt the assertion.
cat > "$FAKE_BIN/tmux" <<'TMUXEOF'
#!/bin/sh
case "$1" in
    list-sessions) echo roeq-fake-session ;;
    capture-pane)  echo '' ;;
    display-message) echo 0 ;;
    send-keys)     exit 0 ;;
    *)             exit 0 ;;
esac
exit 0
TMUXEOF
chmod +x "$FAKE_BIN/tmux"

# Fake identity: one agent, bound to roeq-fake-session:0.0, worktree pointing
# at the fixture's transcript path.
cat > "$IDENTITY_DIR/roeq_test.json" <<JSONEOF
{
  "agent": {
    "Kind": "agent",
    "Name": "roeq_test_agent",
    "Role": "implementer",
    "Module": "roeq-test"
  },
  "tmux_session": "roeq-fake-session:0.0",
  "worktree": "$WORKTREE_PATH",
  "updated_at": "2026-05-29T05:00:00Z",
  "agent_status": "working"
}
JSONEOF

# Two synthetic JSONLs:
#   STALE: birth-ts = 2026-05-29T04:00:00Z (OLDER), ctx=81% (810k input tokens)
#   FRESH: birth-ts = 2026-05-29T05:15:00Z (NEWER), ctx=18% (180k input tokens)
# Window = 1M for claude-opus-4-7 model.
STALE_JSONL="$TRANSCRIPT_DIR/stale-session.jsonl"
FRESH_JSONL="$TRANSCRIPT_DIR/fresh-session.jsonl"

cat > "$STALE_JSONL" <<'STALEEOF'
{"type":"last-prompt","leafUuid":"stale","sessionId":"stale-session"}
{"type":"attachment","timestamp":"2026-05-29T04:00:00.000Z"}
{"type":"assistant","timestamp":"2026-05-29T04:10:00.000Z","message":{"model":"claude-opus-4-7","stop_reason":"end_turn","usage":{"input_tokens":810000}}}
STALEEOF

cat > "$FRESH_JSONL" <<'FRESHEOF'
{"type":"last-prompt","leafUuid":"fresh","sessionId":"fresh-session"}
{"type":"attachment","timestamp":"2026-05-29T05:15:00.000Z"}
{"type":"assistant","timestamp":"2026-05-29T05:20:00.000Z","message":{"model":"claude-opus-4-7","stop_reason":"end_turn","usage":{"input_tokens":180000}}}
FRESHEOF

# Critical: invert mtime order so STALE has the NEWER mtime — this is the
# pre-fix-trap condition. Pre-fix `ls -t | head -1` would pick STALE.
# Post-fix `birth-ts` selection correctly picks FRESH.
touch -t 202605290600 "$STALE_JSONL"  # newest mtime (06:00 today)
touch -t 202605290530 "$FRESH_JSONL"  # older mtime than STALE (05:30 today)

# Sanity probe: confirm ls -t order matches the trap (STALE first).
ls_first=$(ls -t "$TRANSCRIPT_DIR"/*.jsonl | head -1)
if [[ "$(basename "$ls_first")" != "$(basename "$STALE_JSONL")" ]]; then
    echo "FAIL: setup error — STALE should be the ls -t winner (mtime newest);"
    echo "      ls -t picked: $(basename "$ls_first")"
    exit 1
fi

# Drive the sweep in --json mode against the fixture.
out=$(HOME="$FAKE_HOME" \
    PATH="$FAKE_BIN:$PATH" \
    THRUM_SWEEP_IDENTITY_GLOBS="$IDENTITY_DIR/*.json" \
    bash "$SWEEP" --json --no-nudge --ctx-threshold 1 2>&1) || {
    echo "FAIL: sweep --json exited non-zero"
    echo "output:"
    echo "$out"
    exit 1
}

# Expect exactly one JSON record (the single fixture agent).
nlines=$(printf '%s\n' "$out" | grep -c '^{')
if [[ "$nlines" != "1" ]]; then
    echo "FAIL: expected 1 JSON record, got $nlines"
    echo "output:"
    echo "$out"
    exit 1
fi

# Parse the ctx_used field. FRESH-derived: "18.0% (180k/1000k opus-4-7)"
# STALE-derived: "81.0% (810k/1000k opus-4-7)".
ctx_used=$(printf '%s\n' "$out" | grep '^{' | head -1 | jq -r '.ctx_used')

case "$ctx_used" in
    "18.0%"*) echo "PASS: ctx_used=$ctx_used (FRESH JSONL selected by birth-ts despite older mtime — thrum-roeq fix holds)" ;;
    "81.0%"*) echo "FAIL: ctx_used=$ctx_used (STALE JSONL selected by mtime — thrum-roeq regression)"; exit 1 ;;
    *)        echo "FAIL: ctx_used=$ctx_used (neither 18% nor 81% — selection or extraction broken)"; exit 1 ;;
esac
