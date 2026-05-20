#!/usr/bin/env bash
# error-and-context-agent-sweep.sh — flag agents nearing context limits or in
# API-error state. Captures bottom N lines of each thrum agent's tmux pane plus
# JSONL-derived state (ctx %, model, stop_reason, api_errors) for Claude runtime.
#
# Renamed from tmux-agent-sweep.sh (2026-05-20, thrum-e1n0): the script is no
# longer tmux-only — Claude state comes from the JSONL transcript per
# reference_claude_jsonl_state_source. The "error-and-context" prefix
# distinguishes it from waiting-on-coord-agent-sweep.sh (sibling sweep, same
# enumeration substrate, different lens: detects agents blocked waiting for
# coordinator decisions via assistant-message pattern match).
#
# Outputs a single report-style file with one section per agent:
#   ===== @<agent_id> · <role>/<module> =====
#   tmux:     <session>:<window>.<pane> (<state>)
#   worktree: <path>
#   last_seen: <RFC3339> (<minutes_ago>m ago)
#   status:   <status>
#   --- pane (bottom <N> lines) ---
#   <captured lines>
#   --- end pane ---
#
# Coordinator greps the report for prompts/questions/stalls:
#   bash scripts/error-and-context-agent-sweep.sh > /tmp/agent-sweep.txt
#   grep -nE "(\\? *$|Do you want|permission|Continue\\?|y/n|Y/N|[Ii]nput:)" /tmp/agent-sweep.txt
#
# Usage:
#   bash scripts/error-and-context-agent-sweep.sh                # default: 15 lines, all roles
#   bash scripts/error-and-context-agent-sweep.sh --lines 25     # custom line count
#   bash scripts/error-and-context-agent-sweep.sh --role implementer  # filter by role
#   bash scripts/error-and-context-agent-sweep.sh --out /tmp/sweep.txt # write to file
#
# Exits 0 even on per-agent capture errors (continues sweeping). Exits non-zero
# only if `thrum team --json` itself fails.

set -euo pipefail

LINES=10
ROLE_FILTER=""   # empty = no filter
OUT=""           # empty = stdout
SHOW_ALL=0       # 0 = only emit flagged agents (default); 1 = emit all
CTX_THRESHOLD=50 # int %; agents at-or-above this are flagged

while [[ $# -gt 0 ]]; do
    case "$1" in
        --lines) LINES="$2"; shift 2 ;;
        --role)  ROLE_FILTER="$2"; shift 2 ;;
        --out)   OUT="$2"; shift 2 ;;
        --all)   SHOW_ALL=1; shift ;;
        --ctx-threshold) CTX_THRESHOLD="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
            exit 0
            ;;
        *) echo "Unknown flag: $1" >&2; exit 2 ;;
    esac
done

# Pipe to file if --out, else stdout
if [[ -n "$OUT" ]]; then
    exec > "$OUT"
fi

now_epoch=$(date -u +%s)

# Build the agent list from identity files + tmux list-sessions directly,
# bypassing `thrum team --json` to avoid CLI-startup identity-refresh overhead
# under fleet load (filed as a perf-investigation bd; see thrum-xir.36).
#
# Sources:
#   - Per-worktree identity files at <worktree>/.thrum/identities/*.json
#   - Each identity carries: agent.Name, agent.Role, agent.Module, tmux_session,
#     worktree, agent_status, updated_at
# Liveness check:
#   - tmux list-sessions for alive set; identity's tmux_session is "<name>:W.P"
#     so we strip the suffix and check inclusion

