<!-- BEGIN THRUM -->

## Thrum — Agent Messaging

This repo uses [Thrum](https://leonletto.github.io/thrum/) for persistent
cross-session, cross-agent messaging. The Thrum daemon delivers messages between
agents coordinating on shared work and preserves conversation state across
restarts.

### Essential commands

- `thrum whoami` — Your current agent identity and session
- `thrum team` — List active agents in the team
- `thrum inbox --unread` — Check unread messages addressed to you
- `thrum send "message" --to @agent_name` — Send a directed message
- `thrum reply <msg_id> "response"` — Reply to a message in-thread

### First time here?

If you don't have a Thrum identity registered for this repo yet:

```bash
thrum quickstart --name <your-name> --role <role> --module <module>
```

### Full reference

- Run `thrum --help` for all commands
- Docs: <https://leonletto.github.io/thrum/docs.html>

### Using Thrum with the Claude Code plugin

If you're using Claude Code with the Thrum plugin installed, prefer that — the
plugin provides skills and hooks that handle messaging automatically and keep
this CLAUDE.md minimal. These instructions are the portable alternative for
environments without the plugin.

<!-- END THRUM -->
