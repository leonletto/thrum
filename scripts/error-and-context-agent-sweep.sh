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
#   bash scripts/error-and-context-agent-sweep.sh --out /tmp/sweep.txt # write report to file
#   bash scripts/error-and-context-agent-sweep.sh --no-nudge     # skip API-error continue nudges
#
# Always emits at most one "ALERT:" line to stdout when any agent is flagged.
# The ALERT line is the consolidated signal a `thrum monitor` registration
# matches against (--match '^ALERT:'). When --out is given, the full
# per-agent report is written to that file; stdout carries the ALERT line
# (and nothing else) plus any final exit-status text. When --out is omitted,
# both the full report AND the ALERT line go to stdout. Silent when clean:
# no ALERT line is emitted when no agent crosses a threshold.
#
# State file (consecutive-sweep STUCK detection):
#   $THRUM_CONTEXT_SWEEP_STATE (override) OR
#   ${XDG_STATE_HOME:-$HOME/.local/state}/thrum/context-sweep-state.json
# Tracks the set of agents in api-error state from the previous sweep so
# agents in api-error on 2 consecutive sweeps are flagged STUCK in the
# ALERT line. The state file is created on first run; missing parent dirs
# are created on demand. Never lives inside the repo.
#
# Exits 0 even on per-agent capture errors (continues sweeping). Exits non-zero
# only if `thrum team --json` itself fails.

set -euo pipefail

LINES=10
ROLE_FILTER=""   # empty = no filter
OUT=""           # empty = stdout
SHOW_ALL=0       # 0 = only emit flagged agents (default); 1 = emit all
CTX_THRESHOLD=50 # int %; agents at-or-above this are flagged
NUDGE=1          # 1 = auto-nudge api-error panes (default); --no-nudge sets 0
SILENCE_THRESHOLD_MIN=10  # min; thrum-9neg L5; --silence-threshold-min overrides

while [[ $# -gt 0 ]]; do
    case "$1" in
        --lines) LINES="$2"; shift 2 ;;
        --role)  ROLE_FILTER="$2"; shift 2 ;;
        --out)   OUT="$2"; shift 2 ;;
        --all)   SHOW_ALL=1; shift ;;
        --ctx-threshold) CTX_THRESHOLD="$2"; shift 2 ;;
        --no-nudge) NUDGE=0; shift ;;
        --silence-threshold-min) SILENCE_THRESHOLD_MIN="$2"; shift 2 ;;
        -h|--help)
            sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
            exit 0
            ;;
        *) echo "Unknown flag: $1" >&2; exit 2 ;;
    esac
done

# Decide where the per-agent report goes. When --out is given, the report
# goes to the file and stdout is reserved for the ALERT line. When --out is
# absent the report goes to stdout, prefixed by the ALERT line.
REPORT_DEST="/dev/stdout"
if [[ -n "$OUT" ]]; then
    REPORT_DEST="$OUT"
    : > "$REPORT_DEST"  # truncate
fi

# Resolve the state file path. Override is supported for tests + the
# daemon-driven monitor use-case (single canonical location across sweeps).
STATE_FILE="${THRUM_CONTEXT_SWEEP_STATE:-${XDG_STATE_HOME:-$HOME/.local/state}/thrum/context-sweep-state.json}"
mkdir -p "$(dirname "$STATE_FILE")" 2>/dev/null || true

