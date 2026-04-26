#!/usr/bin/env bash
# tests/release/helpers/assert.sh — driver-side result waiter.
# Sees a `!`-prefix command's <bash-stdout> entry land in the agent's JSONL
# and emits PASS / FAIL via output.sh.
#
# Depends on: paths.sh, drive.sh, output.sh (sourced via helpers/all.sh).

# assert_jsonl <pane> <repo> <scenario-id> <assertion-name> <expected-line-prefix> [src-line]
#
#   pane                — for error reporting only ("coord" or "impl")
#   repo                — the agent's repo path (used to find its JSONL)
#   scenario-id         — e.g. "01-session-start-injection"
#   assertion-name      — e.g. "briefing-header"
#   expected-line-prefix — what we expect to see inside <bash-stdout>...</bash-stdout>;
#                         most commonly "VERIFIED <tag>" or "FAILED <tag>" or "ERROR <tag>"
#   src-line            — optional "scenarios/01-foo.test.sh:42" for failure attribution
assert_jsonl() {
  local pane="$1" repo="$2" sid="$3" name="$4" expected="$5" loc="${6:-unknown}"

  # Grep for the next <bash-stdout> entry containing the expected prefix.
  # The literal text marker is stable per spec § 4 "Empirical findings".
  local filter
  filter=".type == \"user\" and (.message.content | type == \"string\") and (.message.content | startswith(\"<bash-stdout>${expected}\"))"

  local match
  if match=$(wait_for_jsonl_match "$repo" "$filter" 30); then
    emit_pass "$sid" "$name"
    return 0
  fi

  # Failure path: capture the most recent <bash-stdout> in the repo's JSONL
  # to put in the "got:" line, so the operator sees what actually arrived
  # instead of a generic timeout.
  local jsonl
  if jsonl=$(jsonl_for_repo "$repo"); then
    local got
    got=$(jq -r 'select(.type=="user" and (.message.content | type == "string") and (.message.content | startswith("<bash-stdout>"))) | .message.content' "$jsonl" \
      | tail -n1 \
      | sed 's|^<bash-stdout>||; s|</bash-stdout>.*||')
    emit_fail "$sid" "$name" "bash-stdout starting with '${expected}'" "${got:-<no <bash-stdout> entry seen yet>}" "$loc"
  else
    emit_fail "$sid" "$name" "bash-stdout starting with '${expected}'" "(no JSONL found at all for $pane)" "$loc"
  fi
  return 1
}
