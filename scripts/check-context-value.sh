#!/usr/bin/env bash
# scripts/check-context-value.sh
#
# Verify that a needle string appears in a SessionStart hook attachment of the
# current Claude Code session's JSONL transcript. Used by the manual release
# test plan to assert that hook-injected additionalContext landed correctly,
# without having to ask the agent to introspect its own context.
#
# Usage:
#   check-context-value.sh [--source=any] <test_tag> <needle> [hook_name]
#
# Arguments:
#   --source=any  Optional flag. Skip the cwd→project-dir encoding and scan
#                 every JSONL under ~/.claude/projects/*/. Use when the
#                 caller's cwd does not encode to a thrum project directory
#                 (e.g. /tmp panes in fallback scenarios). UNSAFE for
#                 negative assertions during full release-test runs because
#                 the run-level coord/impl panes also produce briefing-bearing
#                 SessionStart attachments — prefer unique sub-case cwds with
#                 default cwd-mode for negative assertions.
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

SOURCE_ANY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --source=any) SOURCE_ANY=1; shift ;;
    --source=*)
      echo "ERROR ? (unknown --source value: ${1#--source=}; only 'any' is supported)"
      exit 2
      ;;
    --) shift; break ;;
    -*)
      echo "ERROR ? (unknown flag: $1)"
      exit 2
      ;;
    *) break ;;
  esac
done

TAG="${1:-}"
NEEDLE="${2:-}"
HOOK_FILTER="${3:-}"

if [ -z "$TAG" ] || [ -z "$NEEDLE" ]; then
  echo "ERROR ${TAG:-?} (usage: $0 [--source=any] <test_tag> <needle> [hook_name])"
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
# Mirrors internal/restart/restart.go:encodeCwd. Both must stay in sync —
# they're independent implementations of one Claude-Code-side convention,
# applied at the same three characters ("/", ".", "_").
encode_cwd() {
  local cwd="$1"
  cwd="${cwd#/}"
  cwd="${cwd//\//-}"
  cwd="${cwd//./-}"
  cwd="${cwd//_/-}"
  printf '%s' "-${cwd}"
}

if [ "$SOURCE_ANY" = "1" ]; then
  PROJECT_DIR_LABEL="<all projects>"
  PROJECTS_ROOT="${HOME}/.claude/projects"
else
  PROJECT_DIR="${HOME}/.claude/projects/$(encode_cwd "$PWD")"
  PROJECT_DIR_LABEL="$PROJECT_DIR"
  if [ ! -d "$PROJECT_DIR" ]; then
    echo "ERROR $TAG (no project dir at $PROJECT_DIR)"
    exit 2
  fi
fi

# Collect all .jsonl transcripts in the project dir. Claude Code writes
# multiple JSONL files per session (a main `<uuid>.jsonl` plus
# `agent-<short>.jsonl` files for sub-agents spawned by SessionStart hooks
# and the like). The needle could be in the main one, but a sub-agent
# JSONL may briefly be the newest by mtime — so we scan all of them and
# concatenate matching SessionStart attachments. Retry up to ~3s in case
# the runtime hasn't flushed the SessionStart attachment to disk yet.
JSONL_FILES=()
for _ in 1 2 3; do
  if [ "$SOURCE_ANY" = "1" ]; then
    mapfile -t JSONL_FILES < <(ls -1 "$PROJECTS_ROOT"/*/*.jsonl 2>/dev/null || true)
  else
    mapfile -t JSONL_FILES < <(ls -1 "$PROJECT_DIR"/*.jsonl 2>/dev/null || true)
  fi
  if [ "${#JSONL_FILES[@]}" -gt 0 ]; then
    break
  fi
  sleep 1
done

if [ "${#JSONL_FILES[@]}" -eq 0 ]; then
  echo "ERROR $TAG (no JSONL transcript found under $PROJECT_DIR_LABEL)"
  exit 2
fi

# Build jq filter. SessionStart attachments have:
#   .type == "attachment"
#   .attachment.hookEvent == "SessionStart"
#   .attachment.hookName matches HOOK_FILTER (or any if filter empty)
#   .attachment.content is a string (or occasionally an array of strings for
#     skill-injected SessionStart entries — handle both shapes).
#
# HOOK_FILTER is passed via `jq --arg hookName` (NOT interpolated into the
# filter string) so a hook name containing `"` or `\\` can't corrupt the
# expression. The shape of the predicate differs whether the filter is set
# or unset.
JQ_HOOK_PREDICATE_BASE='.type=="attachment" and .attachment.hookEvent=="SessionStart"'
if [ -n "$HOOK_FILTER" ]; then
  JQ_HOOK_PREDICATE="${JQ_HOOK_PREDICATE_BASE} and .attachment.hookName==\$hookName"
else
  JQ_HOOK_PREDICATE="$JQ_HOOK_PREDICATE_BASE"
fi

# Concatenate all matching SessionStart attachment content across every
# JSONL in the project dir into a single blob, then grep. The blob also
# pulls .attachment.stdout — Claude Code's SessionStart attachment shape
# is dual: some tool versions populate .content, others populate .stdout
# (with .content empty). Reading both is required for cross-version
# robustness.
BLOB=""
for f in "${JSONL_FILES[@]}"; do
  [ -s "$f" ] || continue
  CHUNK=$(jq -r --arg hookName "$HOOK_FILTER" \
    "select(${JQ_HOOK_PREDICATE}) | (.attachment.content // \"\" | if type==\"array\" then join(\"\n\") else . end), (.attachment.stdout // \"\")" \
    "$f" 2>/dev/null || true)
  if [ -n "$CHUNK" ]; then
    BLOB="${BLOB}${CHUNK}"$'\n'
  fi
done

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
