## Thrum Quickstart Guide

Install Thrum, register an agent, send your first message. Five minutes.

## Installation

### Install Script (recommended)

This script downloads the latest release binary and verifies the SHA-256
checksum. If no release is available for your platform, it falls back to
`go install` or builds from source.

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
```

### Homebrew

```bash
brew install leonletto/tap/thrum
```

### From Source

```bash
git clone https://github.com/leonletto/thrum.git
cd thrum
make install  # Builds UI + Go binary, installs to ~/.local/bin
```

## Fast Path

One command to scaffold the project, register your agent, configure worktrees,
install role templates, and start the daemon:

```bash
cd your-project
thrum init
```

On a TTY, `thrum init` launches an opinionated wizard that walks you through
identity, worktrees root, role templates, and daemon start. Press enter through
every prompt to accept the recommended defaults. When the wizard finishes you
are fully registered and can send your first message.

Need a non-interactive run for CI or scripted setup? Pre-fill every prompt with
flags (the wizard skips prompts whose value is supplied):

```bash
thrum init --name myagent --role implementer --module auth \
           --worktrees-root ~/.thrum/worktrees/myproj \
           --roles=enhanced
```

Force the legacy silent path explicitly with `--non-interactive` (existing CI
scripts that pipe stdin already get this behavior automatically).

The sections below walk through each step individually if you want to understand
what the wizard is doing.

## Step-by-Step Walkthrough

### 1. Initialize Repository

Navigate to your project and initialize Thrum:

```bash
cd your-project
thrum init
```

The wizard creates:

- `.thrum/` directory (gitignored entirely)
- `.git/thrum-sync/a-sync/` sync worktree on the `a-sync` orphan branch (for
  JSONL event logs)
- `.thrum/identities/` for agent identity files (your initial identity is
  registered here automatically)
- `.thrum/var/` for daemon runtime files
- `.thrum/role_templates/` populated with the role templates you choose
- `a-sync` orphan branch for message synchronization
- `Worktrees.BasePath` in `.thrum/config.json` set to the directory you pick
- The daemon, started in the background

The wizard also runs `thrum quickstart` for you, so you do not need to run it
separately. The "Step 4: Register" section below applies only when you ran
`thrum init --non-interactive` and want to register an agent later.

#### Worktree base path migration

In v0.10.0 the implicit fallback for `Worktrees.BasePath` migrated from
`~/.workspaces/<project>` to `~/.thrum/worktrees/<project>`. Users with an
explicit `Worktrees.BasePath` in `.thrum/config.json` are unaffected. If you
relied on the legacy fallback and want to keep existing worktrees in place, set
the override before your next `thrum worktree create`:

```bash
thrum config set worktrees.base_path "$HOME/.workspaces/<project>"
```

The wizard also offers to set this path interactively — press enter to accept
the new default, or type the legacy path to preserve existing worktrees.

**v0.7.0 default:** New repos start in single-agent mode
(`single_agent_mode: true`). This gives you context management (`thrum prime`,
sessions, context save/show) without the messaging layer (no listener, no cron
watchdog, no messaging protocol in preambles). If you need multi-agent
coordination, run `thrum single-agent-mode false`. See
[Single-Agent Mode](single-agent-mode.md) for details.

### 2. Install the Thrum Skill

Install the thrum skill so your agent knows how to use thrum for coordination.
This works with Claude Code, Cursor, Codex, Gemini, and Amp:

```bash
thrum init --skills
```

Thrum auto-detects which agent you're using and installs the skill to the right
location (e.g., `.claude/skills/thrum/` for Claude Code, `.cursor/skills/thrum/`
for Cursor). If no agent-specific directory exists, it installs to
`.agents/skills/thrum/` which all agents check.

You can also target a specific agent:

```bash
thrum init --skills --runtime cursor    # Install for Cursor specifically
thrum init --skills --runtime codex     # Install for Codex specifically
```

**Claude Code full plugin (optional):** If you want the complete experience with
slash commands, automatic context injection, hooks, and startup scripts, install
the Claude Code plugin instead:

```bash
claude plugin marketplace add https://github.com/leonletto/thrum
claude plugin install thrum
```

The plugin already includes the skill — `thrum init --skills` will detect the
plugin and skip the install. See [Claude Code Plugin](claude-code-plugin.md) for
details.

### 3. Wire Thrum into Your Agent

Pick **one** of the two paths below. They're alternatives — don't run both.

**Path A: Claude Code with the Thrum plugin (recommended).** If you installed
the plugin in Step 2 above, you're done with this step. The plugin's
SessionStart hook injects messaging instructions, slash commands cover the
common operations, and skills disclose deeper docs on demand. There is nothing
to add to your CLAUDE.md — the plugin already provides everything agents need.

**Path B: Claude Code without the plugin, or another runtime.** Inject a minimal
Thrum coordination block into your repository's `CLAUDE.md` (or equivalent
agent-instructions file):

```bash
thrum setup claude-md --apply
```

What this does:

- If `CLAUDE.md` doesn't exist, creates it with a Thrum block and nothing else.
- If `CLAUDE.md` exists and has no Thrum block, appends a blank line plus the
  block at the end. Existing content is preserved byte-for-byte.
- If `CLAUDE.md` already contains a Thrum block (between `<!-- BEGIN THRUM -->`
  and `<!-- END THRUM -->` markers), exits with an error and tells you to re-run
  with `--force`.

Re-run with `--force` after upgrading thrum to refresh the block in place:

```bash
thrum setup claude-md --apply --force
```

The block is intentionally minimal — what Thrum is, the five essential commands
(`whoami`, `team`, `inbox`, `send`, `reply`), and a pointer to the full docs at
<https://thrum.team>. To preview the template without writing to disk, omit
`--apply`:

```bash
thrum setup claude-md   # prints to stdout
```

### 4. Register Your Agent and Start a Session

The fastest way is the quickstart command, which registers, starts a session,
and sets your intent in one step:

```bash
thrum quickstart --name myagent --role implementer --module auth --intent "Working on auth"
```

Or register manually with individual commands:

```bash
thrum agent register --name myagent --role=implementer --module=auth
thrum session start
```

Agent names must be lowercase alphanumeric with underscores (`[a-z0-9_]+`).
Reserved names: `daemon`, `system`, `thrum`, `all`, `broadcast`.

Optional flags: `--runtime <name>` sets `preferred_runtime` in the identity file
(useful for mixed-runtime teams). `--dry-run` previews without writing.

### 5. Send Your First Message

```bash
thrum send "Started working on user authentication" \
  --scope module:auth \
  --ref issue:beads-123
