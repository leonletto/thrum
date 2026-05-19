#!/usr/bin/env bash
# tmux-agent-sweep.sh — capture bottom N lines of each thrum agent's tmux pane
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
#   bash scripts/tmux-agent-sweep.sh > /tmp/agent-sweep.txt
#   grep -nE "(\\? *$|Do you want|permission|Continue\\?|y/n|Y/N|[Ii]nput:)" /tmp/agent-sweep.txt
#
# Usage:
#   bash scripts/tmux-agent-sweep.sh                # default: 15 lines, all roles
#   bash scripts/tmux-agent-sweep.sh --lines 25     # custom line count
#   bash scripts/tmux-agent-sweep.sh --role implementer  # filter by role
#   bash scripts/tmux-agent-sweep.sh --out /tmp/sweep.txt # write to file
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
    echo "# tmux-agent-sweep report (no alive tmux agents matched)"
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

    # Capture pane FIRST so we can extract Ctx Used% for the header.
    # Capture-pane: -p print to stdout, -t target, -S -<N> start N lines from end.
    pane_capture_ok=1
    if ! pane=$(tmux capture-pane -p -t "$tmux_session" -S -"$LINES" 2>&1); then
        pane_capture_ok=0
    fi

    # Extract Claude Code footer's "Ctx Used: X.X%" if present in the captured
    # pane. Claude Code's status line separates words with non-breaking spaces
    # (UTF-8 \xc2\xa0), not ASCII spaces — normalize before matching. The
    # trailing `|| true` keeps set -e from killing the loop on runtimes whose
    # panes have no footer to match (Codex, Cursor) or have scrolled past it.
    if [[ "$pane_capture_ok" -eq 1 ]]; then
        ctx_used=$(printf '%s\n' "$pane" | sed $'s/\xc2\xa0/ /g' | grep -oE 'Ctx Used: [0-9]+\.[0-9]+%' | tail -1 | sed 's/Ctx Used: //' || true)
        [[ -z "$ctx_used" ]] && ctx_used="(n/a)"
    else
        ctx_used="(capture failed)"
    fi

    # Extract Anthropic API errors from the captured pane. These are
    # transient (usually 529 Overloaded or rate limits) and the right
    # remediation is `thrum tmux send <session> 'continue'` — see
    # coordinator-context-monitoring SKILL §Step 6.
    if [[ "$pane_capture_ok" -eq 1 ]]; then
        # Match Claude Code's "⎿  API Error: ..." tool-result error lines.
        # Phrasing of the error suffix varies (3-digit codes, "Server is
        # temporarily limiting requests", "Rate limited", etc.) so we don't
        # constrain the tail — but we DO anchor to the ⎿ tool-result prefix
        # so that source code containing "API Error" strings (this very script,
        # commit messages, comments) and prior sweep output (which echoes
        # captured error text back) don't trigger recursive false-positives.
        # grep returns non-zero on no-match under pipefail; suppress with || true.
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

    # ctx_used >= threshold?
    if [[ "$ctx_used" =~ ^([0-9]+)\.([0-9]+)%$ ]]; then
        ctx_int="${BASH_REMATCH[1]}"
        if [[ "$ctx_int" -ge "$CTX_THRESHOLD" ]]; then
            needs_attention=1
        fi
    elif [[ "$ctx_used" == "(capture failed)" ]]; then
        # Capture failure is a real concern — flag it
        needs_attention=1
    fi
    # ctx_used == "(n/a)" is NOT a flag — Codex/Cursor runtimes have no footer

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
echo "# tmux-agent-sweep report"
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
