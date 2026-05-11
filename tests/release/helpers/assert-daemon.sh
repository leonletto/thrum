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
# inbox view). The guard's cross-worktree check is set to "warn" mode by
# ephemeral_daemon_start so calls succeed; we still need to:
#   1. cd to FIXTURE_REPO so identity-file lookup finds the right .thrum/
#   2. unset THRUM_HOME so it doesn't override cwd back to the harness
#      worktree (the caller is typically running inside a Claude session
#      where THRUM_HOME points at the impl_behavioral worktree).
_thrum_as() {
  local agent="$1"; shift
  ( cd "${FIXTURE_REPO:-.}" \
    && env -u THRUM_HOME -u THRUM_AGENT_ID -u THRUM_INTENT THRUM_NAME="$agent" \
       thrum --repo "${FIXTURE_REPO:-.}" "$@" )
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

# A reply from R to original-sender O lives in O's inbox (NOT R's).
# Caller must pass the original sender so we can scan the right inbox.
# Signature: assert_daemon_agent_replied_to <replier> <replied_to_msg_id> <original_sender>
assert_daemon_agent_replied_to() {
  local replier="$1" replied_to="$2" original_sender="${3:-}"
  if [[ -z "$original_sender" ]]; then
    echo "assert-daemon.agent_replied_to: original_sender argument required (replies sit in the original sender's inbox, not the replier's)" >&2
    return 2
  fi
  local match
  match=$(_thrum_as "$original_sender" inbox --json --all 2>/dev/null \
    | jq -r --arg r "$replier" --arg m "$replied_to" \
        '(.messages // .) | .[]? | select(.agent_id==$r and .reply_to==$m) | .message_id' \
    || true)
  if [[ -n "$match" ]]; then return 0; fi
  echo "assert-daemon.agent_replied_to: no reply from=$replier to_msg=$replied_to in inbox of=$original_sender" >&2
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
  # Portable epoch parse: prefer python3 (one path, GNU+BSD), fall back to
  # date variants. Returns 0 on unparseable, which makes the agent appear
  # stale rather than spuriously fresh.
  local last_epoch now_epoch delta
  last_epoch=$(python3 -c "import sys, datetime; s=sys.argv[1].split('.')[0].rstrip('Z'); print(int(datetime.datetime.fromisoformat(s).replace(tzinfo=datetime.timezone.utc).timestamp()))" "$last_seen" 2>/dev/null || \
               date -j -f '%Y-%m-%dT%H:%M:%S' "${last_seen%%.*}" +%s 2>/dev/null || \
               date -d "$last_seen" +%s 2>/dev/null || echo 0)
  now_epoch=$(date +%s)
  delta=$((now_epoch - last_epoch))
  if (( delta <= ASSERT_DAEMON_RECENCY_S )); then return 0; fi
  echo "assert-daemon.agent_session_active: $agent stale (last_seen=${last_seen}, ${delta}s ago, window=${ASSERT_DAEMON_RECENCY_S}s)" >&2
  return 1
}
