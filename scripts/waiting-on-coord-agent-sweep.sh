#!/usr/bin/env bash
# waiting-on-coord-agent-sweep.sh — flag agents whose latest assistant message
# signals they are blocked waiting for the coordinator to weigh in.
#
# Motivated by researcher_thrum_memory (2026-05-20): agent sat blocked ~10min
# with question fully visible in pane but not in coord's inbox. Patterns mined
# empirically from the project's Claude Code conversation archive via the
# episodic-memory plugin (thrum-e1n0).
#
# Substrate: Claude JSONL transcripts at
#   ~/.claude/projects/<encoded-worktree>/<session>.jsonl
# Per the reference_claude_jsonl_state_source pattern wired into the
# error-and-context-agent-sweep.sh sibling. Other runtimes (Codex/Cursor/
# OpenCode) emit no transcript — they're skipped at v1 with "(no transcript)"
# and will land via thrum-pa34 adapter epic.
#
# Output: report-style file with one section per FLAGGED agent:
#   ===== @<agent_id> · <role>/<module> =====
#   tmux:          <session>:<window>.<pane>
#   worktree:      <path>
#   last_msg:      <Xm ago>
#   matched:       <pattern label(s)>
#   --- excerpt (last ~15 lines of latest assistant message) ---
#   <text>
#   --- end excerpt ---
#
# Exit codes:
#   0 — zero agents flagged
#   1 — one or more agents flagged (cron / hook chain can branch on this)
#   2 — usage error
#
# Usage:
#   bash scripts/waiting-on-coord-agent-sweep.sh                # default
#   bash scripts/waiting-on-coord-agent-sweep.sh --role implementer
#   bash scripts/waiting-on-coord-agent-sweep.sh --out /tmp/wait.txt
#   bash scripts/waiting-on-coord-agent-sweep.sh --excerpt-lines 25
#   bash scripts/waiting-on-coord-agent-sweep.sh --test-fixture <file>
#
# --test-fixture <file> runs the pattern matcher against the given file's
# contents (as if it were one assistant message body) and prints the matched
# labels — used by tests/scripts/waiting_on_coord_patterns_test.sh.
#
# ## Pattern library (empirical, mined 2026-05-20 from project archive)
#
# Each pattern below has been observed at least twice in real conversations
# where the agent was genuinely blocked on coord. Specificity ratings:
#   high   — rare false-positive risk; literal phrases tied to the pattern
#   medium — broader; needs context to disambiguate
#   low    — structural only; reserved for future expansion
#
# (specificity: HIGH)
#   - "PENDING LEON" banner (literal: ═══ PENDING LEON ═══ or **PENDING LEON**)
#   - "your call" / "Your call:" / "It's your call" / "What's your call?"
#   - "awaiting your" (sign-off, go, call, A/B, release-placement, nod, etc.)
#   - "need your direction" / "need you to" / "need your call/nod"
#   - "Standing by for coordinator's <X>" (call, gate, dispatch, confirm, etc.)
#   - "Standing by for <Y> coord(inator)'s confirmation"
#   - "Holding for coordinator <X>"
#   - "Waiting for your <X>" (final read, sign-off, response)
#   - "Stopping here to surface"
#   - "sign-off needed/required/pending"
#
# Soft-block patterns (specificity: HIGH; added 2026-05-20 from coord follow-up):
#   - "just say continue" / 'just say "continue"' — proposed-default soft-block
#   - "Default if you don't specify" — proposed-default soft-block
#   - "One question/confirmation/syntax point before I dive in"
#   - "If that's fine, just say <X>" — proposed-default soft-block
#
# (specificity: MEDIUM)
#   - "before I (proceed|claim|fire|start|move|dive in)" — gating on coord
#   - "surface (this|that|it|<noun>|to you/leon)" — escalation verb form
#   - "my state ... unchanged" — explicit "I'm not moving" signal
#
# Structural patterns (computed in live mode from JSONL fields, NOT regex):
#   - trailing-question-end-turn: stop_reason == end_turn AND body ends in `?`
#     (the strongest single-signal soft-block: done thinking + idle + question)
#
# Patterns deliberately NOT included (would over-fire):
#   - bare "Standing by." (every healthy idle ack ends this way)
#   - bare "?" line endings without end_turn (rhetorical questions in body)
#   - "All clear" / "No action" (coord-sweep all-clear)

