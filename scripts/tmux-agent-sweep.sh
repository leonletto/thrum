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

LINES=15
ROLE_FILTER=""   # empty = no filter
OUT=""           # empty = stdout

while [[ $# -gt 0 ]]; do
    case "$1" in
        --lines) LINES="$2"; shift 2 ;;
        --role)  ROLE_FILTER="$2"; shift 2 ;;
        --out)   OUT="$2"; shift 2 ;;
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

# Pull team JSON; bail loud if daemon is unreachable
team_json=$(thrum team --json 2>&1) || {
    echo "ERROR: thrum team --json failed:" >&2
    echo "$team_json" >&2
    exit 1
}

# jq filter: members with alive tmux, optionally filtered by role
jq_filter='.members[]
    | select(.tmux_state == "alive")
    | select($role == "" or .role == $role)
    | {agent_id, role, module, tmux_session, worktree, last_seen, status}'

# Emit a NUL-delimited stream of compact JSON objects so agent_ids with spaces survive
mapfile -t agent_lines < <(printf '%s' "$team_json" | jq -c --arg role "$ROLE_FILTER" "$jq_filter")

if [[ ${#agent_lines[@]} -eq 0 ]]; then
    echo "# tmux-agent-sweep report (no alive tmux agents matched)"
    echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    [[ -n "$ROLE_FILTER" ]] && echo "# role filter: $ROLE_FILTER"
    exit 0
fi

# Header
echo "# tmux-agent-sweep report"
echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
echo "# lines per pane: $LINES"
[[ -n "$ROLE_FILTER" ]] && echo "# role filter: $ROLE_FILTER"
echo "# agents captured: ${#agent_lines[@]}"
echo

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

    echo "===== @$agent_id · $role${module:+/$module} ====="
    echo "tmux:      $tmux_session"
    echo "worktree:  $worktree"
    echo "last_seen: $last_seen_display"
    echo "status:    $status"
    echo "ctx_used:  $ctx_used"
    echo "--- pane (bottom $LINES lines) ---"

    if [[ "$pane_capture_ok" -eq 1 ]]; then
        # Strip trailing blank lines for tighter output
        printf '%s\n' "$pane" | sed -e :a -e '/^[[:space:]]*$/{$d;N;ba' -e '}'
    else
        echo "[capture failed: $pane]"
    fi

    echo "--- end pane ---"
    echo
done
