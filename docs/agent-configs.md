## Agent Configurations

> **Recommended:** Install the [Thrum plugin](claude-code-plugin.md) instead of
> manual agent definitions. The plugin provides 10 slash commands, automatic
> context hooks, and 8 resource docs — all in a single install.

Claude Code agent definitions teach Claude how to use Thrum effectively. These
`.md` files with YAML frontmatter ship in `toolkit/agents/` and load into your
project's `.claude/agents/` directory. For Beads task tracking, see
[Beads Setup (`bd setup claude`)](#beads-setup-bd-setup-claude) below — that's
the canonical integration path.

## Install the agent configs

Copy the agent definitions to your project:

```bash
mkdir -p .claude/agents
cp toolkit/agents/thrum-agent.md .claude/agents/
cp toolkit/agents/message-listener.md .claude/agents/
```

Claude Code automatically detects and loads these files when you start a
session.

## Available agents

### thrum-agent

Comprehensive guide for Thrum messaging. Teaches Claude how to register agents,
send/receive messages, and coordinate with teammates via the CLI.

**Use when:**

- Coordinating multi-agent workflows
- Working across worktrees or machines
- Requesting code reviews or assigning tasks
- Broadcasting status updates

**Key capabilities:**

- Agent registration with roles and intents
- Direct messaging and broadcasts
- MCP server integration with async notifications
- Session management and heartbeats

### Beads Setup (`bd setup claude`)

For Beads issue tracking, install the **Beads CLI** and let `bd setup claude`
configure the SessionStart hook. Upstream Beads now ships this as the canonical
Claude Code integration path — lighter-weight than the standalone plugin and the
hook auto-runs `bd prime` so agents start every session with full workflow
context.

If you're working in a Thrum-managed project, you don't need to do this yourself
— `thrum init` (and the runtime-init pass on each session) installs the bd
`SessionStart` hook in `.claude/settings.json` whenever `Worktrees.BeadsEnabled`
is true (the default) **and** `bd` is on `PATH`. The hook is removed
automatically if `bd` is uninstalled.

To configure manually (outside a Thrum project, or to set it up yourself):

```bash
brew install beads
bd setup claude
```

Then restart Claude Code so the SessionStart hook loads. See the
[Beads project](https://github.com/steveyegge/beads) for the full CLI reference.

**Migrating from the standalone Beads plugin** — run these five steps in order:

1. `/plugin uninstall beads@beads-marketplace` (inside Claude Code)
2. `/plugin marketplace remove beads-marketplace` (inside Claude Code)
3. `brew install beads`
4. `bd setup claude`
5. Restart Claude Code

The standalone plugin is no longer recommended. If `bd` state changes (installed
or uninstalled) after your first `thrum init`, re-run `thrum init` to refresh
the bd-hook presence in `.claude/settings.json`.

### message-listener

Lightweight background listener that watches for incoming Thrum messages so you
don't have to manually check your inbox. Runs on Haiku for cost efficiency
(~$0.00003/cycle, ~65% fewer tokens than the old pattern). Uses `thrum wait` for
efficient blocking instead of polling loops — returns immediately when messages
arrive, covers up to 4 hours across 30 cycles. A cron watchdog auto-respawns it
every 30 min if it stops — no manual re-arming needed.

**Use when:**

- You're running multiple agents and want to know when they message you
- Working long sessions where agents on other worktrees may send updates
- You want incoming messages surfaced without manually running `thrum inbox`

**Key capabilities:**

- Blocking wait via `thrum wait --timeout 8m` (30 cycles max, ~4 hours, filters
  by agent identity)
- Immediate return on message arrival
- Time-based filtering with `--after` flag (negative value = "N ago"; e.g.,
  `-1s` includes messages sent up to 1 second ago)
- CLI-only (no MCP tools — sub-agents can't access MCP)
- Cron watchdog auto-respawns the listener every 30 min if it is not running

## Configure the message listener

The message-listener runs as a background task so your main agent session stays
focused on work while messages are watched for you.

Launch it at session start:

```typescript
// In Claude Code with Thrum MCP configured
Task({
  subagent_type: "message-listener",
  model: "haiku",
  run_in_background: true,
  prompt:
    "Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s",
});
```

**Wait command flags:**

- `--timeout 8m` — Block up to 8 minutes per cycle
- `--after -15s` — Include messages sent up to 15 seconds ago (negative = "N
  ago"; covers restart gap)

The listener uses `thrum wait` which blocks until a message arrives or the
timeout expires — no polling loops needed. Each cycle is a single Bash call. The
listener loops automatically for up to 4 hours (30 cycles). Set up a cron
watchdog to auto-respawn it if it stops:

```text
CronCreate(cron="*/30 * * * *",
  prompt="If there is no background message listener running, spawn one now:
    Task(subagent_type='message-listener', model='haiku', run_in_background=true,
      prompt='Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s --json')")
```

## Customize for your project

You can edit these agent definitions to match your team's workflows. Add
project-specific commands, adjust priorities, or include custom context.

For the agent file format, see
[Claude Code agent documentation](https://docs.anthropic.com/claude-code).

## Next Steps

- [Claude Code Plugin](claude-code-plugin.md) — the recommended approach:
  install the plugin to get 10 slash commands and automatic hooks instead
- [Codex Plugin](codex-plugin.md) — the Codex skill bundle for users who prefer
  skill-guided CLI workflows
- [Agent Coordination](agent-coordination.md) — multi-agent messaging patterns
  that these agent definitions enable
- [Workflow Templates](workflow-templates.md) — pre-built skill pipelines for
  the full research → plan → implement cycle