# Load the previous sweep's api-error agent set into a lookup string
# |agent_a|agent_b| for fast substring membership tests. Tolerant of a
# missing/corrupt state file.
prev_api_set="|"
if [[ -f "$STATE_FILE" ]]; then
    while IFS= read -r a; do
        [[ -n "$a" ]] && prev_api_set+="$a|"
    done < <(jq -r '.api_error_agents[]? // empty' "$STATE_FILE" 2>/dev/null || true)
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
        status: (.agent_status // ""),
        intent: (.intent // "")
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
    {
        echo "# error-and-context-agent-sweep report (no alive tmux agents matched)"
        echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
        [[ -n "$ROLE_FILTER" ]] && echo "# role filter: $ROLE_FILTER"
    } > "$REPORT_DEST"
    # Reset state file: no agents observed → empty api-error set. Atomic
    # write via tmp + mv so an interrupted run doesn't zero next-sweep
    # STUCK detection.
    reset_tmp=$(mktemp "${STATE_FILE}.tmp.XXXXXX" 2>/dev/null) || reset_tmp=""
    if [[ -n "$reset_tmp" ]]; then
        printf '%s\n' '{"api_error_agents":[],"timestamp":"'"$(date -u +"%Y-%m-%dT%H:%M:%SZ")"'"}' > "$reset_tmp" 2>/dev/null \
            && mv "$reset_tmp" "$STATE_FILE" 2>/dev/null \
            || rm -f "$reset_tmp" 2>/dev/null
    fi
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
# Per-agent flag bookkeeping for the consolidated ALERT line.
alert_segments=()
flagged_count=0
stuck_count=0
stuck_working_count=0
tier3_count=0  # ctx >= 85%
tier2_count=0  # 70% <= ctx < 85%
cur_api_agents=()
trap 'rm -f "$ATTENTION_BUF" "$ALL_BUF"' EXIT

for line in "${agent_lines[@]}"; do
    agent_id=$(jq -r '.agent_id' <<<"$line")
    role=$(jq -r '.role' <<<"$line")
    module=$(jq -r '.module // ""' <<<"$line")
    tmux_session=$(jq -r '.tmux_session' <<<"$line")
    worktree=$(jq -r '.worktree // ""' <<<"$line")
    last_seen=$(jq -r '.last_seen' <<<"$line")
    status=$(jq -r '.status' <<<"$line")
    intent=$(jq -r '.intent // ""' <<<"$line")

    # Compute "Xm ago" if last_seen parses; fall back to raw on failure
    last_seen_epoch=$(date -u -j -f "%Y-%m-%dT%H:%M:%S" "${last_seen%%.*}" +%s 2>/dev/null || echo 0)
    if [[ "$last_seen_epoch" -gt 0 ]]; then
        delta_min=$(( (now_epoch - last_seen_epoch) / 60 ))
        last_seen_display="$last_seen ($delta_min m ago)"
    else
        last_seen_display="$last_seen"
    fi

    # Tmux silence age via native window_activity (per feedback_byte_equality_pane_detection #1).
    # Compares to now_epoch; subsecond op, no diffing. Stripped to bare session name
    # because window_activity is keyed by tmux session, not the :W.P target form.
    tmux_session_only="${tmux_session%%:*}"
    last_activity=$(tmux display-message -p -t "$tmux_session_only" '#{window_activity}' 2>/dev/null || echo 0)
    silence_sec=$((now_epoch - last_activity))
    silence_threshold_sec=$((SILENCE_THRESHOLD_MIN * 60))

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

    # STUCK-WORKING flag computation (thrum-9neg L5). Three conditions per dispatch:
    #   (a) agent_status = "working" (agent claims to be mid-work)
    #   (b) tmux silence > SILENCE_THRESHOLD_MIN (pane has produced no output)
    #   (c) JSONL transcript's last assistant message has stop_reason = tool_use
    #       (state="working" in our derived vocabulary) AND last_msg > threshold
    #
    # Condition (c) tightens "no recent JSONL tool calls" to "JSONL says agent IS
    # mid-tool-call but no progress" — distinguishes a hung tool from a clean
    # end_turn that just hasn't been reflected in agent_status yet (a status-drift,
    # not a stuck; surfaced separately in a future signal).
    #
    # Edge case: state="(no assistant msg)" (fresh agent, no transcript output yet)
    # falls through neither the JSONL branch (state != "working") nor the non-Claude
    # branch (last_msg_ago != "(n/a)"); stuck_working stays 0. Correct by design —
    # an agent with no tool calls yet cannot be stuck mid-tool-call.
    #
    # Warm-hold exemption per L4: if intent starts with `warm-hold:`, skip the
    # classification entirely. Intent is read in the per-agent jq pass (E2.5).
    #
    # last_msg_ago format produced earlier in this loop: "<N>m ago" OR "(n/a)" for
    # non-Claude runtimes (no transcript). For non-Claude, tmux silence alone is
    # the signal — no way to distinguish tool-use vs end-turn without a transcript.
    #
    # Note: This block only SETS the stuck_working flag; needs_attention + reason_parts
    # integration happens in E2.8 (peer of is_stuck check at line 367+, AFTER the
    # needs_attention=0 reset at line 320). Setting needs_attention here would be
    # wiped by line 320.
    stuck_working=0
    if [[ "$status" == "working" && "$silence_sec" -gt "$silence_threshold_sec" \
          && ! "$intent" =~ ^warm-hold: ]]; then
        if [[ "$state" == "working" && "$last_msg_ago" =~ ^([0-9]+)m ]]; then
            last_msg_min="${BASH_REMATCH[1]}"
            if [[ "$last_msg_min" -gt "$SILENCE_THRESHOLD_MIN" ]]; then
                stuck_working=1
            fi
        elif [[ "$last_msg_ago" == "(n/a)" ]]; then
            # Non-Claude runtime (no transcript). Tmux silence alone is the signal.
            stuck_working=1
        fi
    fi

    # Build this agent's full section into a temp variable
    agent_section=""
    agent_section+="===== @$agent_id · $role${module:+/$module} =====\n"
    agent_section+="tmux:       $tmux_session\n"
    agent_section+="worktree:   $worktree\n"
    agent_section+="last_seen:  $last_seen_display\n"
    agent_section+="status:     $status\n"
    agent_section+="intent:     $intent\n"
    agent_section+="silence:    ${silence_sec}s (threshold ${silence_threshold_sec}s)\n"
    agent_section+="stuck_working: $stuck_working\n"
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

    # Evaluate whether this agent needs attention + classify for ALERT line.
    needs_attention=0
    ctx_int=""
    tier=""    # "tier3" (>=85%), "tier2" (70-84%), or empty
    reason_parts=()  # joined into the ALERT segment after the % marker

    # ctx_used >= threshold? Format is "X.X% (...)" — match the leading percent.
    if [[ "$ctx_used" =~ ^([0-9]+)\.([0-9]+)% ]]; then
        ctx_int="${BASH_REMATCH[1]}"
        if [[ "$ctx_int" -ge "$CTX_THRESHOLD" ]]; then
            needs_attention=1
        fi
        if [[ "$ctx_int" -ge 85 ]]; then
            tier="tier3"
        elif [[ "$ctx_int" -ge 70 ]]; then
            tier="tier2"
        fi
    elif [[ "$ctx_used" == "(capture failed)" ]]; then
        # Capture failure is a real concern — flag it
        needs_attention=1
        reason_parts+=("capture-fail")
    fi
    # ctx_used == "(n/a)" is NOT a flag — Codex/Cursor runtimes have no transcript

    # api_errors present?
    has_api_err=0
    if [[ "$api_errors" != "(none)" && "$api_errors" != "(capture failed)" ]]; then
        needs_attention=1
        has_api_err=1
        reason_parts+=("api-err")
        cur_api_agents+=("$agent_id")
        # Auto-nudge: API errors (rate limits, transient server-side issues) are
        # deterministically recoverable by typing "continue" into the affected
        # pane — Claude Code retries the previous tool call from the same session
        # state. The thrum tmux send wrapper's queue stalls on fully-silent panes
        # (filed as thrum-7yhs), so we bypass via raw tmux send-keys here. Only
        # fires when this specific agent's pane contains an api_errors match, so
        # no cross-agent carry-over. --no-nudge disables this entirely for
        # daemon-driven runs where the operator's tmux context isn't available.
        if [[ "$NUDGE" -eq 1 && -n "$tmux_session" && "$tmux_session" != "(none)" ]]; then
            tmux_target="${tmux_session%%:*}:0.0"
            tmux send-keys -t "$tmux_target" "continue" Enter 2>/dev/null && \
                auto_nudges+=("$agent_id @ $tmux_target") || \
                auto_nudges+=("$agent_id @ $tmux_target (FAILED)")
        fi
    fi

    # STUCK detection: api-error this sweep AND last sweep.
    is_stuck=0
    if [[ "$has_api_err" -eq 1 && "$prev_api_set" == *"|$agent_id|"* ]]; then
        is_stuck=1
        reason_parts+=("STUCK")
    fi

    # stuck-working contributes its own reason segment (independent of api-err STUCK).
    if [[ "$stuck_working" -eq 1 ]]; then
        reason_parts+=("stuck-working")
        needs_attention=1
    fi

    if [[ "$needs_attention" -eq 1 ]]; then
        printf '%b' "$agent_section" >> "$ATTENTION_BUF"
        ATTENTION_COUNT=$((ATTENTION_COUNT + 1))
        flagged_count=$((flagged_count + 1))
        [[ "$is_stuck" -eq 1 ]] && stuck_count=$((stuck_count + 1))
        [[ "$stuck_working" -eq 1 ]] && stuck_working_count=$((stuck_working_count + 1))
        case "$tier" in
            tier3) tier3_count=$((tier3_count + 1)) ;;
            tier2) tier2_count=$((tier2_count + 1)) ;;
        esac
        # Build per-agent ALERT segment: name(pct,reason,...).
        pct_label="?"
        [[ -n "$ctx_int" ]] && pct_label="${ctx_int}%"
        joined="$pct_label"
        for r in "${reason_parts[@]}"; do
            joined+=",$r"
        done
        alert_segments+=("${agent_id}(${joined})")
    fi
