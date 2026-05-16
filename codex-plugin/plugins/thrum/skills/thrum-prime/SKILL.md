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
