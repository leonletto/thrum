# Codex Session Notes - 2026-04-09

This is a temporary handoff note for the Codex/plugin work done in this
session.

## Goal

Revamp the Codex Thrum bundle so Codex registers the real Claude-style Thrum
surface instead of the old Codex-only shim skills, and make the install/update
flow clearer and safer.

## Key findings

- The old installed Codex skills in `~/.codex/skills` were present but stale.
- The old Codex bundle used Codex-only shim names:
  - `thrum-core`
  - `thrum-ops`
  - `thrum-role-config`
  - `project-setup`
- Those names do not match the real Claude plugin skill/command surface.
- The repo docs claimed `./codex-plugin/scripts/sync-skills.sh` existed, but it
  did not.
- `scripts/sync-docs.sh` was causing unrelated repo-wide changes because it ran
  `make fmt-all` and repo-wide markdown linting.

## Main Codex plugin changes

Reworked the Codex bundle to expose this 15-skill surface:

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

Added/new:

- `codex-plugin/skills/thrum/`
- `codex-plugin/skills/thrum-configure-roles/`
- `codex-plugin/skills/thrum-project-setup/`
- generated command-wrapper skills:
  - `codex-plugin/skills/thrum-prime/`
  - `codex-plugin/skills/thrum-overview/`
  - `codex-plugin/skills/thrum-update-project/`
  - `codex-plugin/skills/thrum-team/`
  - `codex-plugin/skills/thrum-inbox/`
  - `codex-plugin/skills/thrum-group/`
  - `codex-plugin/skills/thrum-send/`
  - `codex-plugin/skills/thrum-reply/`
  - `codex-plugin/skills/thrum-wait/`
  - `codex-plugin/skills/thrum-restart/`
  - `codex-plugin/skills/thrum-load-context/`
  - `codex-plugin/skills/thrum-quickstart/`

Removed old shim bundle entries:

- `codex-plugin/skills/thrum-core/`
- `codex-plugin/skills/thrum-ops/`
- `codex-plugin/skills/thrum-role-config/`
- `codex-plugin/skills/project-setup/`
- `codex-plugin/skills/update-project.md`

## Script changes

Updated:

- `scripts/sync-skills.sh`
  - now regenerates the Codex bundle from Claude plugin source
  - creates Codex wrapper skills for the command docs
  - syncs resources into the new `thrum` and `thrum-project-setup` dirs
  - removes reliance on the old split-shim layout

- `codex-plugin/scripts/install-skills.sh`
  - `--force` now removes legacy installed skill dirs:
    - `thrum-core`
    - `thrum-ops`
    - `thrum-role-config`
    - `project-setup`
    - `configure-roles`

Added:

- `codex-plugin/scripts/sync-skills.sh`
  - wrapper entry point that runs the top-level sync and then reinstalls the
    local Codex skills

## Install docs changes

Added:

- `codex-plugin/INSTALL.md`

Intent:

- provide a stable human/agent-facing installer entry point
- keep the shell script as the source of truth for actual install behavior

Updated docs:

- `website/docs/codex-plugin.md`
- `docs/codex-plugin.md`

## Sync-docs fix

Updated:

- `scripts/sync-docs.sh`

Change:

- it no longer runs repo-wide `make fmt-all`
- it no longer runs repo-wide `make lint-md-fix`
- it now formats and lint-fixes only:
  - `website/docs/**/*.md`
  - `docs/**/*.md`

This avoids unrelated Go formatting diffs outside the scope of docs sync.

## Validation done

- Regenerated the Codex bundle with `./scripts/sync-skills.sh`
- Verified the repo bundle contained 15 top-level Thrum skill dirs
- Fixed generated wrapper heading issues so markdown lint passes
- Reinstalled the updated skills into `~/.codex/skills`

Installed skill dirs observed in `~/.codex/skills`:

- `thrum`
- `thrum-configure-roles`
- `thrum-group`
- `thrum-inbox`
- `thrum-load-context`
- `thrum-overview`
- `thrum-prime`
- `thrum-project-setup`
- `thrum-quickstart`
- `thrum-reply`
- `thrum-restart`
- `thrum-send`
- `thrum-team`
- `thrum-update-project`
- `thrum-wait`

## Git state changes during session

- Confirmed this worktree was behind `origin/thrum-dev`
- Safely stashed all local uncommitted Codex/docs changes, including untracked
  files
- Fast-forwarded this worktree to latest `origin/thrum-dev`
- Reapplied the stash cleanly on top

Result:

- current worktree is on top of latest `thrum-dev`
- Codex plugin changes remain uncommitted in the working tree

## Important note after restart

You restarted specifically to confirm whether the new Codex skill install was
picked up. After restart, check whether Codex now exposes the 15 Thrum skills
listed above. If it does not, the next thing to investigate is Codex discovery
behavior between:

- `~/.codex/skills`
- `~/.agents/skills`

The repo also suggests `.agents/skills` may be a standard cross-agent path, but
this session installed directly into `~/.codex/skills`.
