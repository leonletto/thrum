#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PLUGIN_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${PLUGIN_ROOT}/.." && pwd)"

CLAUDE_RESOURCES="${REPO_ROOT}/claude-plugin/skills/thrum/resources"
CLAUDE_COMMANDS="${REPO_ROOT}/claude-plugin/commands"

rm -rf "${PLUGIN_ROOT}/skills/thrum-core/references"
rm -rf "${PLUGIN_ROOT}/skills/thrum-ops/references"
mkdir -p "${PLUGIN_ROOT}/skills/thrum-core/references"
mkdir -p "${PLUGIN_ROOT}/skills/thrum-ops/references"

cp "${CLAUDE_RESOURCES}/CLI_REFERENCE.md" "${PLUGIN_ROOT}/skills/thrum-ops/references/CLI_REFERENCE.md"

for res_file in "${CLAUDE_RESOURCES}"/*.md; do
  fname="$(basename "${res_file}")"
  if [[ "${fname}" == "CLI_REFERENCE.md" ]]; then
    continue
  fi
  cp "${res_file}" "${PLUGIN_ROOT}/skills/thrum-core/references/${fname}"
done

for cmd_file in "${CLAUDE_COMMANDS}"/*.md; do
  cp "${cmd_file}" "${PLUGIN_ROOT}/skills/thrum-ops/references/$(basename "${cmd_file}")"
done

cp "${REPO_ROOT}/codex-plugin/skills/thrum-core/references/MESSAGE_LISTENER_AGENT.md" \
  "${PLUGIN_ROOT}/skills/thrum-core/references/MESSAGE_LISTENER_AGENT.md"

PLUGIN_ROOT="${PLUGIN_ROOT}" python3 <<'PY'
from pathlib import Path
import os

plugin_root = Path(os.environ["PLUGIN_ROOT"])


def replace_in(relative_path: str, replacements: list[tuple[str, str]]) -> None:
    path = plugin_root / relative_path
    text = path.read_text()
    for old, new in replacements:
        if old not in text:
            raise SystemExit(f"expected text not found in {relative_path}: {old[:80]!r}")
        text = text.replace(old, new)
    path.write_text(text)


replace_in(
    "skills/thrum-core/references/WORKTREES.md",
    [
        (
            """Use the setup script to create worktrees with all required configuration:

```bash
# Full setup: creates worktree, branch, thrum redirect, .claude/settings.json, and identity
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/feature feature/my-feature \\
  --identity feature_impl --role implementer

# Existing worktree: adds thrum redirect and .claude/settings.json
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/feature

# Auto-detect: fixes all worktrees missing redirects or settings
./scripts/setup-worktree-thrum.sh
```

### Critical: `.claude/settings.json`

This file is **gitignored** — each worktree needs its own copy. It registers the
`SessionStart` hook that runs `scripts/thrum-startup.sh` (agent registration,
daemon check, env vars). Without it, Claude Code sessions in the worktree won't
auto-register with Thrum.

The setup script copies it automatically from the main repo. If a worktree is
missing it, either re-run the setup script or copy manually:

```bash
cp /path/to/main-repo/.claude/settings.json ~/.workspaces/thrum/feature/.claude/settings.json
```""",
            """Use the setup script to create worktrees with all required configuration:

```bash
# Full setup: creates worktree, branch, thrum redirect, local runtime config, and identity
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/feature feature/my-feature \\
  --identity feature_impl --role implementer

# Existing worktree: refreshes thrum redirects and any local runtime config
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/feature

# Auto-detect: fixes worktrees missing redirects or local runtime setup
./scripts/setup-worktree-thrum.sh
```

### Critical: per-worktree local config

Some runtimes use gitignored per-worktree local settings. Re-run the setup
script for the target worktree so it refreshes any local runtime configuration
Thrum expects there.

```bash
# Refresh redirects + runtime-local config for an existing worktree
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/feature
```""",
        )
    ],
)

replace_in(
    "skills/thrum-core/references/MESSAGING.md",
    [
        (
            "Plugin hooks auto-run `thrum prime` on **SessionStart** and **PreCompact**.",
            "Some runtimes or local project automation may run `thrum prime` on session start or before compaction. In Cursor, do not assume this exists; run `thrum prime` explicitly when starting or resuming work.",
        ),
        (
            """### After Compaction

Context auto-recovers via the PreCompact hook. The agent sees:""",
            """### After Compaction

After compaction or context loss, re-run `thrum prime` (or `thrum context show`)
to restore working context. Typical recovery includes:""",
        ),
    ],
)

replace_in(
    "skills/thrum-core/references/LISTENER_PATTERN.md",
    [
        (
            "file so the Stop hook can detect if the listener dies and prompt a restart.",
            "file so a local watchdog or session-end reminder can detect if the listener dies and prompt a restart.",
        ),
        (
            """```text
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages.\\nSTEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat\\nSTEP_2: thrum wait --timeout 8m --after -15s"
)
```""",
            """```text
Launch a background agent (or delegated subagent) with a prompt like:

Listen for Thrum messages.
STEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat
STEP_2: thrum wait --timeout 8m --after -15s
```""",
        ),
        (
            "1. **Spawn** — Launch as background Task with `run_in_background: true`",
            "1. **Spawn** — Launch as a background agent in your runtime",
        ),
        (
            """The Stop hook reads this heartbeat. If it's missing, stale (>10 min), or from a
different session, the hook tells Claude to restart the listener.""",
            """A local watchdog or session-end reminder can read this heartbeat. If it is
missing, stale (>10 min), or from a different session, restart the listener.""",
        ),
        (
            r'''## Cron Watchdog (Recommended)

Use a cron job to automatically respawn the listener if it dies, times out, or
is lost after compaction. This eliminates manual re-arming entirely.

```text
CronCreate(
  cron="*/30 * * * *",
  prompt="If there is no background message listener running, spawn one now:\n\nAgent(subagent_type=\"message-listener\", model=\"haiku\", run_in_background=true, prompt=\"Listen for Thrum messages.\\nSTEP_1: /path/to/repo/scripts/thrum-startup.sh --listener-heartbeat\\nSTEP_2: thrum wait --timeout 8m --after -15s\")"
)
```

The cron fires every 30 minutes. If a listener is already running, it skips. If
the listener has died or expired, it spawns a new one. Combined with the 4-hour
listener budget, this provides continuous coverage with minimal overhead.

**Setup:** Spawn the initial listener on session start, then create the cron
watchdog. The cron handles all subsequent re-arming automatically.''',
            """## Optional Watchdog Automation

If your environment supports scheduled automation, add a 30-minute watchdog
that checks for a healthy listener heartbeat and starts a new background
listener when needed.

Recommended behavior:

1. Check whether the listener heartbeat is present and recent.
2. If it is missing or stale, launch a new background listener using the prompt
   from the quick-start section above.
3. If a healthy listener is already running, do nothing.

**Setup:** Start one listener at session start, then let the watchdog handle
future re-arming if your workflow supports it.""",
        ),
        (
            "- **Heartbeat before each wait** — keeps the Stop hook informed",
            "- **Heartbeat before each wait** — keeps the watchdog/reminder informed",
        ),
        (
            "- **Cost-efficient** — runs on Haiku, blocks instead of polling",
            "- **Cost-efficient** — use a low-cost model and block instead of polling",
        ),
        (
            "   message or timeout (stays under Bash 600s limit)",
            "   message or timeout (keeps each wait cycle bounded)",
        ),
        (
            "| `--timeout 8m` | Block up to 8 min per cycle (under Bash 600s limit)                             |",
            "| `--timeout 8m` | Block up to 8 min per cycle while keeping each wait cycle bounded               |",
        ),
        (
            "- Listener uses CLI only (`Bash` tool), not MCP tools",
            "- Listener uses CLI only through the shell/terminal tool, not MCP tools",
        ),
    ],
)

replace_in(
    "skills/thrum-core/references/BOUNDARIES.md",
    [
        (
            "| Native tool integration in Claude Code | Shell-out via Bash             |",
            "| Native tool integration in MCP-capable runtimes | Shell-out via shell/terminal |",
        ),
        (
            """**Rule of thumb:** Use CLI via `Bash(thrum:*)`. The SKILL.md `allowed-tools` is
set to `Bash(thrum:*)` — all data is accessed via CLI output, not file reads.""",
            """**Rule of thumb:** Use the `thrum` CLI via your runtime's shell/terminal tool.
Prefer CLI output over reading Thrum data files directly.""",
        ),
    ],
)

replace_in(
    "skills/thrum-core/references/ANTI_PATTERNS.md",
    [
        (
            """```text