done

# Persist current sweep's api-error set for next-sweep STUCK detection.
# Atomic write: build into a tmp file in the same directory, then mv into
# place. Single-writer (the daemon's monitor) makes torn-write practically
# impossible, but a mid-write SIGKILL/disk-full would otherwise leave the
# state file blank and zero the next sweep's STUCK history. Tolerant of
# write failure (state persistence is best-effort — STUCK detection
# degrades to "always false" if persistence fails, but the script still
# works).
state_tmp=""
state_tmp=$(mktemp "${STATE_FILE}.tmp.XXXXXX" 2>/dev/null) || state_tmp=""
if [[ -n "$state_tmp" ]]; then
    {
        if [[ ${#cur_api_agents[@]} -eq 0 ]]; then
            printf '%s\n' '{"api_error_agents":[],"timestamp":"'"$(date -u +"%Y-%m-%dT%H:%M:%SZ")"'"}'
        else
            printf '%s\n' "${cur_api_agents[@]}" | jq -R . | jq -s --arg ts "$(date -u +"%Y-%m-%dT%H:%M:%SZ")" '{api_error_agents: ., timestamp: $ts}'
        fi
    } > "$state_tmp" 2>/dev/null && mv "$state_tmp" "$STATE_FILE" 2>/dev/null || rm -f "$state_tmp" 2>/dev/null
fi

# Consolidated ALERT line — always goes to STDOUT (separate from the
# per-agent report, which goes to REPORT_DEST). The thrum monitor
# registration matches against this single line via --match '^ALERT:'.
# Silent when clean: no ALERT line if zero agents were flagged.
if [[ "$flagged_count" -gt 0 ]]; then
    alert_body=$(IFS='; '; echo "${alert_segments[*]}")
    echo "ALERT: flagged=$flagged_count stuck=$stuck_count stuck_working=$stuck_working_count tier3=$tier3_count tier2=$tier2_count — $alert_body"
fi

# Header → report file (or stdout if no --out)
{
    echo "# error-and-context-agent-sweep report"
    echo "# generated: $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    echo "# lines per pane: $LINES"
    [[ -n "$ROLE_FILTER" ]] && echo "# role filter: $ROLE_FILTER"
    echo "# alive agents: ${#agent_lines[@]}; flagged: $ATTENTION_COUNT (ctx>=${CTX_THRESHOLD}% or api_errors or capture-fail or stuck-working); stuck: $stuck_count; stuck_working: $stuck_working_count; tier3(>=85%): $tier3_count; tier2(70-84%): $tier2_count"
    if [[ ${#auto_nudges[@]} -gt 0 ]]; then
        echo "# auto-nudged ${#auto_nudges[@]} agent(s) on api_errors with 'continue':"
        for n in "${auto_nudges[@]}"; do echo "#   - $n"; done
    elif [[ "$NUDGE" -eq 0 ]]; then
        echo "# auto-nudge disabled (--no-nudge)"
    fi
} >> "$REPORT_DEST"

# Emit body: --all forces full emit; otherwise only flagged agents
{
    if [[ "$SHOW_ALL" -eq 1 ]]; then
        echo
        cat "$ALL_BUF"
    elif [[ "$ATTENTION_COUNT" -eq 0 ]]; then
        echo "# all clear — no agents need attention. Run with --all to see full fleet."
    else
        echo
        cat "$ATTENTION_BUF"
    fi
} >> "$REPORT_DEST"
