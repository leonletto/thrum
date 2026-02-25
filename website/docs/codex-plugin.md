---
title: "Codex Plugin"
description:
  "Install and use Thrum's Codex skill bundle — split skills for coordination,
  operations, role template generation, and project setup"
category: "guides"
order: 4
tags: ["codex", "skills", "thrum", "installation", "agent-coordination"]
last_updated: "2026-02-24"
---

## Codex Plugin

> See also: [Quickstart Guide](quickstart.md) for base Thrum setup,
> [Claude Code Plugin](claude-code-plugin.md) for Claude-specific plugin flows,
> [Agent Coordination](agent-coordination.md) for coordination patterns,
> [Role Templates](role-templates.md) for role preamble details.

## Overview

Codex does not currently use the Claude-style plugin marketplace model.
Instead, Thrum integration is packaged as a **Codex skill bundle** in
`codex-plugin/skills`.

The bundle is split into focused skills:

- `thrum-core` — durable messaging, groups, identity, worktree boundaries
- `thrum-ops` — quickstart/inbox/wait/context/daemon/sync operational flows
- `thrum-role-config` — generate or update `.thrum/role_templates`
- `project-setup` — turn design docs into Beads epics/tasks before coding

## Prerequisites

- Thrum installed and available on `PATH`
- Thrum initialized in the repo (`thrum init`)
- Codex installed with local skills directory (`~/.codex/skills`)

If you have not initialized Thrum yet:

```bash
thrum init
thrum daemon start
thrum quickstart --name myagent --role implementer --module auth
```

## Installation

From the Thrum repository root:

```bash
./codex-plugin/scripts/install-skills.sh
```

This installs the bundled skills into `~/.codex/skills` (or
`$CODEX_HOME/skills` if `CODEX_HOME` is set).

### Verify installed skills

```bash
find "${CODEX_HOME:-$HOME/.codex}/skills" -maxdepth 1 -type d -name "thrum-*" -o -name "project-setup"
```

### Restart Codex

Restart Codex after installation so the new skills are loaded.

## Updating Skills During Development

After editing files under `codex-plugin/skills/`, sync updates into your local
Codex installation:

```bash
./codex-plugin/scripts/sync-skills.sh
```

`sync-skills.sh` replaces installed copies with your local versions.

## Usage Patterns

### Multi-agent coordination

Ask Codex to coordinate work across agents using Thrum. The `thrum-core` skill
handles audience routing (`@agent`, `@group`, `@everyone`) and durable
message/reply workflows.

### Session operations and triage

When you need to bootstrap sessions, triage unread messages, or verify daemon
health, `thrum-ops` provides operational workflows and command references.

### Role template generation

When team structure changes, use `thrum-role-config` to detect environment
context and regenerate `.thrum/role_templates/<role>.md` with clear autonomy and
scope boundaries.

## Manual Installation (Optional)

If you prefer manual copy instead of scripts:

```bash
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
cp -R codex-plugin/skills/thrum-core "${CODEX_HOME:-$HOME/.codex}/skills/"
cp -R codex-plugin/skills/thrum-ops "${CODEX_HOME:-$HOME/.codex}/skills/"
cp -R codex-plugin/skills/thrum-role-config "${CODEX_HOME:-$HOME/.codex}/skills/"
cp -R codex-plugin/skills/project-setup "${CODEX_HOME:-$HOME/.codex}/skills/"
```

## Troubleshooting

**Skills do not appear in Codex**

- Confirm install destination: `echo "${CODEX_HOME:-$HOME/.codex}/skills"`
- Re-run `./codex-plugin/scripts/install-skills.sh --force`
- Restart Codex

**Skill updates are not reflected**

- Run `./codex-plugin/scripts/sync-skills.sh`
- Restart Codex after sync

**Thrum commands fail inside skill workflows**

- Check daemon status: `thrum daemon status`
- Verify identity/session: `thrum whoami && thrum session start`
- Confirm repo initialization: `thrum status`

## Codex Bundle vs Claude Plugin

| Capability           | Codex Skill Bundle (`codex-plugin/`) | Claude Code Plugin (`claude plugin`) |
| -------------------- | ------------------------------------- | ------------------------------------ |
| Packaging            | Local skill folders                   | Marketplace plugin                   |
| Installation         | `install-skills.sh`                   | `claude plugin install thrum`        |
| Updates              | `sync-skills.sh`                      | Reinstall/update plugin              |
| Command UX           | Skill-guided CLI workflows            | Slash commands (`/thrum:*`)          |
| Role customization   | `thrum-role-config` skill             | Configure-roles plugin skill         |
| Project decomposition| `project-setup` skill                 | project-setup plugin skill           |