```

### 6. Check Your Inbox

```bash
thrum inbox
thrum sent
thrum message read --all     # Mark all messages as read
```

You'll see messages from other agents and humans on the project. `thrum sent`
shows what you've sent, who it went to, and whether they've read it.

## Common Commands

### Check Status

```bash
thrum overview
```

Shows:

- Your agent identity
- Active session
- Inbox counts
- Sync status
- Team overview

### Wait for Notifications

Use `thrum wait` to block until a message arrives — useful in automation and
hooks. See [CLI Reference](cli.md#thrum-wait) for flags.

### Sync Control

```bash
# Check sync status
thrum sync status

# Force immediate sync
thrum sync force
```

### Context Management

```bash
# Save context for session continuity
thrum context save --file continuation-notes.md

# View saved context
thrum context show

# Clear context
thrum context clear

# Share context across worktrees (manual sync)
thrum context sync
```

### Agent Management

```bash
# Delete an agent
thrum agent delete myagent

# Detect orphaned agents (preview)
thrum agent cleanup --dry-run

# Delete all orphaned agents
thrum agent cleanup --force
```

### End Session

```bash
thrum session end
```

### MCP Server (for LLM Agents)

Start an MCP server for native tool-based messaging (e.g., from Claude Code):

```bash
thrum mcp serve
thrum mcp serve --agent-id myagent  # Override agent identity
```

See [MCP Server](mcp-server.md) for configuration and the complete tools
reference (4 core messaging tools).

## Typical Workflow

### Morning: Start Work

```bash
# 1. Register and start session (or just start session if already registered)
#    (Use `thrum daemon start` explicitly if the daemon stopped for any reason)
thrum quickstart --name myagent --role implementer --module auth --intent "Working on auth"

# 2. Check inbox for updates
thrum inbox --unread         # does not mark messages as read

# 2b. Check sent items for delivery/read state
thrum sent --unread

# 2c. Mark all messages as read when done reviewing
thrum message read --all