Task(subagent_type="message-listener", run_in_background=true, prompt="...")
```""",
            """```text
Launch a new background message-listener agent with the listener prompt from
`LISTENER_PATTERN.md`.
```""",
        ),
        (
            """**Wrong:** Reading `.git/thrum-sync/` files directly with the Read tool.
**Right:** Use `thrum inbox`, `thrum status`, `thrum prime`. The SKILL.md
`allowed-tools` is `Bash(thrum:*)` — no Read permission needed.""",
            """**Wrong:** Reading `.git/thrum-sync/` files directly.
**Right:** Use `thrum inbox`, `thrum status`, and `thrum prime` through your
runtime's shell/terminal tool instead of inspecting Thrum internals by hand.""",
        ),
    ],
)

replace_in(
    "skills/thrum-core/references/IDENTITY.md",
    [
        ("`claude_impl:implementer:auth`", "`cursor_impl:implementer:auth`"),
    ],
)

replace_in(
    "skills/thrum-core/references/MESSAGE_LISTENER_AGENT.md",
    [
        ("  - Bash", "  - Shell"),
        (
            """You do NOT have access to MCP tools. Do NOT attempt to call `mcp__thrum__*`
tools. You MUST use the Bash tool exclusively to run `thrum` CLI commands.""",
            """You do NOT have access to MCP tools. Do NOT attempt to call `mcp__thrum__*`
tools. Use your runtime's shell/terminal tool exclusively to run `thrum` CLI
commands.""",
        ),
        (
            "1. **Wait for messages** (Bash call): Run the WAIT_CMD from your prompt EXACTLY",
            "1. **Wait for messages** (shell call): Run the WAIT_CMD from your prompt EXACTLY",
        ),
        (
            "**Budget**: You have up to 12 Bash tool calls (6 wait cycles × ~15 min each =",
            "**Budget**: You have up to 12 shell tool calls (6 wait cycles × ~15 min each =",
        ),
        (
            """```text
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s --json"
)
```""",
            """```text