# Globs: main repo + ~/.thrum/worktrees/thrum/* (extend if your layout differs)
shopt -s nullglob
identity_files=(
    /Users/leon/dev/opensource/thrum/.thrum/identities/*.json
    /Users/leon/.thrum/worktrees/thrum/*/.thrum/identities/*.json
)
shopt -u nullglob

# Build alive-set string for fast membership check (|name1|name2|...|)
mapfile -t alive_sessions < <(tmux list-sessions -F "#{session_name}" 2>/dev/null || true)
alive_set=""
for s in "${alive_sessions[@]}"; do alive_set+="|$s"; done
alive_set+="|"

# Emit one compact JSON per qualifying identity (alive tmux + role match).
# Same shape as the old jq filter: {agent_id, role, module, tmux_session, worktree, last_seen, status}
agent_lines=()
for f in "${identity_files[@]}"; do
    [[ -f "$f" ]] || continue
    # Single jq invocation per file extracts everything
    raw=$(jq -c '{
        agent_id: (.agent.Name // .Name // ""),
        role: (.agent.Role // .Role // ""),
        module: (.agent.Module // .Module // ""),
        tmux_session: (.tmux_session // ""),
        worktree: (.worktree // ""),
        last_seen: (.updated_at // ""),
        status: (.agent_status // "")
    }' "$f" 2>/dev/null) || continue
    [[ -z "$raw" || "$raw" == "null" ]] && continue

    # Extract session name (strip :W.P suffix) for alive check
    tmux_full=$(jq -r '.tmux_session' <<<"$raw")
    [[ -z "$tmux_full" ]] && continue
    session_name="${tmux_full%%:*}"
    [[ "$alive_set" != *"|$session_name|"* ]] && continue

    # Role filter
    if [[ -n "$ROLE_FILTER" ]]; then
        role=$(jq -r '.role' <<<"$raw")
        [[ "$role" != "$ROLE_FILTER" ]] && continue
    fi

    agent_lines+=("$raw")
done

if [[ ${#agent_lines[@]} -eq 0 ]]; then
    echo "# error-and-context-agent-sweep report (no alive tmux agents matched)"
    echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    [[ -n "$ROLE_FILTER" ]] && echo "# role filter: $ROLE_FILTER"
    exit 0
fi

# Output strategy: buffer each agent's section to a temp file. Only emit
# (a) all agents if --all, or (b) only flagged agents (ctx >= threshold,
# api_errors present, or capture failure). Keeps healthy-fleet output to
# a 2-line "all clear" header. Default threshold is 50% — matches the
# coordinator-context-monitoring SKILL's 50% directed-restart tier.
ATTENTION_BUF=$(mktemp)
ALL_BUF=$(mktemp)
ATTENTION_COUNT=0
auto_nudges=()
trap 'rm -f "$ATTENTION_BUF" "$ALL_BUF"' EXIT

for line in "${agent_lines[@]}"; do
    agent_id=$(jq -r '.agent_id' <<<"$line")
    role=$(jq -r '.role' <<<"$line")
    module=$(jq -r '.module // ""' <<<"$line")
    tmux_session=$(jq -r '.tmux_session' <<<"$line")
    worktree=$(jq -r '.worktree // ""' <<<"$line")
    last_seen=$(jq -r '.last_seen' <<<"$line")
    status=$(jq -r '.status' <<<"$line")

    # Compute "Xm ago" if last_seen parses; fall back to raw on failure
    last_seen_epoch=$(date -u -j -f "%Y-%m-%dT%H:%M:%S" "${last_seen%%.*}" +%s 2>/dev/null || echo 0)
    if [[ "$last_seen_epoch" -gt 0 ]]; then
        delta_min=$(( (now_epoch - last_seen_epoch) / 60 ))
        last_seen_display="$last_seen ($delta_min m ago)"
    else
        last_seen_display="$last_seen"
    fi

    # Capture pane FIRST so we can extract api_errors and fallback ctx footer.
    # Capture-pane: -p print to stdout, -t target, -S -<N> start N lines from end.
    pane_capture_ok=1
    if ! pane=$(tmux capture-pane -p -t "$tmux_session" -S -"$LINES" 2>&1); then
        pane_capture_ok=0
    fi

    # Resolve transcript path for Claude Code agents (encode worktree path by
    # replacing / and . with -). Both ctx_used and api_errors derive from the
    # transcript when available — same source ccstatusline uses for the in-TUI
    # status footer, but reliable regardless of pane scroll state. Daemon-side
    # implementation lives in thrum-j9zg.
    transcript=""
    if [[ -n "$worktree" ]]; then
        transcript_dir="$HOME/.claude/projects/$(echo "$worktree" | sed 's|[./]|-|g')"
        if [[ -d "$transcript_dir" ]]; then
            transcript=$(ls -t "$transcript_dir"/*.jsonl 2>/dev/null | head -1 || true)
        fi
    fi

    # Extract richer state from JSONL transcript in one jq pass:
    #   ctx tokens + window + model + stop_reason + last-assistant timestamp +
    #   api-error text (if latest assistant is one). All derived from the tail
    #   of the transcript so historical state doesn't leak into the report.
    # Falls through to "(n/a)" / pane fallbacks when no transcript (non-claude).
    ctx_used="(n/a)"
    state="(n/a)"
    last_msg_ago="(n/a)"
    api_errors="(none)"
    model=""
    if [[ -n "$transcript" ]]; then
        jsonl_state=$(tail -200 "$transcript" 2>/dev/null | jq -rs '
            (map(select(.type == "assistant")) | last) as $a |
            (map(select(.message.usage != null)) | last | .message.usage) as $u |
            [
              ($u // {} | ((.input_tokens // 0) + (.cache_creation_input_tokens // 0) + (.cache_read_input_tokens // 0)) | tostring),
              ($a.message.model // ""),
              ($a.message.stop_reason // ""),
              ($a.timestamp // ""),
              (($a.message.content // [])[0] | select(.type == "text") | .text | select(startswith("API Error"))) // ""
            ] | @tsv
        ' 2>/dev/null || echo "")
        if [[ -n "$jsonl_state" ]]; then
            IFS=$'\t' read -r used_tokens model stop_reason ts api_text <<<"$jsonl_state"
            # Window detection by model (1M for Opus 4.7 1m-context fleet default;
            # 200k otherwise — conservative for unknown models).
            case "$model" in
                claude-opus-4-7*) window=1000000 ;;
                *) window=200000 ;;
            esac
            if [[ -n "$used_tokens" && "$used_tokens" != "0" ]]; then
                ctx_used=$(awk -v u="$used_tokens" -v w="$window" 'BEGIN { printf "%.1f%% (%dk/%dk %s)", (u/w)*100, u/1000, w/1000, "MODEL" }' | sed "s/MODEL/${model##claude-}/")
            fi
            # Activity state from stop_reason
            case "$stop_reason" in
                end_turn) state="idle" ;;
                tool_use) state="working" ;;
                stop_sequence) state="error" ;;
                "") state="(no assistant msg)" ;;
                *) state="$stop_reason" ;;
            esac
            # Interaction-level idleness from latest assistant timestamp
            if [[ -n "$ts" ]]; then
                ts_epoch=$(date -u -j -f "%Y-%m-%dT%H:%M:%S" "${ts%%.*}" +%s 2>/dev/null || echo 0)
                if [[ "$ts_epoch" -gt 0 ]]; then
                    delta_min=$(( (now_epoch - ts_epoch) / 60 ))
                    last_msg_ago="${delta_min}m ago"
                fi
            fi
            # api_errors flag from latest-assistant-message text
            [[ -n "$api_text" ]] && api_errors="$api_text"
        fi
    elif [[ "$pane_capture_ok" -eq 1 ]]; then
        # Non-claude runtime: pane scan for api_errors with ⎿ anchor.
        api_errors=$(printf '%s\n' "$pane" | { grep -oE '⎿[[:space:]]+API Error[^\n]*' || true; } | sort -u | paste -sd '; ' -)
        [[ -z "$api_errors" ]] && api_errors="(none)"
    else
        api_errors="(capture failed)"
    fi

    # Build this agent's full section into a temp variable
    agent_section=""
    agent_section+="===== @$agent_id · $role${module:+/$module} =====\n"
    agent_section+="tmux:       $tmux_session\n"
    agent_section+="worktree:   $worktree\n"
    agent_section+="last_seen:  $last_seen_display\n"
    agent_section+="status:     $status\n"
    agent_section+="ctx_used:   $ctx_used\n"
    agent_section+="state:      $state\n"
    agent_section+="last_msg:   $last_msg_ago\n"
    agent_section+="api_errors: $api_errors\n"
    agent_section+="--- pane (bottom $LINES lines) ---\n"
    if [[ "$pane_capture_ok" -eq 1 ]]; then
        pane_trimmed=$(printf '%s\n' "$pane" | sed -e :a -e '/^[[:space:]]*$/{$d;N;ba' -e '}')
        agent_section+="$pane_trimmed\n"
    else
        agent_section+="[capture failed: $pane]\n"
    fi
    agent_section+="--- end pane ---\n\n"

    # Always append to the all-buffer
    printf '%b' "$agent_section" >> "$ALL_BUF"

    # Evaluate whether this agent needs attention
    needs_attention=0

    # ctx_used >= threshold? Format is "X.X% (...)" — match the leading percent.
    if [[ "$ctx_used" =~ ^([0-9]+)\.([0-9]+)% ]]; then
        ctx_int="${BASH_REMATCH[1]}"
        if [[ "$ctx_int" -ge "$CTX_THRESHOLD" ]]; then
            needs_attention=1
        fi
    elif [[ "$ctx_used" == "(capture failed)" ]]; then
        # Capture failure is a real concern — flag it
        needs_attention=1
    fi
    # ctx_used == "(n/a)" is NOT a flag — Codex/Cursor runtimes have no transcript

    # api_errors present?
    if [[ "$api_errors" != "(none)" && "$api_errors" != "(capture failed)" ]]; then
        needs_attention=1
        # Auto-nudge: API errors (rate limits, transient server-side issues) are
        # deterministically recoverable by typing "continue" into the affected
        # pane — Claude Code retries the previous tool call from the same session
        # state. The thrum tmux send wrapper's queue stalls on fully-silent panes
        # (filed as thrum-7yhs), so we bypass via raw tmux send-keys here. Only
        # fires when this specific agent's pane contains an api_errors match, so
        # no cross-agent carry-over.
        if [[ -n "$tmux_session" && "$tmux_session" != "(none)" ]]; then
            tmux_target="${tmux_session%%:*}:0.0"
            tmux send-keys -t "$tmux_target" "continue" Enter 2>/dev/null && \
                auto_nudges+=("$agent_id @ $tmux_target") || \
                auto_nudges+=("$agent_id @ $tmux_target (FAILED)")
        fi
    fi

    if [[ "$needs_attention" -eq 1 ]]; then
        printf '%b' "$agent_section" >> "$ATTENTION_BUF"
        ATTENTION_COUNT=$((ATTENTION_COUNT + 1))
    fi
done

# Header
echo "# error-and-context-agent-sweep report"
echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
echo "# lines per pane: $LINES"
[[ -n "$ROLE_FILTER" ]] && echo "# role filter: $ROLE_FILTER"
echo "# alive agents: ${#agent_lines[@]}; flagged: $ATTENTION_COUNT (ctx>=${CTX_THRESHOLD}% or api_errors or capture-fail)"
if [[ ${#auto_nudges[@]} -gt 0 ]]; then
    echo "# auto-nudged ${#auto_nudges[@]} agent(s) on api_errors with 'continue':"
    for n in "${auto_nudges[@]}"; do echo "#   - $n"; done
fi

# Emit body: --all forces full emit; otherwise only flagged agents
if [[ "$SHOW_ALL" -eq 1 ]]; then
    echo
    cat "$ALL_BUF"
elif [[ "$ATTENTION_COUNT" -eq 0 ]]; then
    echo "# all clear — no agents need attention. Run with --all to see full fleet."
else
    echo
    cat "$ATTENTION_BUF"
fi