# 3. Block until a message arrives (useful in automation/hooks)
# thrum wait --timeout 5m
```

### During Work: Send Updates

```bash
# Progress updates
thrum send "Implemented password hashing" \
  --scope module:auth \
  --ref issue:beads-123

# Request review
thrum send "Auth module ready for review" \
  --scope module:auth \
  --mention @reviewer
```

### Evening: End Work

```bash
# End session
thrum session end

# Check final status
thrum overview
```

## Working Across Machines

> **Note:** `thrum init` sets `local_only: true` by default. To enable
> cross-machine sync, set `local_only: false` in `.thrum/config.json` or run
> `THRUM_LOCAL=false thrum daemon start`.

Thrum uses Git for synchronization. No cloud service, no opaque API — just push
and pull on the `a-sync` branch.

### On Machine A

```bash
# Make changes, send messages
thrum send "Completed feature X"

# Sync happens automatically every 60s
# Or force sync
thrum sync force
```

### On Machine B

```bash
# Pull latest (includes a-sync branch)
git fetch origin
git merge origin/main

# Daemon automatically syncs messages
# Or force sync
thrum sync force

# Check inbox
thrum inbox

# Check sent items
thrum sent
```

## Working with Multiple Worktrees

Feature worktrees share the main worktree's daemon and message store via a
redirect file. Use `thrum worktree create` (alias: `thrum worktree setup`) to
create and configure a feature worktree in one step:

```bash
# Create a worktree with redirect setup only
thrum worktree create auth -b feature/auth
# or equivalently:
thrum worktree setup auth -b feature/auth

# With agent quickstart flags — creates the worktree, registers the agent,
# and creates the tmux session in one step. The agent is NOT running yet —
# you launch it in the next step.
thrum worktree create auth -b feature/auth \
  --name furiosa --role implementer --module auth

# Start the runtime in the tmux session
thrum tmux launch auth
```

The worktree is created at `worktrees.base_path/<name>` (default
`~/.thrum/worktrees/<repo>/<name>`). `thrum worktree create` handles the
redirect file creation automatically — you don't need to run
`thrum setup --main-repo` separately. If you pass `--name`, `--role`, and
`--module`, it also creates a real tmux session and registers the agent inside
it (PID-isolated, with retry if the shell init swallows the first attempt). The
output tells you the agent is not running yet and shows the exact
`thrum tmux launch` command to start it.

For an existing worktree that just needs redirect setup, you can still use the
manual approach:

```bash
cd ~/project-features/auth
thrum setup --main-repo ~/project
thrum session start
thrum send "Experimenting with auth approaches"
```

The `thrum setup --main-repo <path>` command creates a `.thrum/redirect` file
pointing to the main worktree's `.thrum/` directory. All worktrees then share
the same sync worktree, daemon, and message store. Messages sync across all
worktrees and machines through Git.

### Use the setup scripts for batch configuration

Two shell scripts automate redirect file creation for all your worktrees at
once:

```bash
# Set up Thrum redirects for all worktrees
./scripts/setup-worktree-thrum.sh

# Set up Beads redirects for all worktrees
./scripts/setup-worktree-beads.sh
```

Both scripts auto-detect worktrees via `git worktree list` and create the
appropriate redirect files. They skip worktrees that are already configured.

#### Set up a single worktree

```bash
# Thrum redirect for one worktree
./scripts/setup-worktree-thrum.sh ~/.thrum/worktrees/thrum/my-feature

# Beads redirect for one worktree
./scripts/setup-worktree-beads.sh ~/.thrum/worktrees/thrum/my-feature
```

#### What the scripts create

Each script creates a redirect file pointing to the main repository:

```text
# In the worktree:
.thrum/redirect    → /path/to/main/repo/.thrum
.beads/redirect    → /path/to/main/repo/.beads
```

This ensures all worktrees share the same daemon, message store, and issue
tracker. The scripts are idempotent — run them as many times as you need.

## Troubleshooting

### Daemon won't start

```bash
# Check if already running (shows repo path from JSON PID file)
thrum daemon status

# Stop and restart
thrum daemon stop
thrum daemon start