Launch a background agent or delegated subagent with a prompt like:

Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s --json
```""",
        ),
    ],
)

replace_in(
    "skills/thrum-ops/references/update-context.md",
    [
        ("### Step 2: Spawn the Update Agent", "### Step 2: Launch the Update Agent"),
        (
            "Delegate to a **general-purpose subagent** that will:",
            "Delegate to a general-purpose agent or delegated subagent that will:",
        ),
        (
            '''```text
Task(
  subagent_type: "general-purpose",
  description: "Update agent context",
  prompt: """
    You are updating agent context for a thrum-managed project.

    ## Agent's Session Summary
    <paste your narrative from Step 1 here>

    ## Your Job
    1. Run git commands to gather repo state
    2. Run beads commands if available (skip if bd not found)
    3. Run `thrum context show` to read existing context
    4. Compose structured markdown merging session summary with gathered state
    5. Pipe result to `thrum context save`
    6. Return brief summary of what was saved
  """
)
```''',
            """```text
Launch a general-purpose agent with a prompt like:

You are updating agent context for a thrum-managed project.

## Agent's Session Summary
<paste your narrative from Step 1 here>

## Your Job
1. Run git commands to gather repo state
2. Run beads commands if available (skip if bd not found)
3. Run `thrum context show` to read existing context
4. Compose structured markdown merging session summary with gathered state
5. Pipe result to `thrum context save`
6. Return a brief summary of what was saved
```""",
        ),
    ],
)

replace_in(
    "skills/thrum-ops/references/CLI_REFERENCE.md",
    [
        (
            "thrum init --runtime claude                    # Init + generate Claude configs",
            "thrum init --runtime cursor                    # Init + generate Cursor configs",
        ),
        (
            "Configure in `.claude/settings.json`:",
            "If you are wiring the MCP server manually in an MCP-capable runtime, add an equivalent entry to that runtime's local MCP configuration:",
        ),
    ],
)
PY

echo "cursor-plugin reference sync complete"
