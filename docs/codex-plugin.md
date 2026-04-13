## Codex Plugin

> **Prerequisites:** Thrum installed and initialized (`thrum init`), and Codex
> installed with a local skills directory (`~/.codex/skills`).

## Overview

Codex does not currently use the Claude-style plugin marketplace model. Instead,
Thrum integration is packaged as a **Codex skill bundle** in
`codex-plugin/skills`.

The Codex bundle mirrors the Claude plugin's `thrum:*` command surface as
top-level Codex skills.

Registered skills:

- `thrum`
- `thrum-prime`
- `thrum-overview`
- `thrum-update-project`
- `thrum-team`
- `thrum-inbox`
- `thrum-group`
- `thrum-send`
- `thrum-reply`
- `thrum-wait`
- `thrum-restart`
- `thrum-load-context`
- `thrum-quickstart`
- `thrum-configure-roles`
- `thrum-project-setup`

## Prerequisites

- Thrum installed and available on `PATH`
- Thrum initialized in the repo (`thrum init`)
- Codex installed with local skills directory (`~/.codex/skills`)

If you have not initialized Thrum yet:

```bash
thrum init  # also starts the daemon automatically
thrum quickstart --name myagent --role implementer --module auth
```

## Installation

### From GitHub

Clone the Thrum repository and run the install script:

```bash
git clone https://github.com/leonletto/thrum.git
cd thrum
./codex-plugin/scripts/install-skills.sh
```

You can also use the packaged installer guide in `codex-plugin/INSTALL.md`,
which points at the same script and lists the expected 15 installed skills.

### From Local Clone

If you already have the Thrum repository cloned locally:

```bash
cd /path/to/thrum
./codex-plugin/scripts/install-skills.sh
```

This installs the bundled skills into `~/.codex/skills` (or `$CODEX_HOME/skills`
if `CODEX_HOME` is set).

### Verify installed skills

```bash
find "${CODEX_HOME:-$HOME/.codex}/skills" -maxdepth 1 -type d \( -name "thrum" -o -name "thrum-*" \)
```

### Restart Codex

Restart Codex after installation so the new skills are loaded.

## Updating Skills During Development

After editing the Claude plugin source or Codex bundle files, sync updates into
your local Codex installation:

```bash
./codex-plugin/scripts/sync-skills.sh
```

`sync-skills.sh` replaces installed copies with your local versions.

## Usage Patterns

### Multi-agent coordination

Ask Codex to coordinate work across agents using `thrum`. It acts as the main
entry point and routes into the specific `thrum-*` command skills when the work
becomes command-specific.

### Session operations and triage

When you need one explicit workflow, invoke the matching command skill:
`thrum-prime`, `thrum-overview`, `thrum-inbox`, `thrum-send`, and so on.

### Role template generation

When team structure changes, use `thrum-configure-roles` to detect environment
context and regenerate `.thrum/role_templates/<role>.md` with clear autonomy and
scope boundaries.

## Manual Installation (Optional)

If you prefer manual copy instead of scripts:

```bash
mkdir -p "${CODEX_HOME:-$HOME/.codex}/skills"
cp -R codex-plugin/skills/thrum "${CODEX_HOME:-$HOME/.codex}/skills/"
cp -R codex-plugin/skills/thrum-* "${CODEX_HOME:-$HOME/.codex}/skills/"
```

## Troubleshooting

### Skills do not appear in Codex

- Confirm install destination: `echo "${CODEX_HOME:-$HOME/.codex}/skills"`
- Re-run `./codex-plugin/scripts/install-skills.sh --force`
- Restart Codex

### Skill updates are not reflected

- Run `./codex-plugin/scripts/sync-skills.sh`
- Restart Codex after sync

### Thrum commands fail inside skill workflows

- Check daemon status: `thrum daemon status`
- Verify identity/session: `thrum whoami && thrum session start`
- Confirm repo initialization: `thrum overview`

## Codex Bundle vs Claude Plugin

| Capability            | Codex Skill Bundle (`codex-plugin/`) | Claude Code Plugin (`claude plugin`) |
| --------------------- | ------------------------------------ | ------------------------------------ |
| Packaging             | Local skill folders                  | Marketplace plugin                   |
| Installation          | `install-skills.sh`                  | `claude plugin install thrum`        |
| Updates               | `sync-skills.sh`                     | Reinstall/update plugin              |
| Command UX            | Skill-guided CLI workflows           | Slash commands (`/thrum:*`)          |
| Role customization    | `thrum-configure-roles` skill        | `/thrum:configure-roles`             |
| Project decomposition | `thrum-project-setup` skill          | `/thrum:project-setup`               |

## Next Steps

- [Claude Code Plugin](claude-code-plugin.md) — the equivalent plugin for Claude
  Code users, with slash commands and automatic hooks
- [Cursor Plugin](cursor-plugin.md) — the equivalent plugin for Cursor users
- [Agent Coordination](agent-coordination.md) — practical multi-agent workflows
  that the Codex skills support
- [Role Templates](role-templates.md) — role-based preamble templates that
  `thrum-configure-roles` generates
- [Workflow Templates](workflow-templates.md) — the `project-setup` skill's
  design → plan → implement pipeline in detail
