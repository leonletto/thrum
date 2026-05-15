## Codex Plugin

> **Prerequisites:** Thrum installed and initialized (`thrum init`), and Codex
> installed with a local skills directory (`~/.agents/skills`, canonical as of
> codex v0.130.0).

## Overview

Codex does not currently use the Claude-style plugin marketplace model. Instead,
Thrum integration is packaged as a **Codex skill bundle** in
`codex-plugin/skills`.

The Codex bundle mirrors the Claude plugin's `thrum:*` command surface as
top-level Codex skills.

The Codex runtime preset has `HasSessionStartHook: true` — the prime-context
banner fires automatically on session start, on parity with the Claude plugin.
No manual `thrum prime` is needed at session open.

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
- Codex installed (v0.130.0+); user skills live in `~/.agents/skills`

If you have not initialized Thrum yet:

```bash
thrum init  # also starts the daemon automatically
thrum quickstart --name myagent --role implementer --module auth
```

## Installation

### One-command install (recommended)

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/thrum-dev/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

This script handles the cache-staging gap in codex 0.130.0 where third-party
marketplace entries aren't loaded until the cache is rebuilt — no manual steps
needed after running it.

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

This installs the bundled skills into `$HOME/.agents/skills`. Codex v0.130.0
([PR #21485](https://github.com/openai/codex/pull/21485)) removed the legacy
`~/.codex/skills/` extra-roots loader; `~/.agents/skills/` is the canonical
user-skills path. If you previously installed under `~/.codex/skills/`, see
[Migrating from `~/.codex/skills/`](#migrating-from-codexskills) below.

### Verify installed skills

```bash
find "$HOME/.agents/skills" -maxdepth 1 -type d \( -name "thrum" -o -name "thrum-*" \)
```

### Migrating from `~/.codex/skills/`

If you upgraded codex past v0.130.0 with skills already installed at the legacy
flat path, move them before your next codex session:

```bash
mkdir -p "$HOME/.agents/skills"
mv "$HOME/.codex/skills/thrum"* "$HOME/.agents/skills/" 2>/dev/null || true
mv "$HOME/.codex/skills/orchestrate" "$HOME/.agents/skills/" 2>/dev/null || true
```

Then rerun `./codex-plugin/scripts/install-skills.sh --force` to refresh.

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
mkdir -p "$HOME/.agents/skills"
cp -R codex-plugin/skills/thrum "$HOME/.agents/skills/"
cp -R codex-plugin/skills/thrum-* "$HOME/.agents/skills/"
```

## Troubleshooting

### Skills do not appear in Codex

- Confirm install destination: `echo "$HOME/.agents/skills"`
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
