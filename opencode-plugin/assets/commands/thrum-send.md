---
description: Send a message to an agent
---

Send a direct message or broadcast.

If arguments are provided, use them. Otherwise ask for recipient and message
content.

```bash
thrum send "message" --to @agent_name             # Direct message
thrum send "message" --to @everyone               # Broadcast to all agents
```

Unknown recipients are a hard error. Use `thrum team` to verify agent names
before sending.

If the body has backticks, `$(...)`, `$VAR`, or quotes, pass it via a quoted
heredoc so the shell doesn't corrupt it (thrum-d3fp):

```bash
thrum send --to @agent_name --stdin <<'EOF'
Run `make build`, then check $(git rev-parse HEAD).
EOF
```

`--body-file <path>` reads from a file; the body argument `-` is a stdin alias.
