---
title: "Cursor Plugin"
description: "Install and use the Thrum Cursor plugin — commands, focused skills, safety rules, and Cursor-native packaging."
category: "guides"
order: 5
tags: ["cursor", "plugin", "skills", "commands", "agent-coordination"]
last_updated: "2026-03-31"
---

## Cursor Plugin

> See also: [Quickstart Guide](quickstart.md) for basic Thrum setup,
> [Claude Code Plugin](claude-code-plugin.md) for the Claude marketplace plugin,
> [Codex Plugin](codex-plugin.md) for the Codex skill bundle,
> [Agent Configurations](agent-configs.md) for manual agent definitions,
> [MCP Server](mcp-server.md) for native MCP transport,
> [Multi-Agent Support](multi-agent.md) for coordination patterns.

## Overview

The Thrum **Cursor plugin** is a Cursor-native package under `cursor-plugin/` in
the Thrum repository. It bundles **slash-style commands**, **focused skills**
(`thrum-core`, `thrum-ops`, `thrum-role-config`, `project-setup`), **project
rules** (`thrum-session`, `thrum-safety`), a **message-listener agent** template,
and a minimal **hooks** configuration so agents can use the same coordination
workflows as elsewhere in Thrum.

**Current status:** The tree contains scaffolded and ported assets (manifest,
commands, skills, rules, agents, and `SessionStart` hook wiring). The package is
**not** yet published to a Cursor marketplace flow comparable to
`claude plugin install`, and **hook parity with Claude Code is incomplete** (see
[Hook Limitations](#hook-limitations)). Treat local installation as **developer
preview**: verify behavior in your Cursor version before relying on it in
critical workflows.

For the lightest-weight Cursor path today, `thrum init --skills --runtime cursor`
still installs the standalone Thrum skill under `.cursor/skills/thrum/`. The
plugin is the fuller, repo-packaged alternative once you symlink or copy it into
Cursor’s local plugins directory (see [Installation](#installation)).

## Prerequisites

- **Thrum CLI** installed and on `PATH` (see [Quickstart](quickstart.md))
- **`thrum init`** run in the repository you open in Cursor (daemon, identity,
  sync worktree)
- **Cursor** with support for local plugin folders (see Cursor docs for your
  version)

Verify the daemon before relying on messaging:

```bash
thrum daemon status
```

## Installation

There is **no** documented one-command marketplace install for this package yet
comparable to Claude’s `claude plugin marketplace add` flow. Use a **local
development install** from a clone of Thrum:

1. Clone or update the Thrum repository (branch that contains `cursor-plugin/`
   with the manifest you want).
2. Copy or symlink the plugin directory into Cursor’s local plugins location,
   for example:

   ```bash
   mkdir -p ~/.cursor/plugins/local
   ln -sf /path/to/thrum/cursor-plugin ~/.cursor/plugins/local/thrum
   ```

   Alternatively, copy the folder instead of symlinking if you prefer an
   isolated snapshot.

3. **Restart Cursor** so it reloads plugin metadata (`plugin.json`), commands,
   skills, rules, and hooks.

The in-repo [cursor-plugin README](https://github.com/leonletto/thrum/blob/main/cursor-plugin/README.md) reiterates
that the package was historically described as scaffold-in-progress; the tree
now includes real commands and skills, but **install and marketplace readiness
are still evolving**—validate in your environment after each update.

### Verify installation

- Open the project in Cursor and confirm Thrum commands appear in the command
  palette or slash UI (exact surfacing depends on Cursor version).
- Start a new chat or agent session and confirm identity or inbox context when
  hooks run (if your Cursor build invokes `SessionStart` hooks for that session
  type).

## Included Components

Paths are relative to `cursor-plugin/` in the Thrum repository.

| Area | Contents |
| ---- | -------- |
| **Manifest** | `.cursor-plugin/plugin.json` — name `thrum`, version aligned with the repo release line, pointers to `commands`, `skills`, `rules`, `agents`, and `hooks/hooks.json`. |
| **Commands** | `commands/*.md` — operational entry points such as quickstart, send, inbox, reply, prime, overview, group, team, wait, load-context, update-context (Cursor-native command docs). |
| **Skills** | `skills/thrum-core`, `skills/thrum-ops`, `skills/thrum-role-config`, `skills/project-setup` — same conceptual split as Codex: core coordination, CLI operations, role templates, plan → beads workflow. |
| **Rules** | `rules/thrum-session.mdc`, `rules/thrum-safety.mdc` — session and safety guidance for the agent. |
| **Agents** | `agents/message-listener.md` — template for background listening patterns. |
| **Hooks** | `hooks/hooks.json` — see [Hook Limitations](#hook-limitations). |
| **Scripts** | `scripts/` — helper shell scripts (e.g. sync from Claude sources, guardrails, pre-compact helpers); some are intended for parity or future hook wiring, not all are active in every Cursor session type. |

Skill reference markdown under `skills/*/references/` is populated from the
Claude plugin resources and command docs (via the sync script below), so
wording may still mention Claude-specific paths in places; prefer the Cursor
plugin layout when following file paths in Cursor.

## Hook Limitations

- **What is wired today:** `hooks/hooks.json` defines a **SessionStart** hook
  that runs `thrum prime` (with a fallback message if Thrum is not initialized).
- **Claude Code parity:** The Claude plugin also uses a **PreCompact** hook to
  re-inject context before compaction. That event **is not** mirrored in the
  current Cursor `hooks.json`; behavior depends on whether Cursor exposes an
  equivalent lifecycle hook in your product version.
- **Semantic differences:** Cursor’s hook and session model may not run the
  same triggers as Claude Code (e.g. per-chat vs per-workspace). Do not assume
  identical timing or stderr handling.
- **MCP:** Hook behavior is separate from [MCP Server](mcp-server.md)
  configuration. You can still run `thrum mcp serve` and wire it in Cursor MCP
  settings if your workflow needs tool-based messaging.

If automatic prime does not run, use the **prime** command from the plugin (or
run `thrum prime` in the terminal) at session start.

## Development Sync Workflow

When you maintain Thrum’s Claude plugin as the source of truth for shared
markdown, refresh Cursor skill references and Cursor-specific substitutions from
the repo root:

```bash
./cursor-plugin/scripts/sync-from-claude.sh
```

The script copies Claude skill resources and command files into
`cursor-plugin/skills/*/references/` and applies targeted text replacements for
Cursor-oriented instructions (for example worktree and settings paths).

After syncing, restart Cursor or reload the plugin as needed, and run your usual
tests or manual smoke checks. Optional: `cursor-plugin/scripts/assert-plugin-versions.sh`
can help catch version drift when invoked as part of your workflow.

Contributors should commit updated reference files when the Claude-side sources
change, so the Cursor package stays aligned.

## Troubleshooting

### “Thrum not initialized” or empty prime output

Run `thrum init` in the repository root and ensure you opened that root (or a
configured worktree with redirect) in Cursor.

### Commands or skills do not appear

- Confirm the plugin path is under `~/.cursor/plugins/local/` (or the path your
  Cursor build documents).
- Fully quit and restart Cursor after copying or symlinking.
- Check that `.cursor-plugin/plugin.json` is present and valid JSON.

### Hooks never seem to run

Cursor may not invoke hooks for all session types, or your version may differ.
Rely on manual `thrum prime` or the plugin’s prime command until you confirm
hook support.

### Messaging or sync errors

Use `thrum daemon status`, `thrum status`, and `thrum sync status` as in
[Quickstart troubleshooting](quickstart.md#troubleshooting). Plugin packaging
does not replace a healthy daemon or Git sync setup.

### Stale documentation inside reference files

References are synced from Claude plugin sources; if you see `.claude/` paths in
a reference file, check whether `sync-from-claude.sh` replacements cover that
section or file an issue/upstream fix in the Thrum repo.

## When To Use Cursor vs Claude vs Codex

| You are using… | Prefer… | Why |
| -------------- | ------- | --- |
| **Cursor** as the primary IDE agent | **Cursor plugin** (local install) or **`thrum init --skills --runtime cursor`** | Native Cursor commands, rules, and skills; plugin is the fuller packaged layout when symlinked locally. |
| **Claude Code** with marketplace support | **[Claude Code Plugin](claude-code-plugin.md)** | Official self-hosted marketplace install, SessionStart + PreCompact hooks, mature slash-command set. |
| **OpenAI Codex** (CLI / skills dir) | **[Codex Plugin](codex-plugin.md)** | Skill bundle under `~/.codex/skills`; no Cursor or Claude plugin runtime. |
| **Multiple products** | **Thrum CLI + MCP** everywhere, product-specific bundles where they add UX | Same Git-backed store; plugins bundle ergonomics, not a different protocol. |

**Honest scope notes:**

- **Marketplace / one-click install:** Claude plugin is ahead; Cursor plugin is
  local-from-repo today.
- **Hooks:** Claude plugin’s PreCompact story is stronger on paper; Cursor
  plugin currently emphasizes SessionStart only in `hooks.json`.
- **Skills:** Codex and Cursor both use split skills; Claude leans on slash
  commands plus resource docs inside its plugin.

## Next Steps

- [Claude Code Plugin](claude-code-plugin.md) — marketplace install and full hook
  story for Claude Code
- [Codex Plugin](codex-plugin.md) — skill-only install for Codex
- [Agent Coordination](agent-coordination.md) — workflows that apply across
  tools
- [MCP Server](mcp-server.md) — optional native tools alongside any plugin
