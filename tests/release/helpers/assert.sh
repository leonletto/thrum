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

  # Match the expected prefix at the start of EITHER the bash-stdout region
  # or the bash-stderr region of a `!`-prefix bash command's JSONL entry.
  # check-context-value.sh writes VERIFIED to stdout (exit 0) and FAILED /
  # ERROR to stderr (exit 1/2) — the spec uses both shapes as positive
  # signals, so we have to look in both regions. Content layout:
  #   stdout-only:  <bash-stdout>VERIFIED…</bash-stdout><bash-stderr></bash-stderr>
  #   stderr-only:  <bash-stdout></bash-stdout><bash-stderr>FAILED…</bash-stderr>
  local stdout_prefix="<bash-stdout>${expected}"
  local stderr_prefix="<bash-stdout></bash-stdout><bash-stderr>${expected}"
  local filter
  filter=".type == \"user\" and (.message.content | type == \"string\") and (.message.content | (startswith(\"${stdout_prefix}\") or startswith(\"${stderr_prefix}\")))"

  # 60s (not 30s) ceiling: under claude 2.1.x full-gate render load the
  # `!`-bash command's <bash-stdout> entry can land later than 30s after the
  # send even though the command DID execute (observed: scenario 03 had all
  # three VERIFIED lines rendered in the fail snapshot, yet the middle 30s
  # assert had already timed out). 60s matches the coord-pane idle ceiling
  # the send-side scenarios already use, absorbing the slow-land without
  # adding a resend.
  local match
  if match=$(wait_for_jsonl_match "$repo" "$filter" 60); then
    emit_pass "$sid" "$name"
    return 0
  fi

  # Failure path: extract the LAST `!`-bash entry's content (both stdout and
  # stderr regions) so the operator can see what actually landed. Newlines
  # collapsed to spaces for one-line presentation. Guard the glob: if the
  # project dir doesn't exist yet OR contains no JSONL files, the unquoted
  # glob would expand to the literal `*.jsonl` and jq would fail silently,
  # masking the real "no transcript" condition behind a generic empty `got`.
  local project_dir got
  project_dir="$HOME/.claude/projects/$(encode_cwd "$repo")"
  got=""
  if [ -d "$project_dir" ] && compgen -G "$project_dir/*.jsonl" >/dev/null; then
    got=$(jq -r 'select(.type=="user" and (.message.content | type == "string") and (.message.content | startswith("<bash-stdout>"))) | .message.content' \
      "$project_dir"/*.jsonl 2>/dev/null \
      | tail -n1 | tr '\n' ' ')
  fi
  emit_fail "$sid" "$name" "${expected} (in <bash-stdout> or <bash-stderr>)" \
    "${got:-<no bash entry seen yet>}" "$loc"
  return 1
}
