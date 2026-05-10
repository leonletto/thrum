#!/usr/bin/env bash
# tests/release/helpers/assert-daemon.sh — thrum-daemon-state predicates.
# Each function returns 0 on pass, non-zero on fail, prints a diagnostic
# line on fail, and reads its own values directly from the daemon (no
# caller-state dependency beyond FIXTURE_REPO).

# Run thrum against the fixture's daemon. Caller may override THRUM_NAME etc.
_thrum() {
  thrum --repo "${FIXTURE_REPO:-.}" "$@"
}

# Run thrum impersonating a specific agent (so 'inbox' returns that agent's
# inbox view). Used by recipient-filtered predicates.
#
# thrum's cross_worktree identity guard compares the caller's PID ancestry
# against the session the agent was registered in. When called from within a
# Claude session the guard fires and inbox returns empty. We route through
# ephemeral_te_exec (scripts/tmux-exec pool pane) to break the PID chain.
# The cwd is set to FIXTURE_REPO so identity files resolve correctly.
_thrum_as() {
  local agent="$1"; shift
  # ephemeral_te_exec runs with --cwd FIXTURE_REPO so thrum's git-discovery
  # finds the right repo, but pass --repo explicitly to defend against any
  # caller invoking us from a different cwd.
  ephemeral_te_exec THRUM_NAME="$agent" -- thrum --repo "${FIXTURE_REPO:-.}" "$@"
}

assert_daemon_agent_registered() {
  local agent="$1" role="$2" module="$3"
  local match
  match=$(_thrum agent list --json 2>/dev/null \
    | jq -r --arg a "$agent" --arg r "$role" --arg m "$module" \
        '.agents.agents[]? | select(.agent_id==$a and .role==$r and .module==$m) | .agent_id' \
    || true)
  if [[ -n "$match" ]]; then return 0; fi
  echo "assert-daemon.agent_registered: no match for agent=$agent role=$role module=$module" >&2
  return 1
}

# Real schema (verified):
#   thrum inbox --json --all returns {"messages": [<msg>, ...]}
#   each msg: {message_id, thread_id, reply_to, agent_id (sender),
#              body: {content, format}, created_at, deleted, is_read}
#   The recipient is implicit — inbox shows the calling agent's view.
assert_daemon_message_delivered() {
  local to="$1" from="${2:-}" pattern="${3:-}"
  local match
  match=$(_thrum_as "$to" inbox --json --all 2>/dev/null \
    | jq -r --arg from "$from" --arg pat "$pattern" \
        '(.messages // .) | .[]? | select(($from=="" or .agent_id==$from) and ($pat=="" or ((.body.content // "") | test($pat)))) | .message_id' \
    || true)
  if [[ -n "$match" ]]; then return 0; fi
  echo "assert-daemon.message_delivered: no message match (to=$to from=$from pattern=$pattern)" >&2
  return 1
}

assert_daemon_agent_replied_to() {
  local replier="$1" replied_to="$2"
  # The reply itself sits in the original sender's inbox. We don't always
  # know that sender here, so search across all participants by querying
  # the daemon's full message log via --all from the replier's view —
  # the replier's outbox is reflected in their thread. If no reliable
  # reply lookup CLI exists, fall back to scanning every known agent's
  # inbox; for fixture tests this is bounded to ~3 agents.
  local match
  match=$(_thrum_as "$replier" inbox --json --all 2>/dev/null \
    | jq -r --arg r "$replier" --arg m "$replied_to" \
        '(.messages // .) | .[]? | select(.agent_id==$r and .reply_to==$m) | .message_id' \
    || true)
  if [[ -n "$match" ]]; then return 0; fi
  echo "assert-daemon.agent_replied_to: no reply from=$replier to=$replied_to (note: scoped to replier's inbox; may need cross-agent scan)" >&2
  return 1
}

# Recency check on last_seen_at: every registered agent has this field, so
# we additionally require the heartbeat to be within RECENCY_WINDOW seconds.
ASSERT_DAEMON_RECENCY_S="${ASSERT_DAEMON_RECENCY_S:-30}"

assert_daemon_agent_session_active() {
  local agent="$1"
  local last_seen
  last_seen=$(_thrum agent list --json 2>/dev/null \
    | jq -r --arg a "$agent" \
        '.agents.agents[]? | select(.agent_id==$a) | (.last_seen_at // "")' \
    || true)
  if [[ -z "$last_seen" || "$last_seen" == "null" ]]; then
    echo "assert-daemon.agent_session_active: no last_seen_at for $agent" >&2
    return 1
  fi
  local last_epoch now_epoch delta
  last_epoch=$(date -j -f '%Y-%m-%dT%H:%M:%S' "${last_seen%%.*}" +%s 2>/dev/null || \
               date -d "$last_seen" +%s 2>/dev/null || echo 0)
  now_epoch=$(date +%s)
  delta=$((now_epoch - last_epoch))
  if (( delta <= ASSERT_DAEMON_RECENCY_S )); then return 0; fi
  echo "assert-daemon.agent_session_active: $agent stale (last_seen=${last_seen}, ${delta}s ago, window=${ASSERT_DAEMON_RECENCY_S}s)" >&2
  return 1
}