# Check PID file (JSON format: PID, RepoPath, StartedAt, SocketPath)
cat .thrum/var/thrum.pid
```

The daemon uses flock-based locking (`.thrum/var/thrum.lock`) for SIGKILL
resilience and pre-startup duplicate detection to prevent multiple daemons
serving the same repository.

### Messages not syncing

```bash
# Check sync status
thrum sync status

# Force sync
thrum sync force

# Check Git branches
git branch -a | grep a-sync
```

### Registration conflicts

```bash
# Another agent registered with same role+module
thrum agent register --force  # Override

# Or use different role/module
thrum agent register --role=implementer-2 --module=auth
```

## Key Concepts

### Messages

Messages are persistent records stored in Git-tracked JSONL. They're just text —
`cat` them, `grep` them, pipe them through `jq`.

### Sessions

A session is a work period tied to your agent. You need an active session to
send messages.

### Scopes

Scopes tag messages by context — `module:auth`, `file:src/auth.go`, and so on.
Use them to filter your inbox.

### Live Inbox

The daemon pushes new messages to connected WebSocket clients in real time. From
the CLI, use `thrum wait` to block until a message arrives addressed to you.

### Sync

The sync process runs in the background every 60 seconds and pushes/pulls
messages via Git. Data lives on the `a-sync` orphan branch, accessed through a
sparse-checkout worktree at `.git/thrum-sync/a-sync/`. You never switch branches
manually.

### Daemon

The daemon runs in the background and handles RPC requests, sync, and the
embedded Web UI. The WebSocket and SPA share the same port (default 9999).

### MCP Server

`thrum mcp serve` starts an MCP server for environments that support native tool
integration. The CLI works everywhere — MCP is just an alternative transport.

## Tips

1. **Always start a session** before sending messages
2. **Use `thrum wait`** to block until a message arrives in automation
3. **Use scopes** to categorize messages
4. **Mention other agents** when you need their attention
5. **Check sync status** if messages aren't appearing
6. **Use `--json` flag** for scripting and automation
7. **Back up your data** regularly: `thrum backup`
8. **Enable automatic backups**: `thrum backup schedule 24h`

## Tmux Setup (Recommended for Multi-Agent)

If you're planning to run multiple agents, install tmux and enable mouse
support. This lets the coordinator create and manage agent sessions
automatically — no background listeners needed.

```bash
# Install tmux
brew install tmux  # macOS
# or: sudo apt install tmux  # Ubuntu/Debian

# Enable mouse scrolling (critical for a good experience)
echo "set -g mouse on" >> ~/.tmux.conf
```

`thrum tmux create` requires `--name`, `--role`, and `--module` flags (or
`--no-agent` to skip registration). It runs quickstart automatically inside the
new pane. Calling it without these flags errors out.

```bash
# Correct: flags required
thrum tmux create --name furiosa --role implementer --module auth

# Alias that does the same thing
thrum tmux quickstart --name furiosa --role implementer --module auth

# No-agent mode (pane only, no registration)
thrum tmux create --no-agent --session mywork
```

See [Tmux-Managed Sessions](tmux-sessions.md) for the full story.

**Beads in worktrees:** If you use [Beads](beads-and-thrum.md) for task
tracking, note that `bd init` doesn't auto-detect worktrees like `thrum init`
does. Set up the redirect manually:

```bash
cd your-worktree
mkdir -p .beads && echo /path/to/main/repo/.beads > .beads/redirect
```

## Pick your scenario

Once you've got the basics running, pick the scenario that matches what you're
building:

- [Solo Dev with One Agent](scenarios/solo-dev.md) — single agent, single
  machine
- [Team on Your Machine](scenarios/team.md) — multiple agents in parallel
  worktrees
- [Agents Across Repos/Machines](scenarios/across-boundaries.md) — peers across
  repos or machines
- [Automated Plan Execution](scenarios/orchestration.md) — hand a plan to the
  orchestrator

## Next Steps

- [Tmux-Managed Sessions](tmux-sessions.md) — daemon-managed agent sessions with
  instant message delivery and zero background listeners
- [Why Thrum Exists](philosophy.md) — understand the philosophy behind
  human-directed agent coordination before going deeper
- [CLI Reference](cli.md) — complete documentation for every command and flag
- [Messaging](messaging.md) — send and receive messages between agents,
  including scopes, mentions, and threads
- [Agent Coordination](agent-coordination.md) — practical multi-agent workflows
  with Beads integration and session templates