set -euo pipefail

# mapfile (line 170) requires bash 4.0+; macOS /bin/bash is 3.2. The
# #!/usr/bin/env bash above picks up homebrew bash if PATH has it, but a
# `bash scripts/...` invocation through /bin/bash would silently break.
if [[ "${BASH_VERSINFO[0]}" -lt 4 ]]; then
    echo "ERROR: bash 4.0+ required (mapfile); detected $BASH_VERSION" >&2
    exit 2
fi

ROLE_FILTER=""
OUT=""
EXCERPT_LINES=15
TEST_FIXTURE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --role)            ROLE_FILTER="$2"; shift 2 ;;
        --out)             OUT="$2"; shift 2 ;;
        --excerpt-lines)   EXCERPT_LINES="$2"; shift 2 ;;
        --test-fixture)    TEST_FIXTURE="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
            exit 0
            ;;
        *) echo "Unknown flag: $1" >&2; exit 2 ;;
    esac
done

# ----------------------------------------------------------------------------
# Pattern matcher: input = full text body; output = matched pattern labels
# (one per line). Empty stdout = no match.
#
# Each rule is an ERE regex + a label. Order matters only for output
# readability; matching is independent per rule.
# ----------------------------------------------------------------------------
match_patterns() {
    local body="$1"
    # The bash =~ operator runs case-sensitive; rules use case-insensitive
    # POSIX ERE via grep -E -i. Keep regexes anchored to fragment-level shape,
    # not full-line, so multi-paragraph bodies don't slip through.
    local rules=(
        'PENDING-LEON-banner|(═══ *PENDING LEON ═══|\*\*PENDING LEON\b|🚨 ?PENDING LEON)'
        'your-call|(\bwhat'\''?s your call\??|\byour call\b *[:.?]|\bit'\''?s your call\b)'
        'awaiting-your|\bawaiting +(your|leon|coord|the coord|explicit|@coord|a +nod|the +nod)'
        'need-your-direction|\bneed +(your|leon'\''?s?|coord'\''?s?|a) +(direction|call|decision|sign-?off|input|go|answer|read|verdict|nod)'
        'need-you-to|\bneed +you +to +(lock|decide|authoriz|confirm|pick|answer|weigh)'
        'standing-by-for-coord|\bstanding +by +for +(the +)?coord(inator)?'\''?s?'
        'standing-by-for-leon|\bstanding +by +for +(leon|@?leon|your)'
        'holding-for-coord|\bholding +for +(the +)?coord(inator)?'\''?s?'
        'waiting-for-your|\b(still +)?waiting +(on|for) +(your|leon|coord)'
        'stopping-to-surface|\bstopping +(here +)?to +surface\b'
        'before-i-gate|\bbefore +I +(proceed|claim|fire|start|move|continue|spawn|dispatch|dive +in)\b'
        # surface-to-you: tightened to require an explicit verb-form
        # ("surface this/it/that/<noun> to <you/leon/coord>") so it doesn't
        # false-positive on noun-form "surface area for your X".
        'surface-to-you|\bsurface +(this|that|it|an? +[a-z]+|to +you|to +leon|for +your|to +the +coord)\b'
        'on-your-signoff|\bon +your +(sign-?off|go|approval|OK)\b'
        'sign-off-needed|\bsign-?off +(needed|required|pending)\b'
        'authorize-or-pick|\bauthoriz[ei]\b.{0,80}\bor +(do +you|wait|want)'
        # my-state-unchanged: removed the ambiguous `.*?` branch (POSIX ERE
        # doesn't support non-greedy quantifiers); kept the two literal shapes
        # actually observed in the corpus.
        'my-state-unchanged|\bmy +state +(is +|on +[a-z0-9 _.-]+ +is +)?(still +)?unchanged\b'
        # Coord follow-up patterns (soft-block, observed 2026-05-20):
        'just-say-continue|\bjust +say +"?continue"?\b'
        'default-if-not-specified|\bdefault +if +you +(don'\''?t|do +not) +specify\b'
        'one-question-before|\bone +(small +)?(question|confirm[a-z]*|syntax +point|clarif[a-z]*) +(worth +)?(getting +|i +need +)?before\b'
        'if-thats-fine-just|\bif +that'\''?s +(fine|ok|good)\b.{0,30}\bjust +say\b'
        'reply-with-go|\bif +(they|you) +reply +"?(go|yes|ok)"?\b'
    )
    local rule label re hit_any=0
    for rule in "${rules[@]}"; do
        label="${rule%%|*}"
        re="${rule#*|}"
        if printf '%s\n' "$body" | grep -E -i -q "$re"; then
            echo "$label"
            hit_any=1
        fi
    done
    return $((hit_any == 0))
}

