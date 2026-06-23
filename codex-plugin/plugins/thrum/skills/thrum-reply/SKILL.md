---
name: thrum-reply
description: Reply to a message
# source: claude-plugin/commands/reply.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Reply

Use this skill when the user explicitly wants the `reply` Thrum workflow. Prefer
the umbrella `thrum` skill when the request spans multiple commands or needs
broader coordination judgment.

Reply to a message with the same audience as the original.

If arguments are provided, use them. Otherwise ask for the message ID and reply
content.

```bash
thrum reply <msg-id> "response text"
```

The reply inherits the original message's audience (direct or group).

If the body has backticks, `$(...)`, `$VAR`, or quotes, pass it via a quoted
heredoc so the shell doesn't corrupt it (thrum-d3fp):

```bash
thrum reply <msg-id> --stdin <<'EOF'
Done — see `internal/foo.go`. Cost was $(estimate).
EOF
```

`--body-file <path>` reads from a file; the response argument `-` is a stdin
alias.
