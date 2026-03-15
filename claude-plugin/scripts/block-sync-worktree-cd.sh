#!/bin/bash
# PreToolUse hook: protect the a-sync JSONL worktree from destruction.
#
# .git/thrum-sync/a-sync/ is a detached git worktree living INSIDE .git/.
# It holds the append-only JSONL event log for thrum message sync.
#
# DANGER: If you check out a different branch in that worktree, git replaces
# its contents — but since it lives inside .git/, this DESTROYS the git object
# store, refs, and config. The entire repo and ALL worktrees are wiped.
#
# This happened once and required significant effort to recover.
#
# Blocked:
#   1. cd/pushd into the a-sync worktree (prevents arbitrary commands there)
#   2. git -C <a-sync-path> with branch-changing commands (checkout, switch,
#      reset, merge, rebase, pull) — these change HEAD without cd
#   3. git --work-tree=<a-sync-path> with branch-changing commands
#
# Allowed:
#   git -C <a-sync-path> add/commit/push/status/rm/log/diff — safe operations
#   ls/cat/grep/rm on absolute paths — no CWD change
set -euo pipefail

input=$(cat)

tool_name=$(echo "$input" | jq -r '.tool_name // empty')
[ "$tool_name" = "Bash" ] || exit 0

command=$(echo "$input" | jq -r '.tool_input.command // empty')
[ -n "$command" ] || exit 0

SYNC_PATTERN='\.git/thrum-sync/a-sync'

deny() {
  cat >&2 <<EOF
{
  "hookSpecificOutput": {
    "permissionDecision": "deny"
  },
  "systemMessage": "BLOCKED: $1 The .git/thrum-sync/a-sync/ worktree lives INSIDE .git/ — checking out a different branch there DESTROYS the entire git object store, wiping the repo and all worktrees. Use absolute paths with safe operations (ls, grep, rm, git -C ... add/commit/push) instead."
}
EOF
  exit 2
}

# 1. Block cd/pushd/chdir into the a-sync worktree
if echo "$command" | grep -qE "(^|\s|;|&&|\|\|)(cd|pushd|chdir)\s+['\"]?[^\s]*${SYNC_PATTERN}"; then
  deny "Changing directory into .git/thrum-sync/a-sync/ is forbidden."
fi

# 2. Block git -C <a-sync-path> with branch-changing commands
if echo "$command" | grep -qE "git\s+(-C\s+['\"]?[^\s]*${SYNC_PATTERN}['\"]?\s+)(checkout|switch|reset|merge|rebase|pull)\b"; then
  deny "Branch-changing git operations on the a-sync worktree are forbidden."
fi

# 3. Block git --work-tree=<a-sync-path> with branch-changing commands
if echo "$command" | grep -qE "git\s+(--work-tree[= ]['\"]?[^\s]*${SYNC_PATTERN}['\"]?\s+).*(checkout|switch|reset|merge|rebase|pull)\b"; then
  deny "Branch-changing git operations on the a-sync worktree are forbidden."
fi

# 4. Block git --git-dir=<a-sync-path> with branch-changing commands
if echo "$command" | grep -qE "git\s+(--git-dir[= ]['\"]?[^\s]*${SYNC_PATTERN}['\"]?\s+).*(checkout|switch|reset|merge|rebase|pull)\b"; then
  deny "Branch-changing git operations on the a-sync worktree are forbidden."
fi