# Test-fixture mode: read a file as if it were one assistant message body and
# print matched labels. Exit 0 if any match, 1 if none.
if [[ -n "$TEST_FIXTURE" ]]; then
    if [[ ! -r "$TEST_FIXTURE" ]]; then
        echo "ERROR: fixture not readable: $TEST_FIXTURE" >&2
        exit 2
    fi
    body=$(cat "$TEST_FIXTURE")
    if match_patterns "$body"; then
        exit 0
    else
        echo "(no match)"
        exit 1
    fi
fi

# ----------------------------------------------------------------------------
# Live sweep: enumerate alive Claude agents and pattern-match their latest
# assistant message body.
# ----------------------------------------------------------------------------
if [[ -n "$OUT" ]]; then
    exec > "$OUT"
fi

now_epoch=$(date -u +%s)

# Identity-file enumeration (same shape as error-and-context-agent-sweep.sh;
# duplicated deliberately at v1 per coord's YAGNI call on the shared helper —
# refactor when the third sweep variant lands, tracked as P3 follow-up).
shopt -s nullglob
identity_files=(
    /Users/leon/dev/opensource/thrum/.thrum/identities/*.json
    /Users/leon/.thrum/worktrees/thrum/*/.thrum/identities/*.json
)
shopt -u nullglob

mapfile -t alive_sessions < <(tmux list-sessions -F "#{session_name}" 2>/dev/null || true)
alive_set=""
for s in "${alive_sessions[@]}"; do alive_set+="|$s"; done
alive_set+="|"

agent_lines=()
for f in "${identity_files[@]}"; do
    [[ -f "$f" ]] || continue
    raw=$(jq -c '{
        agent_id: (.agent.Name // .Name // ""),
        role: (.agent.Role // .Role // ""),
        module: (.agent.Module // .Module // ""),
        tmux_session: (.tmux_session // ""),
        worktree: (.worktree // "")
    }' "$f" 2>/dev/null) || continue
    [[ -z "$raw" || "$raw" == "null" ]] && continue

    tmux_full=$(jq -r '.tmux_session' <<<"$raw")
    [[ -z "$tmux_full" ]] && continue
    session_name="${tmux_full%%:*}"
    [[ "$alive_set" != *"|$session_name|"* ]] && continue

    if [[ -n "$ROLE_FILTER" ]]; then
        role=$(jq -r '.role' <<<"$raw")
        [[ "$role" != "$ROLE_FILTER" ]] && continue
    fi

    agent_lines+=("$raw")
done

FLAGGED_BUF=$(mktemp)
FLAGGED_COUNT=0
trap 'rm -f "$FLAGGED_BUF"' EXIT

for line in "${agent_lines[@]}"; do
    agent_id=$(jq -r '.agent_id' <<<"$line")
    role=$(jq -r '.role' <<<"$line")
    module=$(jq -r '.module // ""' <<<"$line")
    tmux_session=$(jq -r '.tmux_session' <<<"$line")
    worktree=$(jq -r '.worktree // ""' <<<"$line")

    # Resolve transcript path (Claude only at v1)
    transcript=""
    if [[ -n "$worktree" ]]; then
        transcript_dir="$HOME/.claude/projects/$(echo "$worktree" | sed 's|[./]|-|g')"
        if [[ -d "$transcript_dir" ]]; then
            transcript=$(ls -t "$transcript_dir"/*.jsonl 2>/dev/null | head -1 || true)
        fi
    fi

    if [[ -z "$transcript" ]]; then
        # No JSONL — non-Claude runtime, deferred to thrum-pa34
        continue
    fi

    # Extract the latest assistant message body (all text content joined) and
    # its timestamp. Tail -200 keeps us bounded; the latest assistant message
    # is in the last 1-3 lines for typical sessions but may be further back if
    # the user has typed since.
    #
    # Two jq calls because tsv-joining would escape body newlines as literal
    # \n and ruin the excerpt rendering. Each call is cheap on a 200-line tail.
    tail_buf=$(tail -200 "$transcript" 2>/dev/null || true)
    [[ -z "$tail_buf" ]] && continue
    body=$(printf '%s\n' "$tail_buf" | jq -rs '
        (map(select(.type == "assistant")) | last) as $a |
        ($a.message.content // []
            | map(select(.type == "text") | .text)
            | join("\n\n"))
    ' 2>/dev/null || echo "")
    ts=$(printf '%s\n' "$tail_buf" | jq -rs '
        (map(select(.type == "assistant")) | last) as $a |
        ($a.timestamp // "")
    ' 2>/dev/null || echo "")
    stop_reason=$(printf '%s\n' "$tail_buf" | jq -rs '
        (map(select(.type == "assistant")) | last) as $a |
        ($a.message.stop_reason // "")
    ' 2>/dev/null || echo "")
    [[ -z "$body" ]] && continue

    # Run the matcher (verbatim regex layer)
    matches=$(match_patterns "$body" || true)
    # Structural layer: stop_reason == end_turn AND body ends with '?' (after
    # trailing whitespace). Empirically the strongest single-signal soft-block
    # indicator — agent done thinking + idle + question on the table.
    if [[ "$stop_reason" == "end_turn" ]] && [[ "$body" =~ \?[[:space:]]*$ ]]; then
        matches=$(printf '%s\ntrailing-question-end-turn' "$matches")
    fi
    # Trim leading newline if the structural rule was the only hit
    matches=$(printf '%s' "$matches" | sed '/^$/d')
    [[ -z "$matches" ]] && continue

    # Compute idle time
    last_msg_ago="(unknown)"
    if [[ -n "$ts" ]]; then
        ts_epoch=$(date -u -j -f "%Y-%m-%dT%H:%M:%S" "${ts%%.*}" +%s 2>/dev/null || echo 0)
        if [[ "$ts_epoch" -gt 0 ]]; then
            delta_min=$(( (now_epoch - ts_epoch) / 60 ))
            last_msg_ago="${delta_min}m ago"
        fi
    fi

    # Tail the body for the excerpt (last N lines)
    excerpt=$(printf '%s\n' "$body" | tail -"$EXCERPT_LINES")
    matched_labels=$(printf '%s' "$matches" | paste -sd ',' -)

    {
        echo "===== @$agent_id · $role${module:+/$module} ====="
        echo "tmux:          $tmux_session"
        echo "worktree:      $worktree"
        echo "last_msg:      $last_msg_ago"
        echo "matched:       $matched_labels"
        echo "--- excerpt (last $EXCERPT_LINES lines of latest assistant message) ---"
        printf '%s\n' "$excerpt"
        echo "--- end excerpt ---"
        echo
    } >> "$FLAGGED_BUF"
    FLAGGED_COUNT=$((FLAGGED_COUNT + 1))
done

echo "# waiting-on-coord sweep report"
echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
[[ -n "$ROLE_FILTER" ]] && echo "# role filter: $ROLE_FILTER"
echo "# alive Claude agents: ${#agent_lines[@]}; flagged: $FLAGGED_COUNT"

if [[ "$FLAGGED_COUNT" -eq 0 ]]; then
    echo "# all clear — no agents appear to be waiting on coord"
    exit 0
fi

echo
cat "$FLAGGED_BUF"
exit 1
