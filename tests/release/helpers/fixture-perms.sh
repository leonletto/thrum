#!/usr/bin/env bash
# tests/release/helpers/fixture-perms.sh — fixture per-tool permissions writer.
#
# write_fixture_perms <fixture-worktree>
# Writes .claude/settings.local.json into the given fixture worktree with a
# broad Bash tool allowlist. Required for fixture claudes that AUTONOMOUSLY
# invoke the Bash tool (run.sh NL->tool_use scenarios 56/58/59/60/61 and
# behavioral card 01: thrum send/reply/who-has/agent set-intent/team +
# worktree create + tmux launch) — without a pre-grant, each call stalls on a
# per-tool permission prompt.
#
# `!`-bash-prefix probes (whoami etc., e.g. scenario 29) run bash DIRECTLY and
# BYPASS claude's tool-permission system, so they don't need this file; the
# allowlist is required only where the fixture claude autonomously uses the
# Bash tool.
#
# NEVER use defaultMode=bypassPermissions: empirically it triggers a SECOND
# blocking modal ("Bypass Permissions mode … 1. No, exit / 2. Yes, I accept")
# whose default is "No, exit" — a blind Enter KILLS claude. The allowlist
# pre-grants without that modal.
#
# Idempotent: overwrites unconditionally. Scope: ephemeral test fixtures only
# (under /tmp or ~/.thrum_release_tests).
write_fixture_perms() {
  local wt="$1"
  if [ -z "$wt" ]; then
    echo "write_fixture_perms: missing fixture-worktree argument" >&2
    return 1
  fi
  mkdir -p "$wt/.claude"
  cat > "$wt/.claude/settings.local.json" <<'JSON'
{
  "permissions": {
    "allow": [
      "Bash"
    ]
  }
}
JSON
}
