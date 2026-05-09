# Installing Thrum for Codex

Enable Thrum's Codex skills by installing the bundled skill directories into
Codex's local skills folder.

## Prerequisites

- `thrum` installed and available on `PATH`
- Codex installed
- A local clone of this repository

If Thrum is not initialized in your repo yet:

```bash
thrum init
```

## Installation

From the Thrum repository root:

```bash
./codex-plugin/scripts/install-skills.sh --force
```

This installs the current Codex bundle into:

```text
$HOME/.agents/skills
```

Codex v0.130.0 ([PR #21485](https://github.com/openai/codex/pull/21485))
removed the legacy `~/.codex/skills/` extra-roots mechanism. `$HOME/.agents/skills/`
is the canonical user-skills path going forward.

Installed skills:

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

## Verify

```bash
find "$HOME/.agents/skills" -maxdepth 1 -type d \( -name "thrum" -o -name "thrum-*" \) | sort
```

You should see 15 Thrum skill directories.

## Migrating from `~/.codex/skills/` (codex 0.130.0+ upgraders)

If you previously installed Thrum skills under `~/.codex/skills/` (the legacy
path used before codex v0.130.0), move them to the new canonical location
**before your next codex session** after upgrading codex past 0.130.0:

```bash
mkdir -p "$HOME/.agents/skills"
mv ~/.codex/skills/thrum* "$HOME/.agents/skills/" 2>/dev/null || true
```

Codex 0.130.0 ([PR #21485](https://github.com/openai/codex/pull/21485)) removed
the extra-roots loader that previously surfaced flat `~/.codex/skills/`
installs. Skills left at the legacy path will silently stop loading. Rerunning
`./codex-plugin/scripts/install-skills.sh --force` after the move ensures the
bundle is current.

## Restart Codex

Quit and relaunch Codex after installation so it reloads the skill registry.

## Updating During Development

If you change the Claude plugin command/skill source or the Codex bundle:

```bash
./codex-plugin/scripts/sync-skills.sh
```

That regenerates the Codex bundle from the source-of-truth files and reinstalls
the result into your local Codex skills directory.

## Why use the script?

The shell script is the source of truth for installation behavior:

- it removes legacy Codex-only shim skill names
- it installs the current 15-skill Thrum surface
- it avoids stale manual copies

`INSTALL.md` is the entry point for humans and agents. The script performs the
actual install.
