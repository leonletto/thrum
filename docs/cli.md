
## Thrum CLI Reference

> **TL;DR:** You only need 8 commands for daily use — they're in the
> [Overview](overview.md) page. This page is the full reference for all ~30
> commands. Use Ctrl+F or the Quick Reference table at the top to find what you
> need. Storage layout details are at the very bottom.

Complete reference for the `thrum` command-line interface — a messaging system
for AI agent coordination.

## Quick Reference

| Command                       | Description                                                    |
| ----------------------------- | -------------------------------------------------------------- |
| `thrum init`                  | Initialize Thrum in the current repository                     |
| `thrum setup`                 | Configure a feature worktree with `.thrum/redirect`            |
| `thrum quickstart`            | Register, start session, and set intent in one step            |
| `thrum overview`              | Show combined status, team, and inbox view                     |
| `thrum send`                  | Send a message (direct or broadcast)                           |
| `thrum reply`                 | Reply to a message                                             |
| `thrum inbox`                 | List messages in your inbox                                    |
| `thrum sent`                  | List messages you sent with receipt status                     |
| `thrum message get`           | Get a single message with full details                         |
| `thrum message edit`          | Edit a message (full replacement)                              |
| `thrum message delete`        | Delete a message                                               |
| `thrum message read`          | Mark messages as read                                          |
| `thrum purge`                 | Remove old messages, sessions, and events                      |
| `thrum agent register`        | Register this agent with the daemon                            |
| `thrum agent list`            | List registered agents                                         |
| `thrum agent whoami`          | Show current agent identity                                    |
| `thrum agent delete`          | Delete an agent and all associated data                        |
| `thrum agent cleanup`         | Detect and remove orphaned agents                              |
| `thrum agent start`           | Start a new session (alias)                                    |
| `thrum agent end`             | End current session (alias)                                    |
| `thrum agent set-intent`      | Set work intent (alias)                                        |
| `thrum agent set-task`        | Set current task (alias)                                       |
| `thrum agent set-status`      | Set agent operational status                                   |
| `thrum agent heartbeat`       | Send heartbeat (alias)                                         |
| `thrum session start`         | Start a new work session                                       |
| `thrum session end`           | End the current session                                        |
| `thrum session list`          | List sessions (active and ended)                               |
| `thrum session heartbeat`     | Send a session heartbeat                                       |
| `thrum session set-intent`    | Set session work intent                                        |
| `thrum session set-task`      | Set current task identifier                                    |
| `thrum context save`          | Save agent context from file or stdin                          |
| `thrum context show`          | Show agent context                                             |
| `thrum context load`          | Alias for `thrum context show`                                 |
| `thrum context clear`         | Clear agent context                                            |
| `thrum context sync`          | Sync context to a-sync branch                                  |
| `thrum context preamble`      | Show or set the role-template preamble                         |
| `thrum runtime`               | Manage runtime presets (list, show, set-default)               |
| `thrum peer add`              | Start a pairing session and display a peercode                 |
| `thrum peer join`             | Join a peer using a peercode                                   |
| `thrum peer list`             | List all paired peers                                          |
| `thrum peer status`           | Show detailed per-peer health                                  |
| `thrum peer remove`           | Remove a paired peer                                           |
| `thrum peer configure`        | Add or remove proxy agents for a peer                          |
| `thrum single-agent-mode`     | Toggle or query single-agent mode                              |
| `thrum telegram configure`    | Configure the Telegram bridge (interactive or flags)           |
| `thrum telegram status`       | Show Telegram bridge connection status and config              |
| `thrum roles list`            | List role templates and matching agents                        |
| `thrum roles deploy`          | Re-render agent preambles from role templates                  |
| `thrum roles refresh`         | Re-render templates from saved answers + update rendered_hash  |
| `thrum roles save-config`     | Write role_config to .thrum/config.json from JSON on stdin     |
| `thrum roles templates print` | Print an embedded shipped template to stdout                   |
| `thrum config`                | Manage configuration (show, init)                              |
| `thrum who-has`               | Check which agents are editing a file                          |
| `thrum ping`                  | Check if an agent is online                                    |
| `thrum wait`                  | Wait for notifications                                         |
| `thrum daemon start`          | Start the daemon in the background                             |
| `thrum daemon stop`           | Stop the daemon gracefully                                     |
| `thrum daemon status`         | Show daemon status                                             |
| `thrum daemon restart`        | Restart the daemon                                             |
| `thrum daemon logs`           | View daemon log file                                           |
| `thrum sync status`           | Show sync loop status                                          |
| `thrum sync force`            | Trigger an immediate sync                                      |
| `thrum backup`                | Snapshot all thrum data to a backup directory                  |
| `thrum backup status`         | Show last backup info                                          |
| `thrum backup config`         | Show effective backup config                                   |
| `thrum backup restore`        | Restore from latest backup or a specific archive               |
| `thrum backup plugin list`    | List configured backup plugins                                 |
| `thrum backup plugin add`     | Add a backup plugin (or use a built-in preset)                 |
| `thrum backup schedule`       | Configure automatic backup schedule                            |
| `thrum tmux start`            | One-command: create + launch + prime + attach                  |
| `thrum tmux create`           | Create a tmux session for an agent (quickstart flags required) |
| `thrum tmux quickstart`       | Alias for `thrum tmux create`                                  |
| `thrum tmux launch`           | Start an AI tool inside a tmux session                         |
| `thrum tmux connect`          | Attach to a tmux session (interactive picker or by name)       |
| `thrum tmux status`           | Show tmux-managed sessions with state                          |
| `thrum tmux list`             | Alias for `thrum tmux status`                                  |
| `thrum tmux kill`             | Tear down a tmux session                                       |
| `thrum tmux send`             | Send text into a tmux session                                  |
| `thrum tmux capture`          | Capture pane content from a tmux session                       |
| `thrum tmux restart`          | Restart a tmux session with context snapshot                   |
| `thrum tmux queue`            | Submit a command to a session's queue                          |
| `thrum tmux queue-status`     | Show the command queue for a session                           |
| `thrum tmux cancel`           | Cancel a queued or active command                              |
| `thrum tmux snapshot save`    | Save conversation snapshot for session restart                 |
| `thrum tmux snapshot restore` | Output a restart snapshot to stdout                            |
| `thrum tmux snapshot check`   | Check if a restart snapshot exists (exit code)                 |
| `thrum worktree create`       | Create a new worktree with thrum/beads setup                   |
| `thrum worktree setup`        | Alias for `thrum worktree create`                              |
| `thrum worktree teardown`     | Remove a worktree and clean up artifacts                       |
| `thrum worktree list`         | List worktrees with thrum agent info                           |
| `thrum monitor start`         | Start a new monitor job (regex filter + message delivery)      |
| `thrum monitor list`          | List running monitor jobs                                      |
| `thrum monitor show`          | Show full details for a monitor job                            |
| `thrum monitor stop`          | Stop a monitor job                                             |
| `thrum monitor logs`          | Show recent matched output for a monitor job                   |
| `thrum monitor restart`       | Restart a stopped or dead monitor job                          |
| `thrum mcp serve`             | Start MCP stdio server for agent messaging                     |

## Global Flags

Available on all commands:

| Flag        | Description                              | Default |
| ----------- | ---------------------------------------- | ------- |
| `--role`    | Agent role (or `THRUM_ROLE` env var)     |         |
| `--module`  | Agent module (or `THRUM_MODULE` env var) |         |
| `--json`    | JSON output for scripting                | `false` |
| `--quiet`   | Suppress non-essential output            | `false` |
| `--verbose` | Debug output                             | `false` |

## Core Commands

### thrum init

Initialize Thrum in the current repository.

**On a TTY** (and unless `--non-interactive` is set), `thrum init` launches an
interactive wizard that walks you through identity, worktrees root, role
templates, and daemon start. The wizard scaffolds `.thrum/`, sets up the
`a-sync` branch, registers your initial agent, persists `Worktrees.BasePath`,
writes the role templates you choose, and starts the daemon — all in one flow.
Press enter through every prompt to accept the recommended defaults.

**On non-TTY stdin or with `--non-interactive`**, `thrum init` runs the legacy
silent path: scaffolds `.thrum/`, writes default config, no identity, no
worktrees-root, no role templates, no daemon start. Existing CI scripts that
pipe stdin to `thrum init` get this behavior unchanged.

Pre-fill any wizard prompt with the corresponding flag (`--name`, `--role`,
etc.); the wizard skips prompts whose value was supplied. With every prompt
pre-filled, the wizard runs end-to-end without user input even on a TTY.

The wizard's suggested default agent name is derived from the repo directory,
lowercased and sanitized to satisfy the agent-name validator (a-z, 0-9,
underscore only).

**G2 guard:** `thrum init` hard-errors if the current working directory is not
inside a git repository. Pass `--force` to override for non-git environments
(uncommon; the daemon cannot anchor identity without a git root).

**tmux gate:** if `tmux` is not on `PATH` when the wizard reaches the
daemon-start step, `thrum init` exits early with an OS-appropriate install hint
(`brew install tmux` on macOS, `apt install tmux` on Debian/Ubuntu). Install
tmux and re-run, or pass `--no-daemon` to skip the daemon-start step entirely.

**settings.json merge (v0.10.5+):** `thrum init` now JSON-merges
`.claude/settings.json` when it exists, rather than skipping the file entirely.
This preserves third-party hook entries (including those written by
`bd setup claude`) while injecting Thrum's own hooks alongside them. The
operation is idempotent — re-running `thrum init` produces no diff when Thrum's
entries are already present. When `bd` is on `PATH`, the beads `SessionStart`
hook is auto-installed based on detection rather than a hardcoded default.
Per-worktree redirect consistency is enforced via `worktree.EnsureRedirects` on
each init run.

This command emits contextual hints — see [CLI Hints](cli-hints.md).

```text
thrum init [flags]
```

| Flag                | Description                                                                                   | Default |
| ------------------- | --------------------------------------------------------------------------------------------- | ------- |
| `--force`           | Force reinitialization. On a TTY this re-runs the wizard with existing values pre-seeded.     | `false` |
| `--runtime`         | Specify runtime directly (skip detection prompt)                                              | (auto)  |
| `--dry-run`         | Preview changes without writing files. Bypasses the wizard regardless of TTY.                 | `false` |
| `--stealth`         | Write exclusions to `.git/info/exclude` instead of `.gitignore` (zero tracked-file footprint) | `false` |
| `--skills`          | Install thrum skill only (no MCP config, no startup script)                                   | `false` |
| `--non-interactive` | Force the legacy silent path even on a TTY                                                    | `false` |
| `--name`            | Pre-fill the wizard's identity-name prompt                                                    |         |
| `--role`            | Pre-fill the wizard's role prompt                                                             |         |
| `--module`          | Pre-fill the wizard's module prompt                                                           |         |
| `--worktrees-root`  | Pre-fill the wizard's worktrees-root prompt (must be an absolute path outside the repo)       |         |
| `--roles`           | Pre-fill the wizard's role-template choice (`enhanced` \| `default` \| `skip`)                |         |
| `--no-daemon`       | Skip auto-starting the daemon at the end of the wizard                                        | `false` |

#### Worktree base path migration (v0.10.0)

