---
name: thrum-prime
description: Load AI-optimized session context
# source: claude-plugin/commands/prime.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Prime

Use this skill when the user explicitly wants the `prime` Thrum workflow. Prefer
the umbrella `thrum` skill when the request spans multiple commands or needs
broader coordination judgment.

Run `thrum prime` to load your complete session briefing — identity, daemon
health, role instructions, project state, and session context (plus messaging
protocol if multi-agent mode).

```bash
thrum prime
```

Use `thrum prime --json` for structured output.

## First turn: emit a one-line ack

After loading the briefing, your very first action of this turn — before running
any tools or further reading — is to print one plain-text line in this format:

```text
@<agent-name> primed (<role>/<module>). <one-sentence intent>. Standing by.
```

Substitute the fields from your own identity (visible at the top of the
briefing) and write `<intent>` as a brief sentence drawn from your inbox or
restart snapshot. Drop the `/<module>` segment when no module is set or it
matches the role.

This produces visible scrollback so humans can distinguish a healthy launch from
a stuck or failed one without probing the daemon.
