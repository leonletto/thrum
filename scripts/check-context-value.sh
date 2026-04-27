#!/usr/bin/env bash
# scripts/check-context-value.sh
#
# Verify that a needle string appears in a SessionStart hook attachment of the
# current Claude Code session's JSONL transcript. Used by the manual release
# test plan to assert that hook-injected additionalContext landed correctly,
# without having to ask the agent to introspect its own context.
#
# Usage:
#   check-context-value.sh <test_tag> <needle> [hook_name]
#
# Arguments:
#   test_tag   Arbitrary identifier echoed back in the result line so a single
#              tmux capture can disambiguate multiple checks (test_1, test_2,…).
#   needle     Literal string to grep for (passed to `grep -F`; emoji + special
#              chars are fine, no regex). Wrap in quotes.
#   hook_name  Optional. Filter to a specific hookName attachment
#              (e.g. "SessionStart:startup"). Default: any SessionStart hook.
#
# Output (single line, easy to grep from `tmux capture-pane`):
#   VERIFIED <test_tag> (<n> hits in <hook_name_label>)
#   FAILED   <test_tag> (0 hits in <hook_name_label>)
#   ERROR    <test_tag> (<reason>)
#
# Exit code: 0 on VERIFIED, 1 on FAILED, 2 on ERROR.
#
# Inserted into the test plan as e.g.:
#   ! ${THRUM_HOME}/scripts/check-context-value.sh test_1 "🛑 ACTION REQUIRED" SessionStart:startup
# The `!` prefix runs the command via Claude Code's bash sub-shell so its
# single-line stdout lands in the pane and is captureable by tmux.

set -uo pipefail

TAG="${1:-}"
NEEDLE="${2:-}"
HOOK_FILTER="${3:-}"

if [ -z "$TAG" ] || [ -z "$NEEDLE" ]; then
  echo "ERROR ${TAG:-?} (usage: $0 <test_tag> <needle> [hook_name])"
  exit 2
fi

HOOK_LABEL="${HOOK_FILTER:-any SessionStart}"

if ! command -v jq >/dev/null 2>&1; then
  echo "ERROR $TAG (jq not installed)"
  exit 2
fi

# Encode cwd using Claude Code's convention: leading "/" stripped, then "/",
# ".", and "_" all replaced with "-", then a leading "-" prepended.
#
# NOTE: Adds the "_" → "-" substitution missing from the Go reference at
# internal/restart/restart.go:encodeCwd, which only handles "/" and ".".
# Real Claude Code behavior also collapses underscores; without this, paths
# containing "_" (e.g. ~/.thrum_release_tests/$RUNID) resolve to the wrong
# project dir and check-context-value.sh fails with "no project dir at...".
encode_cwd() {
  local cwd="$1"
  cwd="${cwd#/}"
  cwd="${cwd//\//-}"
  cwd="${cwd//./-}"
  cwd="${cwd//_/-}"
  printf '%s' "-${cwd}"
}

PROJECT_DIR="${HOME}/.claude/projects/$(encode_cwd "$PWD")"

if [ ! -d "$PROJECT_DIR" ]; then
  echo "ERROR $TAG (no project dir at $PROJECT_DIR)"
  exit 2
fi

# Pick the most-recently-modified .jsonl in the project dir (mirrors
# FindLatestJSONLForCwd). Retry up to ~3s in case the runtime hasn't
# flushed the SessionStart attachment to disk yet.
JSONL=""
for _ in 1 2 3; do
  JSONL=$(ls -t "$PROJECT_DIR"/*.jsonl 2>/dev/null | head -n1 || true)
  if [ -n "$JSONL" ] && [ -s "$JSONL" ]; then
    break
  fi
  sleep 1
done

if [ -z "$JSONL" ] || [ ! -s "$JSONL" ]; then
  echo "ERROR $TAG (no JSONL transcript found under $PROJECT_DIR)"
  exit 2
fi

# Build jq filter. SessionStart attachments have:
#   .type == "attachment"
#   .attachment.hookEvent == "SessionStart"
#   .attachment.hookName matches HOOK_FILTER (or any if filter empty)
#   .attachment.content is a string (or occasionally an array of strings for
#     skill-injected SessionStart entries — handle both shapes).
JQ_HOOK_PREDICATE='.type=="attachment" and .attachment.hookEvent=="SessionStart"'
if [ -n "$HOOK_FILTER" ]; then
  JQ_HOOK_PREDICATE="${JQ_HOOK_PREDICATE} and .attachment.hookName==\"${HOOK_FILTER}\""
fi

# Concatenate all matching content into a single blob, then grep.
BLOB=$(jq -r "select(${JQ_HOOK_PREDICATE}) | .attachment.content | if type==\"array\" then join(\"\n\") else . end" "$JSONL" 2>/dev/null || true)

if [ -z "$BLOB" ]; then
  echo "FAILED $TAG (no matching SessionStart attachments in ${HOOK_LABEL})"
  exit 1
fi

# -F: literal/fixed-string. -c: count. Use printf so embedded backslashes in
# NEEDLE aren't interpreted by echo.
HITS=$(printf '%s' "$BLOB" | grep -cF -- "$NEEDLE" 2>/dev/null || true)
HITS="${HITS:-0}"

if [ "$HITS" -gt 0 ]; then
  echo "VERIFIED $TAG ($HITS hits in $HOOK_LABEL)"
  exit 0
else
  echo "FAILED $TAG (0 hits in $HOOK_LABEL)"
  exit 1
fi