The implicit fallback for `Worktrees.BasePath` migrated from
`~/.workspaces/<project>` to `~/.thrum/worktrees/<project>`. Users with an
explicit `Worktrees.BasePath` in `.thrum/config.json` are unaffected. If you
relied on the legacy fallback and want to keep existing worktrees in place, set
the override before the next `thrum worktree create`:

```bash
thrum config set worktrees.base_path "$HOME/.workspaces/<project>"
```

The wizard also offers to set this path interactively.

Example (legacy silent path, with `--non-interactive` or piped stdin):

```text
$ thrum init --non-interactive
✓ Thrum initialized successfully
  Repository: .
  Created: .thrum/ directory structure
  Created: a-sync branch for message sync
  Updated: .gitignore
✓ Runtime saved to .thrum/config.json (primary: cli-only)

Done. Run 'thrum quickstart --name <name> --role <role> --module <module>' to register an agent.
```

The post-init `quickstart` tip lands on stdout (not the hints stream) so it
survives `THRUM_NO_HINTS` and `--quiet`. It's there to prevent the
silent-confusion case where you forget to register an agent after running
`thrum init --non-interactive`. The wizard path skips this tip because it
already ran `quickstart` for you.

#### Skills-Only Install

Use `--skills` to install just the thrum skill without full runtime
configuration. Detects your agent automatically and installs to the correct
skills directory:

```text
$ thrum init --skills
Detected: Claude Code (found .claude/settings.json)
Skill installed to .claude/skills/thrum/
  SKILL.md
  references/CLI_REFERENCE.md
  references/LISTENER_PATTERN.md
  references/MESSAGING.md
```

Supported agents: Claude Code, Cursor, Codex, Gemini, Augment, Amp. If the thrum
Claude plugin is already installed, `--skills` skips the install (use `--force`
to override). If no agent-specific directory is found, falls back to
`.agents/skills/thrum/` (the cross-agent standard path).

### thrum config show

Show effective configuration resolved from all sources. Displays each value and
where it came from (config.json, environment variable, default).

```text
thrum config show [flags]
```

| Flag     | Description             | Default |
| -------- | ----------------------- | ------- |
| `--json` | Machine-readable output | `false` |

Example:

```text
$ thrum config show
Thrum Configuration
  Config file: .thrum/config.json

Runtime
  Primary:     claude (config.json)
  Detected:    claude, augment

Daemon
  Local-only:    true (config.json)
  Sync interval: 60s (default)
  WS port:       auto (default)
  Status:        running (PID 7718)
```

### thrum setup

Set up Thrum in a feature worktree so it shares the daemon, database, and sync
state with the main repository. Creates a `.thrum/redirect` file pointing to the
main repo's `.thrum/` directory and a local `.thrum/identities/` directory for
per-worktree agent identities.

```text
thrum setup [flags]
```

| Flag          | Description                                     | Default |
| ------------- | ----------------------------------------------- | ------- |
| `--main-repo` | Path to the main repository (where daemon runs) | `.`     |

Example:

```text
$ thrum setup --main-repo /path/to/main/repo
Connected to daemon
✓ Thrum worktree setup complete
```

### thrum setup claude-md

Install or manage a Thrum-managed block in `CLAUDE.md`. Prints the template to
stdout by default; `--apply` writes to `./CLAUDE.md`; `--apply --force` replaces
an existing Thrum block idempotently.

```bash
thrum setup claude-md                   # Print template to stdout
thrum setup claude-md --apply           # Create CLAUDE.md or append block
thrum setup claude-md --apply --force   # Replace existing Thrum block
```

Flags:

- `--apply` — Write to `./CLAUDE.md`. Creates the file with template-only
  content if it doesn't exist, or appends a blank line plus the template at the
  end if it exists without a Thrum block. Errors if a Thrum block is already
  present (use `--force` to replace it).
- `--force` — Replace an existing Thrum block in place. Idempotent: re-runs
  produce the same result. Has no effect without `--apply`.

The block is wrapped in `<!-- BEGIN THRUM -->` and `<!-- END THRUM -->` markers
so the command can detect, replace, or skip it on subsequent runs. Content
outside the markers is preserved byte-for-byte.

**Use this command only if you are NOT running the Claude Code Thrum plugin.**
The plugin already provides messaging instructions via its SessionStart hook,
slash commands, and skills — adding the same content to `CLAUDE.md` would
duplicate what the plugin injects. The CLAUDE.md block is the minimal-messaging
path for Claude Code without the plugin, for other runtimes (Codex, Cursor,
opencode, kiro, auggie), or for environments where plugin install isn't
available.

Without `--apply`, the command writes nothing — it just prints the template so
you can pipe it elsewhere or inspect it. Errors that block the apply (existing
block without `--force`, IO failures) go to stderr; exit code is non-zero.

### thrum prime

Load AI-optimized session context for the current agent. Reads the identity
file, active session state, saved context, and any pending restart snapshot,
then assembles a consolidated prompt block that is printed to stdout for the
agent to consume.

```text
thrum prime [flags]
```

| Flag      | Description          | Default |
| --------- | -------------------- | ------- |
| `--quiet` | Suppress hint output | `false` |

Example:

```text
$ thrum prime
# Outputs session context block to stdout
# Agent reads this at session start via /thrum:prime skill
```

**Drift Hints:** `thrum prime` emits up to one `slog.Warn`-level hint per run
when role template state drifts from the current shipped templates. Hints appear
on stderr (or in the `hints` array with `--json`). Precedence — only the
highest-priority matching hint fires:

| Hint code                  | Meaning                                                                                    |
| -------------------------- | ------------------------------------------------------------------------------------------ |
| `roles.config.migration`   | Rendered templates exist but no `role_config` key in config.json (pre-v0.9.2 install)      |
| `roles.config.schema-bump` | Saved `schema_version` is older than the current shipped version                           |
| `roles.config.body-diff`   | Saved `rendered_hash` doesn't match current shipped `body_hash` (template content changed) |

Run `thrum roles refresh` to re-render templates from saved answers and clear
the hint.

See also: [Identity System](identity.md), [Session Restart](session-restart.md).

### thrum quickstart

Register, start a session, and set an initial intent in one step. If the agent
is already registered, it re-registers automatically. Supports agent naming via
the `--name` flag or `THRUM_NAME` environment variable.

**G1a guard (quickstart_self_rename):** If the caller's ancestor PID chain
already owns an identity in this directory (i.e., this runtime has already
registered), attempting to quickstart under a **different** name is refused —
that's a rename and would abandon the existing identity without a record. Pass
`--force` to rename the existing identity to `.deleted` and register fresh.
Same-name re-register (calling `quickstart --name <existing-name>` when you
already own `<existing-name>`) is allowed without `--force` — it's an idempotent
no-op, used by `scripts/thrum-startup.sh` on every SessionStart.

**G1b guard (quickstart_name_collision):** If the requested `--name` is held by
a live process in a different worktree, the registration is refused. Pass
`--force` to displace the existing identity (use only when you are certain the
other process has exited), or choose a different name.

```text
thrum quickstart --name AGENT_NAME --role ROLE --module MODULE [flags]
```

| Flag              | Description                                                                                                                                 | Default |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| `--name`          | Human-readable agent name (optional, defaults to `role_hash`)                                                                               |         |
| `--display`       | Display name for the agent                                                                                                                  |         |
| `--intent`        | Initial work intent                                                                                                                         |         |
| `--runtime`       | Runtime preset (`claude`, `codex`, `cursor`, `gemini`, `opencode`, `auggie`, `cli-only`)                                                    |         |
| `--preamble-file` | Path to custom preamble file                                                                                                                |         |
| `--dry-run`       | Preview without writing                                                                                                                     | `false` |
| `--no-init`       | Skip config file generation                                                                                                                 | `false` |
| `--force`         | Overwrite existing identity (bypasses G1a and G1b guards)                                                                                   | `false` |
| `--no-agent-pid`  | Persist `agent_pid=0` instead of detecting the runtime ancestor; defers PID claim to first `/thrum:prime` (used for inline tmux quickstart) | `false` |

Requires `--role` and `--module` (via flags or `THRUM_ROLE`/`THRUM_MODULE` env
vars). The `--runtime` value is written to `preferred_runtime` in the identity
file.

The `THRUM_NAME` environment variable takes priority over the `--name` flag.

Example:

```text
$ thrum quickstart --name implementer_auth --role implementer --module auth --intent "Fixing token refresh"
✓ Registered as @implementer (implementer_35HV62T9B9)
✓ Session started: ses_01HXF2A9...
✓ Intent set: Fixing token refresh

# With a human-readable name
$ thrum quickstart --name furiosa --role implementer --module auth --intent "Fixing token refresh"
✓ Registered as @furiosa (furiosa)
✓ Session started: ses_01HXF2A9...
✓ Intent set: Fixing token refresh
```

### thrum overview

Show a comprehensive orientation view combining identity, work context, team
activity, inbox counts, and sync status.

```text
thrum overview
```

Example:

```text
$ thrum overview
You: @implementer (implementer_35HV62T9B9)
Session: active for 2h15m
Intent: Fixing token refresh
Branch: feature/auth (3 commits, 5 files)

Team:
  @reviewer     feature/auth   Reviewing PR #42          15m ago
  @planner      main           Planning next sprint      1h ago

Inbox: 3 unread (12 total)
Sync: ✓ synced
```

### thrum team

Show a rich, multi-line status report for every active agent. Displays session
info, work context, inbox counts, branch status, and per-file change details.

Git context fields (branch, unmerged commits ahead, and file changes) are
populated automatically when an agent has a `worktree` session ref set — this
happens on `session start` and `quickstart`. When an agent has an active session
with a worktree ref, the daemon extracts the current branch, number of commits
ahead of the default branch, and the list of changed files on each heartbeat.

```text
thrum team [flags]
```

| Flag       | Description                           | Default |
| ---------- | ------------------------------------- | ------- |
| `--all`    | Include offline agents                | `false` |
| `--system` | Include system/reserved pseudo-agents | `false` |

The `--system` flag surfaces reserved pseudo-agents such as
`@supervisor_<project>`. Status glyphs: `●` active, `○` offline, `⊙` reserved
(system pseudo-agent).

Example:

```text
$ thrum team
@implementer [active]
  PID:      12345 [live]
  Module:   auth
  Worktree: auth-fix
  Session:  ses_01HXF2A9... (duration: 2h15m)
  Intent:   Fixing token refresh
  Inbox:    3 unread / 12 total
  Branch:   feature/auth (2 commits ahead)
  Files:
    internal/auth/token.go        5m ago   +42 -10
    internal/auth/refresh.go      5m ago   +15 -3

$ thrum team --all     # Include agents with no active session
```

The `PID` line shows the agent's runtime process ID with a liveness indicator:
`[live]` if the process is running, `[stale]` if it has exited. Agents without
an `agent_pid` skip this line. See
[PID Liveness Indicators](identity.md#pid-liveness-indicators) for details.

## Messaging

### thrum send

Send a message to the messaging system. The daemon must be running and you must
have an active session.

```text
thrum send MESSAGE [flags]
```

| Flag           | Description                                                         | Default    |
| -------------- | ------------------------------------------------------------------- | ---------- |
| `--to`         | Recipient — `@agent_name` or `@everyone` (mutex with `--broadcast`) |            |
| `--broadcast`  | Fan out to the entire team (mutex with `--to`)                      | `false`    |
| `--scope`      | Add scope (repeatable, format: `type:value`)                        |            |
| `--ref`        | Add reference (repeatable, format: `type:value`)                    |            |
| `--mention`    | Mention a role (repeatable, format: `@role`)                        |            |
| `--structured` | Structured payload (JSON string)                                    |            |
| `--format`     | Message format (`markdown`, `plain`, `json`)                        | `markdown` |

A recipient flag is **required**. `thrum send 'msg'` with no `--to` or
`--broadcast` hard-errors (exit 1) with a conversational prompt offering both
paths — the previous silent-broadcast default was a footgun (thrum-t698).
`--to @agent_name` is the canonical directed-send form (matches CLAUDE.md
convention); `--broadcast` is the explicit team-wide fanout form;
`--to @everyone` continues to work as the legacy keyword form.

This command emits contextual hints — see [CLI Hints](cli-hints.md).

Example:

```text
$ thrum send "PR ready for review" --to @reviewer --scope module:auth --ref pr:42
✓ Message sent: msg_01HXE8Z7...
  Created: 2026-02-03T10:00:00Z

# Explicit team-wide fanout (preferred form for broadcasts)
$ thrum send "Deploy complete" --broadcast
✓ Message sent: msg_01HXE8Z8...

# Legacy keyword form — still works
$ thrum send "Deploy complete" --to @everyone
✓ Message sent: msg_01HXE8Z9...
```

### thrum reply

Reply to a message by ID. Creates a reply-to reference so replies are grouped
with the original message in your inbox. The replied-to message is marked as
read. Replies automatically create or join threads. See
[Auto-Threading](messaging.md#auto-threading-v050) for details.

```text
thrum reply MSG_ID TEXT [flags]
```

| Flag       | Description                                  | Default    |
| ---------- | -------------------------------------------- | ---------- |
| `--format` | Message format (`markdown`, `plain`, `json`) | `markdown` |

Example:

```text
$ thrum reply msg_01HXE8Z7 "Good idea, let's do that"
✓ Reply sent: msg_01HXE9A3...
  In reply to: msg_01HXE8Z7
```

### thrum inbox

List messages in your inbox with filtering and pagination. Displayed messages
are automatically marked as read (unless filtering with `--unread`).

```text
thrum inbox [flags]
```

| Flag          | Description                                                             | Default |
| ------------- | ----------------------------------------------------------------------- | ------- |
| `--scope`     | Filter by scope (format: `type:value`)                                  |         |
| `--mentions`  | Only messages mentioning me                                             | `false` |
| `--from`      | Filter to messages from a specific sender (format: `@agent` or `agent`) |         |
| `--unread`    | Only unread messages                                                    | `false` |
| `--all`, `-a` | Show all messages (disable auto-filtering)                              | `false` |
| `--page-size` | Results per page                                                        | `10`    |
| `--limit N`   | Alias for `--page-size`                                                 | `10`    |
| `--page`      | Page number                                                             | `1`     |

The output adapts to terminal width and shows read/unread indicators.

Example:

```text
$ thrum inbox --unread
┌──────────────────────────────────────────────────────────┐
│ ● msg_01HXE8Z7  @planner  2m ago                       │
│ We should refactor the sync daemon before adding embeds. │
├──────────────────────────────────────────────────────────┤
│ ● msg_01HXE8A2  @reviewer  15m ago                      │
│ LGTM on the auth changes. Ready to merge.               │
└──────────────────────────────────────────────────────────┘
Showing 1-2 of 12 messages (5 unread)
```

### thrum sent

List messages you authored, including resolved recipients and durable
delivery/read state.

```text
thrum sent [flags]
```

Common examples:

```text
thrum sent
thrum sent --unread
thrum sent --to @implementer_api
thrum message get msg_01HXE8Z7
```

### thrum message get

Get a single message with full details. The message is automatically marked as
read.

```text
thrum message get MSG_ID
```

Example:

```text
$ thrum message get msg_01HXE8Z7
Message: msg_01HXE8Z7
  From:    @planner
  Time:    2m ago
  Scopes:  module:auth
  Refs:    issue:thrum-42

We should refactor the sync daemon before adding embeddings.
```

### thrum message edit

Edit a message by replacing its content entirely. Only the message author can
edit their own messages.

```text
thrum message edit MSG_ID TEXT
```

Example:

```text
$ thrum message edit msg_01HXE8Z7 "Updated: refactor sync daemon first"
✓ Message edited: msg_01HXE8Z7 (version 2)
```

### thrum message delete

Delete a message by ID. Requires the `--force` flag to confirm.

```text
thrum message delete MSG_ID --force
```

| Flag      | Description      | Default |
| --------- | ---------------- | ------- |
| `--force` | Confirm deletion | `false` |

Example:

```text
$ thrum message delete msg_01HXE8Z7 --force
✓ Message deleted: msg_01HXE8Z7
```

### thrum message read

Mark one or more messages as read, or all unread messages at once.

```text
thrum message read MSG_ID [MSG_ID...]
thrum message read --all
```

| Flag    | Description                      | Default |
| ------- | -------------------------------- | ------- |
| `--all` | Mark all unread messages as read | `false` |

Example:

```text
$ thrum message read msg_01 msg_02 msg_03
✓ Marked 3 messages as read

$ thrum message read --all
✓ Marked 7 messages as read
```

### thrum purge

Remove messages, sessions, and events before a cutoff date. By default shows a
preview of what would be deleted. Use `--confirm` to execute.

```text
thrum purge --before DURATION|DATE
thrum purge --all
```

| Flag        | Description                                            | Default |
| ----------- | ------------------------------------------------------ | ------- |
| `--before`  | Cutoff: duration (`2d`, `24h`), date, or RFC 3339      |         |
| `--all`     | Purge all messages, sessions, and events               | `false` |
| `--confirm` | Execute the purge (without this, only shows a preview) | `false` |

`--before` and `--all` are mutually exclusive. One is required.

Example:

```text
$ thrum purge --before 2d
Purge preview (before 2026-03-14T00:00:00Z):

  Messages:  142
  Sessions:   8
  Events:     47
  Sync files: 10 message files, 1 events file

Run with --confirm to execute.

$ thrum purge --before 2d --confirm
Purged (before 2026-03-14T00:00:00Z):

  Messages:  142 deleted
  Sessions:   8 deleted
  Events:     47 deleted
  Sync files: 10 message files filtered, 1 events file filtered

Done.
```

## Identity & Sessions

### Agent Naming

Agents support human-readable names that become their canonical identifier for
display, messaging (`@name`), and file paths.

**Naming rules:**

- Allowed characters: lowercase letters (`a-z`), digits (`0-9`), underscores
  (`_`)
- Reserved names: `daemon`, `system`, `thrum`, `all`, `broadcast`
- Cannot be empty

**Name resolution priority (highest to lowest):**

1. `THRUM_NAME` environment variable (highest -- used for multi-agent worktrees)
2. `--name` CLI flag
3. Scan `.thrum/identities/` for a single file (backward compat for solo-agent
   worktrees)

When no name is provided, agent IDs default to `{role}_{hash10}` format (e.g.,
`implementer_35HV62T9B9`).

### thrum agent register

Register this agent with the daemon. The agent identity is resolved from: (1)
`THRUM_NAME` env var, (2) `--name` flag, (3) environment variables
(`THRUM_ROLE`, `THRUM_MODULE`), (4) identity file in `.thrum/identities/`
directory.

```text
thrum agent register [flags]
```

| Flag            | Description                                                   | Default |
| --------------- | ------------------------------------------------------------- | ------- |
| `--name`        | Human-readable agent name (optional, defaults to `role_hash`) |         |
| `--force`       | Force registration (override existing)                        | `false` |
| `--re-register` | Re-register same agent (update)                               | `false` |
| `--display`     | Display name for the agent                                    |         |

Requires `--role` and `--module` (via global flags or env vars). On successful
registration, saves an identity file to `.thrum/identities/{name}.json`.

Example:

```text
$ thrum --role=implementer --module=auth agent register --display "Auth Developer"
✓ Agent registered: implementer_35HV62T9B9

# With a human-readable name
$ thrum --role=implementer --module=auth agent register --name furiosa --display "Auth Developer"
✓ Agent registered: furiosa
```

### thrum agent list

List all registered agents with session status and work context.

```text
thrum agent list [flags]
```

| Flag        | Description                                       | Default |
| ----------- | ------------------------------------------------- | ------- |
| `--role`    | Filter by role                                    |         |
| `--module`  | Filter by module                                  |         |
| `--context` | Show work context table (branch, commits, intent) | `false` |

Without `--context`, shows a detailed card view per agent with active/offline
status. With `--context`, shows a compact table of work contexts.

Example (default view):

```text
$ thrum agent list
Registered agents (2):

┌─ ● @implementer (active)
│  Module:  auth
│  Intent:  Fixing token refresh
│  Branch:  feature/auth (3 commits)
│  Active:  5m ago
└─

┌─ ○ @reviewer (offline)
│  Module:  auth
│  Last seen: 2h ago
└─
```

Example (context table):

```text
$ thrum agent list --context
AGENT          SESSION      BRANCH               COMMITS  FILES INTENT                         UPDATED
────────────────────────────────────────────────────────────────────────────────────────────────────────
@implementer   ses_01HXF... feature/auth               3      5 Fixing token refresh           5m ago
```

### thrum agent whoami

Show the current agent identity and active session.

```text
thrum agent whoami [flags]
```

| Flag             | Description                                                           | Default |
| ---------------- | --------------------------------------------------------------------- | ------- |
| `--field <name>` | Print a single field's value (e.g. `agent_id`, `tmux_alive`) and exit |         |

Identity is resolved from: (1) command-line flags (`--role`, `--module`), (2)
environment variables (`THRUM_ROLE`, `THRUM_MODULE`, `THRUM_NAME`), (3) identity
files in `.thrum/identities/` directory.

Example:

```text
$ thrum agent whoami
Agent ID:  implementer_35HV62T9B9
Role:      @implementer
Module:    auth
Display:   Auth Developer
Source:    environment
Session:   ses_01HXF2A9... (2h ago)

$ thrum agent whoami --field agent_id
implementer_35HV62T9B9
```

### thrum agent delete

Delete an agent and all its associated data. This removes the identity file
(`identities/{name}.json`), message file (`messages/{name}.jsonl`), and the
agent record from the database. Prompts for confirmation before deletion.

```text
thrum agent delete <name>
```

Example:

```text
$ thrum agent delete furiosa
Delete agent 'furiosa' and all associated data? [y/N] y
✓ Agent deleted: furiosa
```

### thrum agent cleanup

Detect and remove orphaned agents whose worktrees or branches no longer exist.
Scans all registered agents and identifies orphans based on missing worktree,
missing branch, or staleness (not seen in a long time).

```text
thrum agent cleanup [flags]
```

| Flag          | Description                                  | Default |
| ------------- | -------------------------------------------- | ------- |
| `--dry-run`   | List orphans without deleting                | `false` |
| `--force`     | Delete all orphans without prompting         | `false` |
| `--threshold` | Days since last seen to consider agent stale | `30`    |

The `--dry-run` and `--force` flags are mutually exclusive.

Example:

```text
$ thrum agent cleanup --dry-run
Orphaned agents (2):
  implementer_35HV... — missing worktree
  reviewer_8KBN...    — not seen in 45 days

$ thrum agent cleanup --force
✓ Deleted implementer_35HV...
✓ Deleted reviewer_8KBN...
✓ Deleted 2 orphaned agent(s)
```

### thrum agent start

Start a new work session. This is an alias for `thrum session start`. The agent
must be registered first.

```text
thrum agent start
```

### thrum agent end

End the current session. This is an alias for `thrum session end`.

```text
thrum agent end [flags]
```

| Flag           | Description                             | Default  |
| -------------- | --------------------------------------- | -------- |
| `--reason`     | End reason (`normal`, `crash`)          | `normal` |
| `--session-id` | Session ID to end (defaults to current) |          |

### thrum agent set-intent

Set the work intent for the current session. This is an alias for
`thrum session set-intent`. Pass an empty string to clear.

```text
thrum agent set-intent TEXT
```

Example:

```text
$ thrum agent set-intent "Fixing memory leak in connection pool"
✓ Intent set: Fixing memory leak in connection pool
```

### thrum agent set-task

Set the current task identifier for the session. This is an alias for
`thrum session set-task`. Pass an empty string to clear.

```text
thrum agent set-task TASK
```

Example:

```text
$ thrum agent set-task beads:thrum-42
✓ Task set: beads:thrum-42
```

### thrum agent set-status

Set the operational status of an agent. Without `--agent`, updates the local
identity file directly. With `--agent`, sends an RPC to the daemon to update a
remote agent's identity file (including across worktrees).

```text
thrum agent set-status <working|idle|blocked> [flags]
```

| Flag      | Description                                    | Default |
| --------- | ---------------------------------------------- | ------- |
| `--agent` | Target agent name (uses daemon RPC for update) |         |

Valid values: `working`, `idle`, `blocked`.

The daemon uses `agent_status` for auto-nudge detection — if a tmux agent's pane
has been silent but its status is `"working"`, the daemon fires a nudge on the
next silence event to wake the agent.

Example:

```text
$ thrum agent set-status working
✓ Status set: working

$ thrum agent set-status idle --agent implementer_api
✓ Status set: idle (agent: implementer_api)
```

### thrum agent heartbeat

Send a heartbeat for the current session. This is an alias for
`thrum session heartbeat`. Triggers git context extraction and updates the
agent's last-seen time.

```text
thrum agent heartbeat [flags]
```

| Flag             | Description                                     | Default |
| ---------------- | ----------------------------------------------- | ------- |
| `--add-scope`    | Add scope (repeatable, format: `type:value`)    |         |
| `--remove-scope` | Remove scope (repeatable, format: `type:value`) |         |
| `--add-ref`      | Add ref (repeatable, format: `type:value`)      |         |
| `--remove-ref`   | Remove ref (repeatable, format: `type:value`)   |         |

### thrum session start

Start a new work session. Automatically detects the current agent from whoami
and recovers any orphaned sessions.

```text
thrum session start
```

Example:

```text
$ thrum session start
✓ Session started: ses_01HXF2A9...
  Agent:      implementer_35HV62T9B9
  Started:    2026-02-03 10:00:00
```

### thrum session end

End the current or specified session.

```text
thrum session end [flags]
```

| Flag           | Description                             | Default  |
| -------------- | --------------------------------------- | -------- |
| `--reason`     | End reason (`normal`, `crash`)          | `normal` |
| `--session-id` | Session ID to end (defaults to current) |          |

Example:

```text
$ thrum session end
✓ Session ended: ses_01HXF2A9...
  Ended:      2026-02-03 12:00:00
  Duration:   2h
```

### thrum session list

List all sessions (active and ended) with optional filtering.

```text
thrum session list [flags]
```

| Flag       | Description               | Default |
| ---------- | ------------------------- | ------- |
| `--active` | Show only active sessions | `false` |
| `--agent`  | Filter by agent ID        |         |

Example:

```text
$ thrum session list
Sessions (3):
  ses_01HXF2A9  implementer_35HV  active  2h ago   Fixing token refresh
  ses_01HXF1B8  reviewer_8KBN     ended   4h ago   Reviewing PR #42
  ses_01HXF0C7  planner_9QRM      ended   1d ago   Sprint planning

$ thrum session list --active
Sessions (1):
  ses_01HXF2A9  implementer_35HV  active  2h ago   Fixing token refresh
```

### thrum session heartbeat

Send a heartbeat for the current session. Triggers git context extraction and
updates the agent's last-seen time. Optionally add or remove scopes and refs.

```text
thrum session heartbeat [flags]
```

| Flag             | Description                                     | Default |
| ---------------- | ----------------------------------------------- | ------- |
| `--add-scope`    | Add scope (repeatable, format: `type:value`)    |         |
| `--remove-scope` | Remove scope (repeatable, format: `type:value`) |         |
| `--add-ref`      | Add ref (repeatable, format: `type:value`)      |         |
| `--remove-ref`   | Remove ref (repeatable, format: `type:value`)   |         |

Example:

```text
$ thrum session heartbeat --add-scope module:auth
✓ Heartbeat sent: ses_01HXF2A9...
  Context: branch: feature/auth, 3 commits, 5 files
```

### thrum session set-intent

Set a free-text description of what the agent is currently working on. Appears
in `thrum agent list --context`. Pass an empty string to clear.

```text
thrum session set-intent TEXT
```

Example:

```text
$ thrum session set-intent "Refactoring login flow"
✓ Intent set: Refactoring login flow
```

### thrum session set-task

Set the current task identifier for the session (e.g., a beads issue ID).
Appears in `thrum agent list --context`. Pass an empty string to clear.

```text
thrum session set-task TASK
```

Example:

```text
$ thrum session set-task beads:thrum-42
✓ Task set: beads:thrum-42
```

## Coordination

### thrum who-has

Check which agents are currently editing a file. Shows agents with the file in
their uncommitted changes or changed files, along with branch and change count
information.

The daemon extracts live git state from each agent's worktree on every call
(about 500ms for a handful of worktrees) rather than serving stale cached data
from the last heartbeat. What you see is what's actually on disk right now.

```text
thrum who-has FILE
```

Example:

```text
$ thrum who-has internal/auth/auth.go
@implementer is editing internal/auth/auth.go (2 uncommitted changes, branch: feature/auth)

$ thrum who-has unknown.go
No agents are currently editing unknown.go
```

### thrum ping

Check the presence status of an agent. Shows whether the agent is active or
offline, along with their current intent, task, and branch if active. The agent
can be specified with or without the `@` prefix.

```text
thrum ping AGENT
```

Example:

```text
$ thrum ping @reviewer
@reviewer: active, last heartbeat 5m ago
  Intent: Reviewing PR #42
  Task: beads:thrum-55
  Branch: feature/auth

$ thrum ping planner
@planner: offline (last seen 3h ago)
```

## Context Management

### thrum context save

Save agent context from a file or stdin. Context is stored in
`.thrum/context/{agent-name}.md` for persistence across sessions.

```text
thrum context save [flags]
```

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--file`  | Path to markdown file to save as context           |         |
| `--agent` | Override agent name (defaults to current identity) |         |

Example:

```text
$ thrum context save --file dev-docs/Continuation_Prompt.md
✓ Context saved for furiosa (1234 bytes)

# Save from stdin
$ echo "Working on auth module" | thrum context save
✓ Context saved for furiosa (24 bytes)
```

**Agent safety note:** Agents should use the `/thrum:update-project` skill
instead of running `thrum context save` directly. The skill composes a
structured context (decisions, next steps, work-in-progress) before saving,
whereas running the command manually with arbitrary input can overwrite
accumulated session state.

### thrum context show

Display the saved context for the current agent.

```text
thrum context show [flags]
```

| Flag            | Description                                        | Default |
| --------------- | -------------------------------------------------- | ------- |
| `--agent`       | Override agent name (defaults to current identity) |         |
| `--raw`         | Output raw content without decoration              | `false` |
| `--no-preamble` | Output raw context without preamble markers        | `false` |

Example:

```text
$ thrum context show
Context for furiosa (1.2 KB, updated 5m ago):

# Current Work
- Implementing JWT token refresh
- Investigating rate limiting bug

# Get raw output
$ thrum context show --raw > backup.md
```

### thrum context load

Alias for `thrum context show`. Same flags, same output. Named for the common
use case: a downstream tool (a runtime's session-restore, a scripted workflow)
that "loads" the saved context back into the current session.

```text
thrum context load [flags]
```

See [thrum context show](#thrum-context-show) for the full flag table.

Example:

```bash
# In a session-restore hook: load the previous snapshot
thrum context load --raw > /tmp/restore.md
```

### thrum context clear

Remove the context file for the current agent. Idempotent — running clear when
no context exists is a no-op.

```text
thrum context clear [flags]
```

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |

Example:

```text
$ thrum context clear
✓ Context cleared for furiosa
```

### thrum context sync

Copy the context file to the a-sync branch for sharing across worktrees and
machines. This is a manual operation — context is never synced automatically.

```text
thrum context sync [flags]
```

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |

What it does:

1. Copies `.thrum/context/{agent}.md` to the sync worktree
2. Commits the change with message `"context: update {agent}"`
3. Pushes to the remote a-sync branch

No-op when no remote is configured (local-only mode) or when the `--local`
daemon flag is set.

Example:

```text
$ thrum context sync
✓ Context synced for furiosa
  Committed and pushed to a-sync branch
```

### thrum context preamble

Manage the preamble — the role-template header that prepends every
`thrum context show` / `thrum context load` output. This is what gets injected
into a session by `thrum prime` before the agent's own saved context. Preambles
live at `.thrum/context/preambles/{agent}.md` and are produced by
`thrum roles deploy` from templates under `.thrum/roles/`.

```text
thrum context preamble [flags]
```

Without flags, prints the current preamble to stdout.

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |
| `--file`  | Set preamble from a file                           |         |
| `--init`  | Create or reset to the default preamble            | `false` |

Example:

```text
# View current preamble
$ thrum context preamble
# Role: implementer
You build what you're assigned…

# Reset to the role default
$ thrum context preamble --init
✓ Preamble reset to implementer default

# Load a custom preamble from a file
$ thrum context preamble --file ./my-preamble.md
✓ Preamble updated from my-preamble.md
```

`--init` is the normal way to regenerate a preamble after a role change or a
template edit. Use `thrum roles deploy` to bulk-regenerate preambles for every
agent that matches a template.

## Notifications

### thrum wait

Block until a matching message arrives or timeout occurs. Useful for automation
and hooks. Always filters by the calling agent's identity — only returns
messages directed to this agent by name or role. There is no `--all` flag.

```text
thrum wait [flags]
```

| Flag        | Description                                                                                                                                                                  | Default |
| ----------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| `--timeout` | Max wait time — requires Go duration units (e.g., `30s`, `5m`, `1h`); bare integers like `120` are not accepted                                                              | `30s`   |
| `--after`   | Relative time offset: negative (e.g., `-30s`) = include messages sent up to N ago; positive (e.g., `+60s`) = only messages arriving at least N in the future; omit for "now" |         |
| `--mention` | Wait for mentions of role (format: `@role`)                                                                                                                                  |         |
| `--json`    | Machine-readable output                                                                                                                                                      | `false` |

Exit codes: `0` = message received, `1` = timeout, `2` = error.

**Output:** `thrum wait` does not print message content. On success it prints an
action directive telling the caller to check inbox; on timeout it prints a
timeout notice to stderr.

```text
# Plain text output (default)
$ thrum wait --after -30s --timeout=5m
ACTION REQUIRED: You have unread messages. Run `thrum inbox --unread` now to read and respond to them.

# JSON output
$ thrum wait --after -30s --timeout=5m --json
{"status": "received", "action": "ACTION REQUIRED: You have unread messages. Run `thrum inbox --unread` now to read and respond to them."}

# Use in scripts (only new messages — no --after means "now")
if thrum wait --timeout=30s; then
  thrum inbox --unread   # read the messages
else
  echo "Timeout"
fi
```

## Infrastructure

### thrum daemon start

Start the daemon in the background. Uses `thrum daemon run` internally to run
the daemon in a detached process. The daemon serves both a Unix socket (for CLI
RPC) and a combined WebSocket + SPA server on port 9999.

**G2 guard:** `thrum daemon start` hard-errors if the current working directory
is not inside a git repository. Pass `--force` to override for ephemeral non-git
environments.

```text
thrum daemon start [flags]
```

| Flag      | Description                                            | Default |
| --------- | ------------------------------------------------------ | ------- |
| `--local` | Disable remote git sync (local-only mode)              | `false` |
| `--force` | Allow start outside a git repository (G2 guard bypass) | `false` |

The daemon performs pre-startup duplicate detection by checking if another
daemon is already serving this repository (via JSON PID files and `flock()`).

Example:

```text
# Start in local-only mode (no git push/fetch)
thrum daemon start --local
```

### thrum daemon stop

Stop the daemon gracefully by sending SIGTERM.

```text
thrum daemon stop
```

### thrum daemon status

Show daemon status including PID, uptime, version, repository path, and (when
the daemon is running) the daemon identity block.

```text
thrum daemon status
```

Example:

```text
$ thrum daemon status
Daemon: running (PID 7718, uptime 3h24m)
  Version:  v0.9.0
  Socket:   .thrum/var/thrum.sock
  Repo:     /Users/leon/dev/opensource/thrum

Identity:
  daemon_id:  d_01HYTESTULID01234567890AB
  repo_name:  thrum
  hostname:   leonsmacm1pro
  repo_path:  /Users/leon/dev/opensource/thrum
  git_origin: https://github.com/leonletto/thrum
  init_at:    2026-04-17T06:30:00Z
```

### thrum daemon restart

Restart the daemon (stop + start).

```text
thrum daemon restart
```

### thrum daemon logs

View the daemon log file. By default prints the last 50 lines from
`.thrum/var/daemon.log`. Supports streaming, line limits, and time filtering.

```text
thrum daemon logs [flags]
```

| Flag             | Description                                                               | Default |
| ---------------- | ------------------------------------------------------------------------- | ------- |
| `--follow`, `-f` | Stream new log lines as they are written                                  | `false` |
| `--lines`, `-n`  | Number of lines to show (0 = all)                                         | `50`    |
| `--since`        | Only show lines at or after this time (`1h`, `7d`, `2026-04-09`, RFC3339) |         |

Example:

```text
$ thrum daemon logs
2026/04/09 21:15:03.000000 [daemon] started (v0.8.0)
2026/04/09 21:15:03.100000 [sync] loop started (interval: 60s)
...

$ thrum daemon logs -f
# Streams new lines until Ctrl+C

$ thrum daemon logs --since 1h -n 0
# All lines from the last hour

$ thrum daemon logs --since 2026-04-09
# All lines since midnight April 9
```

The daemon uses [lumberjack](https://github.com/natefinch/lumberjack) for log
rotation: 10 MB max file size, 4 rotated backups, 28-day retention, gzip
compression. The log level is controlled by the `daemon.log_level` config key
(see [Configuration](configuration.md)).

**Note:** Running `thrum sync` without a subcommand just prints help — use
`thrum sync force` or `thrum sync status` to take action.

### thrum sync status

Show sync loop status, last sync time, and any errors. When local-only mode is
active, displays "Mode: local-only" instead of "Mode: normal".

```text
thrum sync status
```

Sync states: `stopped`, `idle`, `synced`, `error`.

### thrum sync force

Trigger an immediate sync (non-blocking). Fetches new messages from the remote
and pushes local messages. The default sync interval is 60 seconds. When
local-only mode is active, displays "local-only (remote sync disabled)".

```text
thrum sync force
```

## Backup & Restore

### thrum backup

Snapshot all thrum data (events, messages, config, and identity files) to a
backup directory. By default, backups are written to `.thrum/backup/` inside the
repo. The backup directory can be overridden via `--dir` or configured in
`.thrum/config.json`.

```text
thrum backup [flags]
```

| Flag    | Description               | Default          |
| ------- | ------------------------- | ---------------- |
| `--dir` | Override backup directory | `.thrum/backup/` |

The `--dir` flag is a persistent flag inherited by all `backup` subcommands.

Example:

```text
$ thrum backup
Backup complete: .thrum/backup/my-repo/current
  Events: 1240 lines
  Message files: 3
  Local tables: 7
  Config files: 4

$ thrum backup --dir /path/to/backups
Backup complete: /path/to/backups/my-repo/current
```

### thrum backup status

Show information about the most recent backup — timestamp, counts, and archive
history.

```text
thrum backup status [--dir DIR]
```

Example:

```text
$ thrum backup status
Last backup: 2026-03-01 22:15:03
  Events: 1240 lines
  Message files: 3
  Config files: 4
Archives: 7 (12.3 MB)
  Oldest: 2026-02-01 10:00:00
  Newest: 2026-03-01 22:15:03
```

### thrum backup config

Show the effective backup configuration including directory, retention policy,
and any configured plugins.

```text
thrum backup config
```

Example:

```text
$ thrum backup config
Backup directory: .thrum/backup (default)
Retention:
  Daily: 7
  Weekly: 4
  Monthly: forever
```

### thrum backup restore

Restore thrum data from the latest backup or a specific archive zip. A safety
backup is automatically created before restoring, and the daemon is stopped
during the restore to avoid file handle conflicts.

```text
thrum backup restore [archive.zip] [flags]
```

| Flag    | Description              | Default |
| ------- | ------------------------ | ------- |
| `--yes` | Skip confirmation prompt | `false` |

Example:

```text
$ thrum backup restore
This will restore thrum data from backup.
  Backup dir: .thrum/backup
  Repo: my-repo
  Source: current/
A safety backup will be created first.
Continue? [y/N] y
Daemon stopped for restore.
Safety backup: .thrum/backup/my-repo/safety-2026-03-02T10:00:00Z.zip
Restored from: .thrum/backup/my-repo/current

# Restore from a specific archive
$ thrum backup restore .thrum/backup/my-repo/2026-02-15T10:00:00Z.zip --yes
```

### thrum backup plugin list

List all backup plugins configured in `.thrum/config.json`.

```text
thrum backup plugin list
```

### thrum backup plugin add

Add a backup plugin by name/command/include pattern, or use a built-in preset.

```text
thrum backup plugin add [flags]
```

| Flag                | Description                                 |
| ------------------- | ------------------------------------------- |
| `--preset string`   | Use built-in preset: `beads`, `beads-rust`  |
| `--name string`     | Plugin name                                 |
| `--command string`  | Command to run before collecting files      |
| `--include strings` | File patterns to collect (glob, repeatable) |

Example:

```bash
thrum backup plugin add --preset beads
thrum backup plugin add --name myplugin --command "bd backup --force" --include ".beads/backup/*.jsonl"
```

### thrum backup plugin remove

Remove a configured backup plugin by name.

```text
thrum backup plugin remove --name NAME
```

### thrum backup schedule

View or configure the automatic backup schedule. The daemon runs a backup at the
configured interval when it is running.

```text
thrum backup schedule [interval|off] [flags]
```

| Flag    | Description          | Default |
| ------- | -------------------- | ------- |
| `--dir` | Set backup directory |         |

Examples:

```text
# Show current schedule
$ thrum backup schedule

# Back up every 24 hours
$ thrum backup schedule 24h

# Set 8-hour interval and override backup directory
$ thrum backup schedule 8h --dir dev-docs/backup

# Disable scheduled backups
$ thrum backup schedule off
```

Notes:

- Daemon must be restarted for schedule changes to take effect
- Intervals use Go duration format: `24h`, `12h`, `6h30m`, `168h` (1 week)

## Role Templates

### thrum roles list

List all role templates and show which registered agents match each template.

```text
thrum roles list
```

### thrum roles deploy

Re-render agent preambles from role templates. By default deploys for all
agents. Use `--agent` to target a single agent, and `--dry-run` to preview
changes without writing files.

```text
thrum roles deploy [flags]
```

| Flag        | Description                           | Default |
| ----------- | ------------------------------------- | ------- |
| `--agent`   | Deploy for a specific agent only      |         |
| `--dry-run` | Preview changes without writing files | `false` |

Example:

```text
$ thrum roles list
Role Templates:
  implementer   → 2 agents (alice, bob)
  reviewer      → 1 agent  (carol)
  coordinator   → 1 agent  (dave)

$ thrum roles deploy --dry-run
[dry-run] Would update preamble for alice (implementer)
[dry-run] Would update preamble for bob (implementer)
[dry-run] Would update preamble for carol (reviewer)
[dry-run] Would update preamble for dave (coordinator)

$ thrum roles deploy --agent alice
✓ Deployed preamble for alice (implementer)
```

### thrum roles refresh

Re-render `.thrum/role_templates/<role>.md` for all roles that have saved
answers in `role_config`. Uses the embedded shipped templates plus the saved
`autonomy` and `scope` values — no interactive prompts. Updates `rendered_hash`
to the current shipped `body_hash` so drift hints clear on the next
`thrum prime`.

Per-agent template tokens (`{{.AgentName}}` etc.) are kept literal so the
existing per-agent deploy pass can substitute them.

```text
thrum roles refresh
```

This command takes no additional flags. Run it after upgrading Thrum to
re-render templates without re-running `/thrum:configure-roles`.

### thrum roles save-config

Write `role_config` to `.thrum/config.json` from JSON on stdin. This is the
internal CLI shim used by the `/thrum:configure-roles` skill to persist answers.
Reads a `RoleConfig` JSON object, fills in defaults (`schema_version`,
`rendered_hash`) and atomically writes the `role_config` key while preserving
all other top-level keys byte-identical.

```text
thrum roles save-config
```

Reads from stdin; exit code is non-zero on decode or write failure.

### thrum roles templates print

Print an embedded shipped role template to stdout. Used by the
`/thrum:configure-roles` skill to read reference content via CLI rather than a
raw filesystem path (the binary may run from any directory).

```text
thrum roles templates print <role>-<autonomy>
```

`<role>-<autonomy>` must match an embedded template name, e.g.
`implementer-autonomous` or `coordinator-strict`. Exit code is non-zero if the
template is not found.

Example:

```text
$ thrum roles templates print implementer-autonomous
# Agent: {{.AgentName}}
...
```

## Runtime Presets

Runtime presets tell Thrum how to launch each supported AI runtime — the binary
name, the launch command, how to detect the runtime from an existing process
tree, and pane-state pattern matching for permission-prompt detection. The
built-in presets ship with Thrum; user-defined presets live at
`~/.thrum/runtimes.json`.

Supported built-ins in v0.9.0: `claude`, `codex`, `cursor`, `gemini`, `auggie`,
`kiro-cli`, `opencode`, `cli-only`.

### thrum runtime list

List all runtime presets (built-in + user-defined) with their detection status
on the current host.

```text
thrum runtime list [--json]
```

| Flag     | Description                | Default |
| -------- | -------------------------- | ------- |
| `--json` | Emit machine-readable JSON | `false` |

Example:

```text
$ thrum runtime list
PRESET       BINARY       DETECTED   DEFAULT
claude       claude       yes        ✓
codex        codex        yes
cursor       cursor       no
opencode     opencode     yes
kiro-cli     kiro-cli     no
auggie       auggie       no
gemini       gemini       no
cli-only     (none)       n/a
```

### thrum runtime show

Show the full definition for a single preset: launch command, binary paths,
detection pattern, permission-prompt pattern, approve/deny keys.

```text
thrum runtime show <name> [--json]
```

Example:

```text
$ thrum runtime show claude
Runtime: claude
  Binary: claude
  Launch: claude
  Detect: process ancestry match on "claude"
  Permission pattern: claude.tool_confirmation
  Approve key: 1
  Deny key: 3 (Variant A) | 2 (Variant B-Bash) | Escape (other)
```

See [Permission Prompts](permission-prompts.md) for how the approve/deny keys
are used by the supervisor nudge flow, including the per-shape claude deny-key
disambiguation.

### thrum runtime set-default

Set the default runtime preset used when no `--runtime` flag is passed to
commands like `thrum tmux create` / `thrum tmux launch`. The default is
persisted to `.thrum/config.json` under `runtime.default`.

```text
thrum runtime set-default <name>
```

Example:

```text
$ thrum runtime set-default opencode
✓ Default runtime set to opencode
```

The default applies per-repo, not per-agent. Override on any individual launch
with `thrum tmux launch <session> --runtime <name>`.

See [Multi-Runtime Support](multi-runtime.md) for the runtime-resolution order
and guidance on adding a custom preset to `~/.thrum/runtimes.json`.

## Peer Management

### thrum peer add

Start a pairing session on the local machine. Displays a peercode and blocks
until the remote machine connects or the session times out (5 minutes).

> **BREAKING CHANGE (v0.9.0):** `--type` is now mandatory. The previous implicit
> `tailscale` default has been removed. Any script calling `thrum peer add`
> without `--type` must add `--type tailscale` (or another value) to restore
> equivalent behavior.

```text
thrum peer add --type TYPE [flags]
```

| Flag         | Description                                     | Required |
| ------------ | ----------------------------------------------- | -------- |
| `--type`     | Transport type (see table below)                | yes      |
| `--peercode` | Connection string (pass `-` to read from stdin) |          |
| `--address`  | LAN IP for `--type network`                     |          |

**Transport types:**

| `--type`    | When to use                                 | Required additional flags                | Constraints                                      |
| ----------- | ------------------------------------------- | ---------------------------------------- | ------------------------------------------------ |
| `tailscale` | Cross-host via Tailscale CGNAT              | `THRUM_TS_AUTHKEY` env var (or prompted) | Requires Tailscale running on both ends          |
| `local`     | Same-host, different repo or worktree       | (none)                                   | Both daemons on the same machine; loopback only  |
| `network`   | Cross-host without Tailscale                | `--address <ip>` on both sides           | No NAT traversal; requires reachable LAN address |
| `repair`    | Re-establish a broken peer (drift recovery) | Existing peer name (positional)          | Valid on `peer join` only, not `peer add`        |

If `THRUM_TS_AUTHKEY` is not set and `--type tailscale` is used, the command
prompts for a Tailscale auth key and saves it to `.thrum/.env`.

Example:

```text
$ thrum peer add --type tailscale
Waiting for connection...
Pairing code: alice:100.64.1.5:44123:7392

Share this with the other machine:
  thrum peer join --type tailscale --peercode alice:100.64.1.5:44123:7392

Paired with "bob" (100.64.1.9:44123). Syncing started.

# Same-host peer
$ thrum peer add --type local
Pairing code: alice:127.0.0.1:44123:7392
```

### thrum peer join

Connect to a remote peer using the peercode from `thrum peer add`.

> **BREAKING CHANGE (v0.9.0):** `--type` is now mandatory. See the transport
> type table above (`thrum peer add`) for valid values and when to use each.

```text
thrum peer join --type TYPE [peercode] [flags]
```

| Flag          | Description                                               | Required |
| ------------- | --------------------------------------------------------- | -------- |
| `--type`      | Transport type (see table above)                          | yes      |
| `--peercode`  | Connection string (pass `-` to read from stdin)           |          |
| `--repo-path` | Filesystem path to peer's repo (used with `--type local`) |          |

The peercode can be passed as a positional argument, via `--peercode`, piped
through stdin, or entered interactively.

**Peercode format:** `name:ip:port:code`

Example:

```text
$ thrum peer join --type tailscale --peercode alice:100.64.1.5:44123:7392
Paired with "alice". Syncing started.

# Local same-machine peer
$ thrum peer join --type local --peercode alice:127.0.0.1:44123:7392 --repo-path /path/to/other/repo

# Re-establish a broken peer (drift recovery — no new peercode needed)
$ thrum peer join --type repair alice
Re-paired with "alice". Syncing resumed.
```

### thrum peer list

List all paired peers with address, last sync time, and sequence number. When
auto-reconciliation cannot resolve a peer's address drift, an inline hint row
appears under that peer.

```text
thrum peer list [--json]
```

Example:

```text
$ thrum peer list
NAME                 ADDRESS                LAST SYNC          LAST SEQ
alice                100.64.1.5:44123       48 minutes ago     1042
  └─ drift detected — run: thrum peer join --type repair alice
bob                  100.64.1.9:44123       5 seconds ago      1087
```

The `└─` row appears only when `ReconcileStatus == "drift_reconcile_failed"`.
Running `thrum peer join --type repair alice` re-establishes the connection
using the stored bearer token in `peers.json` — no new peercode required. A
successful repair resets the status to healthy and the hint row disappears.

### thrum peer status

Show detailed per-peer health including pairing time and authentication status.

```text
thrum peer status [--json]
```

### thrum peer remove

Remove a paired peer by name. Stops syncing immediately.

```text
thrum peer remove <name>
```

### thrum peer configure

Manage proxy agents for a peer. Proxy agents are local stand-ins that route
messages to agents on the remote peer's machine.

```text
thrum peer configure <peer-name> <action> <agent-name>
```

| Argument       | Description                               |
| -------------- | ----------------------------------------- |
| `<peer-name>`  | Name of the peer (from `thrum peer list`) |
| `<action>`     | `add-agent` or `remove-agent`             |
| `<agent-name>` | Agent to add or remove as a proxy         |

Example:

```text
$ thrum peer configure alice add-agent planner
✓ alice: add-agent planner
```

## Single-Agent Mode

### thrum single-agent-mode

Toggle or query single-agent mode. When enabled, Thrum skips messaging
infrastructure (listener, inbox, stop hook) and focuses on context management.

```text
thrum single-agent-mode [true|false|on|off]
```

Without arguments, prints the current mode. Changes take effect on the next
daemon start or `thrum prime`.

Example:

```text
$ thrum single-agent-mode
single-agent mode: enabled

$ thrum single-agent-mode false
Single-agent mode disabled. Full messaging active on next thrum prime.
```

See [Single-Agent Mode](single-agent-mode.md) for details.

## Telegram

### thrum telegram configure

Configure the Telegram bridge. When all flags are provided with `--yes`, runs
non-interactively. When flags are omitted, runs in interactive mode and prompts
for each value.

```text
thrum telegram configure [flags]
```

| Flag       | Description                                                                                             | Default |
| ---------- | ------------------------------------------------------------------------------------------------------- | ------- |
| `--token`  | Telegram bot token                                                                                      |         |
| `--target` | Default agent for fresh Telegram messages (e.g. `@coordinator_main`). Replies route to original author. |         |
| `--user`   | Telegram username to associate                                                                          |         |
| `--yes`    | Skip confirmation prompt                                                                                | `false` |

Example:

```text
# Interactive mode (prompts for each value)
$ thrum telegram configure

# Non-interactive
$ thrum telegram configure --token 123456:ABC-DEF --target @mychat --user alice --yes
✓ Telegram bridge configured
```

### thrum telegram status

Show the current Telegram bridge connection status and configuration.

```text
thrum telegram status
```

Example:

```text
$ thrum telegram status
Telegram Bridge
  Status:  connected
  Bot:     @my_thrum_bot
  Target:  @mychat
  User:    alice
```

## Tmux Session Management

Manage daemon-driven tmux sessions for agents. Replaces the background listener
with instant message delivery via `send-keys`. See
[Tmux-Managed Sessions](tmux-sessions.md) for the full story.

### thrum tmux start

One-command session bring-up: runs `tmux create`, `tmux launch`, `thrum prime`,
and attaches the current terminal — all in sequence. This is the shortest path
from "I want to start an agent in this worktree" to "I'm sitting at its prompt."
If any step fails, the session is left in place so you can inspect what happened
with `thrum tmux status` and `thrum tmux capture`.

```text
thrum tmux start [flags]
```

| Flag        | Description                                                         | Default |
| ----------- | ------------------------------------------------------------------- | ------- |
| `--name`    | Override session name (default: current directory name)             |         |
| `--runtime` | Override runtime for this launch (default: from config or `claude`) |         |

`thrum tmux start` operates on the **current working directory** — it infers the
session name and agent identity from the worktree at `$PWD`. Use
[thrum tmux create](#thrum-tmux-create) when you need to target a different path
or pass agent registration flags (`--role`, `--module`, etc.).

Example:

```text
$ thrum tmux start impl-api --cwd ../worktrees/api-feature \
    --name impl_api --role implementer --module api
Session created: impl-api
Agent registered: impl_api
Runtime launched: opencode (PID 58421)
Priming session…
[attaches to session]
```

Under the hood this is equivalent to:

```bash
thrum tmux create impl-api --cwd … --name impl_api --role implementer --module api
thrum tmux launch impl-api
thrum tmux send impl-api "/thrum:prime"
thrum tmux connect impl-api
```

Use the lower-level commands directly when you need to inspect intermediate
state (e.g. verify the identity file was written before launching), or when
scripting multi-session setups where attach doesn't fit.

### thrum tmux create

Create a tmux session for an agent with a clean environment. Sets up
`monitor-silence` hooks for permission detection. Quickstart flags (`--name`,
`--role`, `--module`) are required unless you pass `--no-agent`. After
quickstart runs, any old identity files in the session's worktree are cleaned up
— one identity per worktree is enforced.

```text
thrum tmux create <name> --cwd <path> --name <agent-name> --role <role> --module <module> [flags]
```

| Flag         | Description                                                     | Default |
| ------------ | --------------------------------------------------------------- | ------- |
| `--cwd`      | Working directory for the session                               |         |
| `--name`     | Agent name (required unless `--no-agent`)                       |         |
| `--role`     | Agent role (required unless `--no-agent`)                       |         |
| `--module`   | Agent module (required unless `--no-agent`)                     |         |
| `--intent`   | Initial work intent description                                 |         |
| `--runtime`  | Runtime preset: `claude`, `codex`, `cursor`, `gemini`, `auggie` |         |
| `--no-agent` | Skip agent registration (create session only)                   | `false` |
| `--force`    | Overwrite existing runtime config files                         | `false` |

Without `--no-agent`, the command errors if `--name`, `--role`, and `--module`
are all missing.

This command emits contextual hints — see [CLI Hints](cli-hints.md).

Example:

```text
$ thrum tmux create implementer-api --cwd ../worktrees/api-feature \
    --name impl_api --role implementer --module api
Session created: implementer-api
Agent registered: impl_api

$ thrum tmux create scratch --cwd /tmp/sandbox --no-agent
Session created: scratch
```

### thrum tmux quickstart

Alias for `thrum tmux create`. Same flags, same behavior. Use whichever name
reads better in your scripts.

```text
thrum tmux quickstart <name> --cwd <path> --name <agent-name> --role <role> --module <module> [flags]
```

See [thrum tmux create](#thrum-tmux-create) for the full flag table.

### thrum tmux launch

Start an AI tool inside an existing tmux session.

```text
thrum tmux launch <name> [flags]
```

| Flag        | Description                                       | Default  |
| ----------- | ------------------------------------------------- | -------- |
| `--runtime` | AI tool to launch (`claude`, `opencode`, `shell`) | `claude` |

Example:

```text
$ thrum tmux launch implementer-api
Launched claude in session implementer-api

$ thrum tmux launch implementer-api --runtime opencode
Launched opencode in session implementer-api
```

**Hard-errors when no agent identity is registered.** `thrum tmux launch` needs
an agent identity in the target worktree to determine the runtime. If the
session was created with `--no-agent`, or if there's no identity file in the
worktree, launch returns an error and tells you to run `thrum quickstart` (or
recreate the session with `--name`/`--role`/`--module`) first. Launching a
runtime without an identity is a no-op — the agent has no way to register
itself.

### thrum tmux connect

Attach your terminal to a tmux-managed session. With no arguments, prints an
interactive picker listing every session visible to `thrum tmux status`; pass a
session name to attach directly.

```text
thrum tmux connect            # interactive picker
thrum tmux connect <name>     # attach directly
```

Example:

```text
$ thrum tmux connect
Managed sessions:
  1. coordinator-main       (coordinator_main · claude)
  2. implementer-api        (impl_api · opencode)
  3. implementer-website    (impl_website_dev · claude)
Attach to: 2
[attaches to implementer-api]

$ thrum tmux connect implementer-api
[attaches to implementer-api]
```

The picker also surfaces sessions created with `--no-agent` (they're tagged
`@thrum-managed=1` in tmux user-options so status / connect still see them
without needing a registered agent).

Under the hood this is a thin wrapper around `tmux attach-session -t <name>`, so
all the usual tmux attach semantics apply (Ctrl-b d to detach, etc.).

### thrum tmux status

Show all tmux-managed sessions with agent info, liveness state, runtime, and
branch. Includes sessions created with `--no-agent` (they're tagged
`@thrum-managed=1` as a tmux user-option and still show up here, with an empty
agent column).

```text
thrum tmux status
```

`thrum tmux list` is an alias.

Example:

```text
$ thrum tmux status
SESSION                   AGENT                STATE        RUNTIME    BRANCH
coordinator-main          coordinator_main     alive        claude     thrum-dev
implementer-api           impl_api             alive        opencode   feature/api
implementer-website-dev   impl_website_dev     stale        claude     website-dev
```

### thrum tmux kill

Tear down a tmux session and clear `tmux_session` from the agent's identity
file.

```text
thrum tmux kill <name>
```

Example:

```text
$ thrum tmux kill implementer-api
Session implementer-api killed
```

### thrum tmux send

Send text into a tmux session via `send-keys`. Useful for coordinator debugging
or injecting commands.

For agent-managed sessions (the normal case), input is routed through the
daemon's command queue so `@system` completion semantics and `thrum tmux queue`
coordination stay intact. For sessions created with `--no-agent`, there is no
agent identity to queue against — `tmux send` bypasses the queue and writes the
keystrokes directly into the pane.

```text
thrum tmux send <name> "text"
```

Example:

```text
thrum tmux send implementer-api "thrum inbox --unread"
```

### thrum tmux capture

Capture the visible content of a tmux pane. Useful for coordinator inspection or
permission prompt detection.

```text
thrum tmux capture <name> [flags]
```

| Flag      | Description                | Default |
| --------- | -------------------------- | ------- |
| `--lines` | Number of lines to capture | `50`    |

Example:

```text
thrum tmux capture implementer-api --lines 10
```

### thrum tmux restart

Restart a tmux-managed agent session with a context snapshot. By default, the
daemon asks the agent to save its own snapshot (graceful flow), falling back to
JSONL extraction only on timeout. With `--force`, the daemon skips the graceful
prompt and extracts directly from the JSONL transcript. Either way, the session
is killed, a new one is created, and the new session loads the snapshot via
`thrum prime`. See [Session Restart](session-restart.md) for details on the
graceful vs force flows.

```text
thrum tmux restart <name> [flags]
```

| Flag        | Description                                                     | Default |
| ----------- | --------------------------------------------------------------- | ------- |
| `--force`   | Skip graceful save prompt, extract snapshot from JSONL directly | `false` |
| `--runtime` | Runtime override for relaunch (default: same)                   |         |

Example:

```text
$ thrum tmux restart implementer-api
Session implementer-api restarted (847 snapshot lines)

$ thrum tmux restart implementer-api --runtime opencode
Session implementer-api restarted (847 snapshot lines)
```

### thrum tmux queue

Submit a command to a tmux session's queue. Commands are dispatched FIFO — one
at a time per session. The daemon sends the command when the pane goes silent,
waits for completion (detected by the next silence event), and captures the
output.

```text
thrum tmux queue <session> <command> [flags]
```

| Flag        | Type    | Default | Description                                                   |
| ----------- | ------- | ------- | ------------------------------------------------------------- |
| `--timeout` | `int`   | `120`   | Command timeout in seconds                                    |
| `--wait`    | `bool`  | `false` | Block until the command reaches a terminal state              |
| `--silence` | `float` | `0`     | Silence threshold in seconds (server default: 5.0 if omitted) |

Without `--wait`, the command is enqueued and the CLI returns immediately. The
daemon sends an `@system` inbox message to the requester when the command
completes, times out, or is cancelled. With `--wait`, the CLI blocks until the
command finishes and `@system` notifications are suppressed (you get the output
directly).

Example:

```text
$ thrum tmux queue implementer-api "git status"
Queued cmd_01KNTF2A9... (position 1)

$ thrum tmux queue implementer-api "make test" --wait --timeout 300
State: completed
Elapsed: 45200ms

ok   github.com/user/repo/... 45.1s
```

### thrum tmux queue-status

Show the command queue for a tmux session — the active command (if any) and all
queued commands waiting to run.

```text
thrum tmux queue-status <session>
```

Example:

```text
$ thrum tmux queue-status implementer-api
Active: cmd_01KNTF2A9 "git status" (sent)
Queued: 2 commands
  [0] cmd_01KNTF3B1 "make test"
  [1] cmd_01KNTF4C2 "make lint"
```

### thrum tmux cancel

Cancel a queued or active command by its command ID.

```text
thrum tmux cancel <command-id>
```

Example:

```text
$ thrum tmux cancel cmd_01KNTF2A9
Canceled cmd_01KNTF2A9 (state: cancelled)
```

## Worktree Management

### thrum worktree create

Create a git worktree with Thrum and Beads setup. Sets up `.thrum/redirect` and
`.thrum/identities/` so the new worktree shares the daemon with the main repo.
Optionally sets up `.beads/redirect` if Beads is enabled.

Quickstart flags (`--name`, `--role`, `--module`) are optional. When all three
are provided, the command creates a real tmux session with the worktree as `cwd`
and runs `thrum quickstart` inside the pane via SendKeys (PID-isolated). The
daemon retries quickstart at 5s if the shell init swallows the first attempt,
and the CLI captures pane content if the identity file still hasn't appeared
after 12s. Old identity files in the worktree are cleaned up after quickstart
runs — one identity per worktree is enforced.

The agent runtime is **not** started by `worktree create` — the output prints
the next-step `thrum tmux launch <name>` command. The agent isn't running until
you run that command.

**Repo-root guard:** the command errors out if the resolved worktree path or
`worktrees.base_path` is the repo root itself, to prevent accidentally turning
the main worktree into a "feature" worktree.

```text
thrum worktree create <name> [flags]
```

| Flag             | Description                                                                 | Default          |
| ---------------- | --------------------------------------------------------------------------- | ---------------- |
| `--branch`, `-b` | Branch name                                                                 | `feature/<name>` |
| `--detach`       | Create detached HEAD worktree                                               | `false`          |
| `--name`         | Agent name (triggers quickstart when combined with role+module)             |                  |
| `--role`         | Agent role                                                                  |                  |
| `--module`       | Agent module                                                                |                  |
| `--intent`       | Initial work intent description                                             |                  |
| `--runtime`      | Runtime preset: `claude`, `codex`, `cursor`, `gemini`, `opencode`, `auggie` |                  |
| `--detach`       | Create detached HEAD worktree                                               | `false`          |

The worktree is created at `worktrees.base_path/<name>` (default:
`~/.workspaces/<project>/<name>`). The name cannot contain `/`, `\`, or `..`.

Hook scripts (`scripts/thrum-startup.sh`, `scripts/thrum-check-inbox.sh`) are
copied from the main repo into the new worktree so SessionStart hooks fire
correctly.

Example:

```text
$ thrum worktree create api-feature
Worktree created: ~/.workspaces/thrum/api-feature
  Branch: feature/api-feature
  Thrum: .thrum/redirect → /path/to/main/.thrum
  Beads: .beads/redirect → /path/to/main/.beads

$ thrum worktree create hotfix -b fix/urgent-bug
Worktree created: ~/.workspaces/thrum/hotfix
  Branch: fix/urgent-bug

$ thrum worktree create auth-feature --name impl_auth --role implementer --module auth
Worktree created: ~/.workspaces/thrum/auth-feature
  Branch: feature/auth-feature
  Thrum: .thrum/redirect → /path/to/main/.thrum
  Beads: .beads/redirect → /path/to/main/.beads
✓ Session created: auth-feature
✓ Registered @impl_auth in worktree
  Agent is NOT running yet. Start it with:
    thrum tmux launch auth-feature [--runtime <runtime>]
```

### thrum worktree setup

Alias for `thrum worktree create`. Same flags, same behavior.

```text
thrum worktree setup <name> [flags]
```

See [thrum worktree create](#thrum-worktree-create) for the full flag table.

### thrum worktree teardown

Remove a worktree and clean up Thrum artifacts (identity files).

```text
thrum worktree teardown <name> [flags]
```

| Flag              | Description                                                                        | Default |
| ----------------- | ---------------------------------------------------------------------------------- | ------- |
| `--delete-branch` | Delete the worktree's branch after removing the worktree (branch stays by default) | `false` |

Example:

```text
$ thrum worktree teardown api-feature
✓ Cleaned up 1 identity file(s)
✓ Worktree removed: ~/.workspaces/thrum/api-feature
```

### thrum worktree list

List git worktrees with Thrum agent info. Reads identity files from each
worktree to show which agent is active there.

```text
thrum worktree list
```

Example:

```text
$ thrum worktree list
WORKTREE                            BRANCH              HEAD       AGENT                STATUS
/path/to/thrum                      thrum-dev           b3c6352    coordinator_main     working
/home/user/.workspaces/thrum/api    feature/api         a1b2c3d    impl_api             idle
/home/user/.workspaces/thrum/web    website-dev         21908a3    impl_website_dev     -
```

## Monitor Jobs

Run a long-lived command, filter its output through a regex, and deliver
matching lines as Thrum messages to an agent. Jobs persist across daemon
restarts. Max 100 concurrent jobs.

The command must come after `--`:

```text
thrum monitor start --name <name> --match <regex> --to @<agent> [flags] -- <command> [args...]
```

### thrum monitor start

Start a monitor job. (`add` is a retained alias.)

| Flag         | Description                                           | Default | Required |
| ------------ | ----------------------------------------------------- | ------- | -------- |
| `--name`     | Unique monitor name                                   |         | yes      |
| `--match`    | Regex pattern — lines that match trigger a message    |         | yes      |
| `--to`       | Target agent (`@agent_name` or `@everyone`)           |         | yes      |
| `--debounce` | Leading-edge debounce window (minimum 30s)            | `60s`   |          |
| `--env`      | Environment variable in `KEY=VALUE` form (repeatable) |         |          |
| `--cwd`      | Working directory for the command                     | `.`     |          |

Debounce is leading-edge: the first matching line triggers a message
immediately, then the monitor goes quiet for the debounce window before it can
fire again. Lines longer than 2KB are truncated.

Example:

```text
$ thrum monitor start --name app-errors --match "ERROR|FATAL" --to @coordinator_main \
    --debounce 120s -- tail -F /var/log/app.log
Started monitor app-errors (mon_01KNTF2A9...) — target @coordinator_main

$ thrum monitor start --name ci-failures --match "FAIL" --to @impl_ci \
    --env CI_ENV=staging -- ./scripts/run-tests.sh
Started monitor ci-failures (mon_01KNTG3B1...) — target @impl_ci
```

### thrum monitor list

List monitor jobs. Shows running jobs by default.

```text
thrum monitor list [--all]
```

| Flag    | Description                                           | Default |
| ------- | ----------------------------------------------------- | ------- |
| `--all` | Include stopped/dead monitors (younger than one week) | `false` |

Output columns: `ID`, `NAME`, `STATUS`, `TARGET`, `UPTIME`, `PID`.

Example:

```text
$ thrum monitor list
ID                           NAME         STATUS     TARGET         UPTIME     PID
mon_01KNTF2A9...             app-errors   running    @coordinator   3h42m      18421
mon_01KNTG3B1...             ci-failures  running    @team          15m30s     19033
```

### thrum monitor show

Show full details for a monitor job. Env var values are always redacted.

```text
thrum monitor show <id|name>
```

Example:

```text
$ thrum monitor show mon_01KNTF2A9
ID:       mon_01KNTF2A9...
Name:     app-errors
Status:   running
Match:    ERROR|FATAL
Target:   @coordinator
Cwd:      /srv/app
Debounce: 2m0s
Argv:     tail -F /var/log/app.log
Created:  2026-04-10T09:00:00Z
Env:
  CI_ENV=<redacted>
```

### thrum monitor stop

Stop a running monitor job. Sends SIGTERM, waits 5 seconds, then sends SIGKILL
if still running.

```text
thrum monitor stop <id|name>
```

Example:

```text
$ thrum monitor stop mon_01KNTF2A9
Stopped monitor mon_01KNTF2A9...
```

### thrum monitor logs

Show the most recent matched output lines for a monitor job (historical lookup
from the messages table, not live tail).

```text
thrum monitor logs <id|name> [flags]
```

| Flag            | Description                     | Default |
| --------------- | ------------------------------- | ------- |
| `-n`, `--limit` | Max number of matches to return | `20`    |

Output is oldest-first (reads like a normal log tail).

Example:

```text
$ thrum monitor logs mon_01KNTF2A9 -n 5
2026-04-10T09:03:12Z  ERROR: connection timeout on db-primary
2026-04-10T09:15:44Z  ERROR: retry limit exceeded for job 98312
2026-04-10T10:02:01Z  FATAL: out of memory, shutting down
```

### thrum monitor restart

Restart a stopped or dead monitor job. Returns a new monitor ID.

```text
thrum monitor restart <id|name>
```

Example:

```text
$ thrum monitor restart mon_01KNTF2A9
Restarted — new ID: mon_01KNTH4C2...
```

## Session Restart

Save and restore conversation snapshots for session restart. See
[Session Restart & Context Recovery](session-restart.md) for the full story.

### thrum tmux snapshot save

Save a conversation snapshot for the current agent. Extracts user + assistant
text from the Claude Code JSONL transcript, truncates to the configured line
limit, and writes to `.thrum/restart/<agent>.md`.

```text
thrum tmux snapshot save [flags]
```

| Flag       | Description                                                            | Default          |
| ---------- | ---------------------------------------------------------------------- | ---------------- |
| `--reason` | Reason for restart (`self-initiated`, `external`, `context-threshold`) | `self-initiated` |

Example:

```text
$ thrum tmux snapshot save
Restart snapshot saved for impl_api (847 lines)

$ thrum tmux snapshot save --reason context-threshold
Restart snapshot saved for impl_api (847 lines)
```

### thrum tmux snapshot restore

Output a restart snapshot to stdout and delete the file. Manual escape hatch for
non-tmux agents or when `thrum prime` is not used.

```text
thrum tmux snapshot restore
```

Exits with code 1 if no snapshot exists.

### thrum tmux snapshot check

Check if a restart snapshot exists for the current agent. Exits 0 if yes, 1 if
no. No stdout — for scripting.

```text
thrum tmux snapshot check
```

Example:

```text
if thrum tmux snapshot check; then echo "Snapshot ready"; fi
```

## MCP Server

### thrum mcp serve

Start an MCP (Model Context Protocol) server on stdin/stdout for native
tool-based agent messaging. This allows Claude Code agents to communicate via
MCP tools instead of shelling out to the CLI.

```text
thrum mcp serve [flags]
```

| Flag         | Description                                                       | Default |
| ------------ | ----------------------------------------------------------------- | ------- |
| `--agent-id` | Override agent identity (selects `.thrum/identities/{name}.json`) |         |

Requires the Thrum daemon to be running. The `--agent-id` flag sets `THRUM_NAME`
internally for identity resolution.

**MCP Tools provided (4 total):**

| Tool               | Description                                                      |
| ------------------ | ---------------------------------------------------------------- |
| `send_message`     | Send a message to another agent via `@role` addressing           |
| `check_messages`   | Poll for unread messages mentioning this agent (auto-marks read) |
| `wait_for_message` | Block until a message arrives (WebSocket push) or timeout        |
| `list_agents`      | List registered agents with active/offline status                |

**Configuration in Claude Code's `.claude/settings.json`:**

```json
{
  "mcpServers": {
    "thrum": {
      "type": "stdio",
      "command": "thrum",
      "args": ["mcp", "serve"]
    }
  }
}
```

For multi-agent worktrees, use `--agent-id` or set `THRUM_NAME`:

```json
{
  "mcpServers": {
    "thrum": {
      "type": "stdio",
      "command": "thrum",
      "args": ["mcp", "serve", "--agent-id", "furiosa"]
    }
  }
}
```

## Environment Variables

| Variable        | Description                                           | Example                      |
| --------------- | ----------------------------------------------------- | ---------------------------- |
| `THRUM_NAME`    | Agent name (highest priority for identity resolution) | `furiosa`                    |
| `THRUM_ROLE`    | Agent role (overrides identity file)                  | `implementer`                |
| `THRUM_MODULE`  | Agent module (overrides identity file)                | `auth`                       |
| `THRUM_DISPLAY` | Display name (overrides identity file)                | `Auth Developer`             |
| `THRUM_WS_PORT` | WebSocket and SPA server port (daemon)                | `9999`                       |
| `THRUM_UI_DEV`  | Path to dev UI dist for hot reload (daemon)           | `./ui/packages/web-app/dist` |
| `THRUM_LOCAL`   | Enable local-only mode (disables remote sync)         | `1`                          |

## Identity Resolution

Identity is resolved using the following priority (highest to lowest):

1. `THRUM_NAME` environment variable (selects specific identity file)
2. `--name` CLI flag
3. Environment variables (`THRUM_ROLE`, `THRUM_MODULE`)
4. CLI flags (`--role`, `--module`)
5. Identity files in `.thrum/identities/` directory (auto-selects if exactly one
   file exists)

For multi-agent worktrees with multiple identity files, set `THRUM_NAME` to
select the correct one.

## Configuration Files

### .thrum/identities/{name}.json

Per-agent identity files stored in the `.thrum/identities/` directory. Created
automatically on successful registration. The filename is derived from the agent
name (e.g., `furiosa.json` or `implementer_35HV62T9B9.json`).

```json
{
  "version": 5,
  "repo_id": "r_0123456789AB",
  "agent": {
    "kind": "agent",
    "name": "furiosa",
    "role": "implementer",
    "module": "auth",
    "display": "Auth Developer"
  },
  "worktree": "main",
  "tmux_session": "implementer-auth:0.0",
  "runtime": "claude",
  "preferred_runtime": "claude",
  "agent_pid": 12345,
  "agent_status": "working",
  "agent_status_updated_at": "2026-02-03T10:05:00Z",
  "confirmed_by": "",
  "updated_at": "2026-02-03T10:00:00Z"
}
```

---

_The section below covers storage internals. You don't need it for normal use._

### Storage Layout

Messages and events are stored on the `a-sync` Git branch in a worktree at
`.git/thrum-sync/a-sync/`:

| Path                                            | Description                                          |
| ----------------------------------------------- | ---------------------------------------------------- |
| `.git/thrum-sync/a-sync/events.jsonl`           | Agent lifecycle events (register, session start/end) |
| `.git/thrum-sync/a-sync/messages/{agent}.jsonl` | Per-agent sharded message files                      |
| `.thrum/var/messages.db`                        | SQLite projection cache (derived from JSONL)         |
| `.thrum/identities/{name}.json`                 | Per-worktree agent identity files                    |
| `.thrum/var/thrum.sock`                         | Unix socket for CLI-daemon RPC                       |
| `.thrum/var/thrum.pid`                          | JSON PID file with daemon metadata                   |
| `.thrum/var/ws.port`                            | WebSocket port file                                  |
| `.thrum/var/daemon.log`                         | Daemon log file (lumberjack-rotated)                 |
| `.thrum/var/thrum.lock`                         | flock() lock file for SIGKILL resilience             |
| `.thrum/redirect`                               | Redirect pointer for feature worktrees               |

## Next Steps

- [Messaging](messaging.md) — how send, inbox, and reply work together
- [RPC API Reference](rpc-api.md) — the underlying JSON-RPC methods the CLI
  wraps
- [Quickstart Guide](quickstart.md) — get up and running in 5 minutes
- [Overview](overview.md) — which 8 commands you actually need day-to-day
