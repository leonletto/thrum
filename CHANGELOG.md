# Changelog

All notable changes to Thrum will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.7] - 2026-03-15

### Fixed

- **Web UI agent deletion** — Register `agent.delete` and `agent.cleanup` on the
  WebSocket registry so the web UI can call them (previously returned "Method
  not found")
- **Agent delete cleanup** — `HandleDelete` now removes orphaned sessions,
  session child tables (refs, scopes), and events from SQLite; also filters
  agent lifecycle events from `events.jsonl` via new `jsonl.RemoveByField()`
  helper to prevent re-projection on daemon restart

### Added

- **a-sync worktree protection** — PreToolUse hook (`block-sync-worktree-cd.sh`)
  prevents `cd`/`pushd` into `.git/thrum-sync/a-sync/` and blocks
  branch-changing git operations (`checkout`, `switch`, `reset`, `merge`,
  `rebase`, `pull`) via `git -C` targeting the sync worktree. Checking out a
  different branch there destroys the entire `.git` directory.

## [0.5.6] - 2026-03-14

### Agent Detection & Skills Installer

New data-driven agent registry with 3-tier detection (environment variables,
config files, binary verification) replaces hardcoded runtime checks.
`thrum init --skills` installs agent-agnostic Thrum skills without full runtime
setup — useful for multi-agent environments where agents just need messaging
commands.

### Added

- **3-tier agent detection** — registry-driven detection via environment
  variables (tier 1), config files (tier 2), and binary verification (tier 3)
- **Data-driven agent registry** — built-in definitions for Claude Code, Codex,
  Aider, and other runtimes; `SupportedRuntimes` derived from registry
- **`thrum init --skills`** — lightweight skill installation with agent-aware
  path resolution; detects existing plugin before installing
- **Embedded skill content** — agent-agnostic Thrum skill shipped inside the
  binary for install without network access
- **Explicit mark-as-read (UI)** — messages require explicit interaction to mark
  as read; `thrum inbox --unread` no longer marks messages as read
- **Action directive protocol** — `thrum wait` outputs structured action
  directives instead of raw message content; stop hook uses directives too
- **Hybrid message reliability** — stop hook + listener heartbeat file ensures
  no messages are missed between listener re-arms

### Fixed

- **12 E2E test failures** resolved; `THRUM_HOME` cleared in global-setup for
  test isolation
- **UI identity mismatch** — `for_agent` identity used for `is_read` and
  mark-read; message list query invalidation added to `useMarkAsRead`
- **Listener hardening** — standardized timeout to 8m, widened `--after` window
  from -1s to -15s, fixed heartbeat step skipping on Haiku, prevented listener
  from acting on ACTION REQUIRED directives
- **Daemon shutdown** — force-close active connections on shutdown
- **Preamble** recreated when deleted; DefaultPreamble test assertion updated
- **Inbox unread count** aligned with `for_agent` filter

### Changed

- **README** rewritten to match website voice; SVG architecture diagram added
- **Branding** — removed "git-backed" from identity language; CLI positioned as
  primary, MCP as optional
- **Documentation site** improved for human readers; quickstart simplified;
  install methods consolidated

## [0.5.5] - 2026-03-09

### Improved Agent Safety & Toolkit

Default preamble now warns agents against running `thrum context save` manually
(which destroys accumulated session state). Role templates updated with
learnings from a 31-task multi-agent session: mandatory sub-agent delegation,
CAN/CANNOT scope boundaries, background listener pattern, and `thrum sent`
integration.

### Changed

- **DefaultPreamble** — "Save context" line now directs agents to use
  `/thrum:update-context` skill instead of manual `thrum context save`
- **Role templates (all 8)** — added context save warning, background message
  listener pattern, `thrum sent --unread`, SendMessage tool warning, fixed idle
  behavior to use listener instead of direct `thrum wait`
- **Coordinator templates** — added CAN/CANNOT authority lists, strategy file
  references
- **Implementer templates** — added CAN/CANNOT scope lists, mandatory sub-agent
  delegation, 5-step task protocol (strict variant)
- **Planner/Researcher templates** — added exploration-focused strategy
  references
- **project-setup skill** — now self-contained in plugin with
  `resources/implementation-agent.md` and `resources/philosophy-template.md`;
  added beads prerequisite check with correct install instructions
  (`steveyegge/beads/bd`)
- **Beads setup guide** — rewritten for Dolt backend (v0.59.0+), correct repo
  attribution (steveyegge/beads), dolt prerequisite, sync setup, common errors
- **Beads UI setup guide** — updated for Dolt backend, added worktree support
  and sandbox mode sections
- **Context docs** — added agent safety note to `thrum context save` in CLI and
  context documentation

## [0.5.4] - 2026-03-09

### Sent Command & Durable Deliveries

New `thrum sent` command lets agents review messages they sent and see which
recipients have read them. Message delivery is now durable — every `send`
records recipient snapshots in SQLite, and `mark-read` updates durable read
receipts. The send response now shows exactly who the message was delivered to,
eliminating guesswork about routing.

### Added

- **`thrum sent`** — list messages you sent with recipient read status
- **`thrum sent --unread`** — filter to messages with unread recipients
- **`thrum sent --to @agent`** — filter by recipient or audience
- **`thrum sent show MSG_ID`** — full recipient detail for one message
- **Durable message deliveries** — `message_deliveries` table tracks every
  recipient with `delivered_at` and `read_at` timestamps
- **Send confirmation** — `SendResponse` now includes `audiences` and
  `recipients` fields showing resolved delivery targets
- **`thrum sent --unread`** in DefaultPreamble, strategies, and prime output

### Fixed

- **`thrum wait`** now wakes for direct reply mentions and group messages where
  the agent is a member
- **Wait filters** aligned with inbox delivery rules for consistent behavior
- **Startup script** properly quotes values in `CLAUDE_ENV_FILE` heredoc

## [0.5.3] - 2026-03-06

### Scheduled Backups

The daemon can now run automatic backups on a configurable interval. Configure
via CLI (`thrum backup schedule 24h`) or `.thrum/config.json`. Backups include
all thrum data plus third-party plugins (e.g., Beads) in a single archive with
GFS rotation.

### Pinned Agent Identity for Worktrees

Agents working in worktrees no longer silently drift to the daemon's default
identity. Three new environment variables (`THRUM_HOME`, `THRUM_AGENT_ID`,
`THRUM_NAME`) pin CLI commands to a specific repository and agent, even when the
shell cds into a different worktree. The startup script and daemon both respect
these pins.

### Added

- **Scheduled automatic backups** — `thrum backup schedule [interval|off]` with
  `--dir` flag; daemon runs a `BackupScheduler` goroutine at the configured
  interval
- **Embedded strategy files** — three strategy reference files (sub-agent,
  registration, resume-after-context-loss) embedded in the binary and written to
  `.thrum/strategies/` during `thrum init`
- **Strategy read-directives** in `DefaultPreamble` — agents are pointed to
  `.thrum/strategies/` for operational patterns
- **`CLAUDE_ENV_FILE` integration** — startup script persists `THRUM_HOME`,
  `THRUM_AGENT_ID`, and other env vars into Claude Code's session environment
  for SessionStart hooks
- **Strategies documentation** — new website category with three strategy pages

### Changed

- **Startup script** (`scripts/thrum-startup.sh` and template) now exports
  `THRUM_HOME`, `THRUM_AGENT_ID`, and binds all thrum commands to the home repo
  via `--repo "$THRUM_HOME"`
- **`runDaemon()`** creates identities directory in the local checkout instead
  of the shared redirect target, matching the `IdentitiesDir()` contract
- **`DefaultPreamble`** prioritizes `@name` over `@role` for send instructions,
  preventing accidental group fanout
- **`resolveLocalAgentID()`** checks `THRUM_AGENT_ID` env var before config file
  lookup; errors now surface with a helpful registration hint

### Fixed

- **Variable shadowing in `prime.go`** — `whoami` inside an `if` block was
  shadowed by `:=`, causing `ctx.Session` to never populate; `thrum prime`
  always showed "Session: none"
- **Identity drift in `status`, `overview`, `prime`, and subscriptions** — these
  commands now pass `caller_agent_id` to the daemon instead of relying on
  daemon-side resolution
- **`DefaultSocketPath`** applies `EffectiveRepoPath` before redirect resolution
  so worktree agents connect to the correct daemon
- **`thrum init --force`** now refreshes the preamble, pre-populates identity
  fields, and fixes role conflict on re-init
- **Preamble `--after` flag** corrected from `-30s` to `-1s` (prevents stale
  message replay)

## [0.5.0] - 2026-02-23

### Big Update to the UI

The web dashboard has been rebuilt from scratch as a Slack-style 3-panel
interface. Full documentation:
**[Web UI Guide](https://leonletto.github.io/thrum/docs.html#web-ui.html)**

### Added

- **Web UI overhaul** — Slack-style interface with sidebar navigation, Live
  Feed, My Inbox, Group Channels, Agent Inbox, Who Has?, and Settings views
- **Live Feed** with real-time activity stream and three filter modes (All,
  Messages Only, Errors)
- **Group Channels** with member management, create/delete dialogs, and
  channel-scoped messaging
- **Agent Inbox** with context panel showing intent, branch, session info, and
  impersonation view
- **Who Has?** file coordination tool — search which agent is editing a file
- **Settings view** with daemon health, theme toggle (Dark/Light/System),
  keyboard shortcuts, and notification preferences
- **Keyboard shortcuts** — `1`–`5` for views, `Cmd+K` for search, `Esc` to
  dismiss
- **ComposeBar** with `@mention` autocomplete for agents and groups
- **Unread badges** on sidebar groups and agent entries
- **Message deep-linking** from Live Feed to inbox conversations
- **Pagination** in InboxView and GroupChannelView
- **Agent delete dialog** with archive option and type-to-confirm
- **Group delete dialog** with archive option
- **Role-based preamble templates** — auto-applied on agent registration via
  `.thrum/role_templates/`
- **Project setup skill** — converts plan files into beads epics, tasks, and
  worktrees
- **Web UI documentation page** with 7 annotated screenshots

### Added (RPC)

- `message.deleteByAgent` — clean up messages when removing an agent
- `message.deleteByScope` — scoped message deletion
- `message.archive` — export-then-delete for message cleanup
- `group.delete` with `delete_messages` parameter

### Changed

- Dashboard rebuilt as single-page app with hash-based routing
- Sidebar restructured: Live Feed → My Inbox → Groups → Agents → Tools
- Message list uses conversation grouping instead of flat chronological display

### Fixed

- Auth guards on all protected views
- Polling interval consistency across components
- Pagination race conditions in inbox and channel views
- Agent name tooltips for truncated names
- Dead code and unused hooks removed
- Identity fallback for "Unknown" inbox heading
- Port file cleanup on daemon shutdown
- Group member validation on add
- Startup identity persistence

## [0.4.5] - 2026-02-21

### Added

- **Agent context management**: Per-agent context storage with CLI detection.
  `thrum context save/show` persists session state across compaction.
- **`thrum init` full setup**: Single command does prompt, daemon start,
  register, session creation, and intent setting.
- **Identity v3 enrichment**: Identity files now carry branch, intent, and
  session_id. `quickstart` populates these fields automatically.
- **AgentSummary canonical output model**: Unified JSON/human output for agent
  status across `whoami`, `status`, and `overview` commands.
- **safedb package**: Compile-time context enforcement for all SQLite operations
  with connection limits and WAL sync mode.
- **safecmd package**: Context-aware git commands with 5s/10s timeouts replacing
  raw `exec.Command` calls.
- **Resilience test suite**: 32 tests covering RPC, CLI, concurrency, crash
  recovery, and multi-daemon scenarios.
- **`setup claude-md` subcommands**: Generate CLAUDE.md files from Go templates.
- **Pre-compact hook**: Identity-scoped backups via `${CLAUDE_PLUGIN_ROOT}`
  script bundled in plugin.

### Changed

- **Name-only routing**: Messages route by agent name and group membership only.
  Role strings are no longer used for direct inbox matching. Role-based
  addressing (`@implementer`) now works through auto-created role groups that
  are visible in `thrum group list` and manageable via `thrum group member`.
- **Agent name ≠ role**: Registration rejects agents whose name matches their
  role (e.g., `name=implementer role=implementer`). Use distinct names like
  `impl_api` or `impl_db`.
- **`thrum wait` always filters by agent identity**: The `--all` flag has been
  removed. Wait now returns only messages addressed to the calling agent (direct
  mentions, group messages, broadcasts).
- **Recipient validation**: Sending to an unknown agent, role, or group now
  returns a hard error listing the unresolvable addresses. The message is not
  stored — fix the address and resend.
- `status` and `overview` use `FormatAgentSummary` for consistent agent output.
- `team` output shows worktree and host as separate fields.
- `agent list` shows branch and intent for offline agents in context view.
- Go 1.26 required (fixes govulncheck panic on json/v2 variadic types).

### Fixed

- MCP `check_messages` now sees name-directed messages, broadcasts, and group
  messages (previously only role-based mentions were matched).
- MCP send and broadcast include `CallerAgentID` so messages are attributed to
  the correct sender in multi-worktree setups.
- MCP mark-read records read-state under the correct agent identity.
- MCP `check_messages` excludes the agent's own sent messages.
- Replies now include the original sender in the audience so they route back
  correctly.
- Reply to group messages uses `@groupname` instead of the malformed
  `@group:groupname` format.
- MCP waiter subscribes to `@everyone` group scope so broadcasts trigger
  WebSocket push notifications.
- `list_agents` shows the agent ID when display name is empty.
- Daemon deadlock prevention with SQLite and socket timeouts.
- SQLite WAL accumulation with connection limit and sync mode.
- Server per-request timeout reduced from 30s to 10s.
- RPC dial timeout added (5s), RPC call timeout reduced to 10s.
- WebSocket handshake timeout added (10s) for MCP waiter.
- Sync notify goroutines capped to 10 with semaphore.
- Context propagation through pairing wait path.
- All git commands migrated to safecmd with enforced timeouts.
- All DB call sites migrated to context-aware safedb.
- Lock scope reduced in 5 high-risk RPC handlers.

## [0.4.4] - 2026-02-18

### Added

- `thrum init --stealth`: writes exclusions to `.git/info/exclude` instead of
  `.gitignore`, leaving zero footprint in tracked files.
- `--limit N` alias for `--page-size N` on `thrum inbox`.
- `--everyone` flag on `thrum send` (alias for `--to @everyone`).
- Plugin ships `agents/message-listener.md` for auto-discovery by Claude Code.
- `make deploy-remote REMOTE=host` for scp + codesign to remote macOS machines.

### Changed

- `thrum init` defaults to `local_only: true` — remote git sync requires
  explicit opt-in via `local_only: false` in config.
- `thrum prime` listener instruction upgraded from soft tip to
  `⚠ ACTION REQUIRED:` directive.

### Fixed

- `--broadcast` is now a proper alias for `--to @everyone` (not deprecated).
- Plugin install docs corrected to two-step marketplace flow.
- `thrum setup claude-md` added to README Essential Commands table.
- Defensive test for duplicate thrum section headers in CLAUDE.md.
- Clarifying comment on separator edge case in `replaceThrumSection()`.

## [0.4.3] - 2026-02-17

### Changed

- Init is local-only by default — remote git sync must be explicitly enabled via
  `local_only: false` in `.thrum/config.json`

### Fixed

- Internal git commits in the a-sync worktree now skip pre-commit hooks
  (`--no-verify`) to avoid failures from project-level hooks
- Daemon, CLI client, and MCP server can no longer hang indefinitely. All I/O
  paths now enforce timeouts: 5s CLI dial, 10s RPC calls, 10s server
  per-request, 10s WebSocket handshake, 5s/10s git commands, and context-scoped
  SQLite queries. Lock scopes reduced in high-risk handlers so no mutex is held
  during file I/O, git operations, or WebSocket dispatch.
- Subscription cleanup on session end — orphaned subscriptions from crashed
  clients are now deleted when `session.end` fires (thrum-620c)
- Subscription commands (`thrum subscriptions`) now resolve caller identity
  correctly by passing `caller_agent_id` through the RPC layer (thrum-efjv)
- Context propagation in subscription handlers changed from
  `context.Background()` to request context for proper cancellation
- Template rendering test expectations aligned with identity-reuse design

### Added

- Crash recovery tests: kill-during-write, DB integrity after abrupt shutdown,
  daemon restart, projection rebuild from JSONL
- Negative path tests: send to non-existent agent, malformed JSON-RPC requests
  (6 cases), connection drops mid-request, mixed valid/invalid concurrent load
- Timeout enforcement tests verifying 10s per-request handler deadline and
  concurrent request isolation
- Goroutine leak detection helper (`checkGoroutineLeaks`) added to concurrent
  resilience tests
- E2E agent-cleanup tests: agent delete removes all artifacts (identity,
  messages, events), delete non-existent agent returns error, cleanup emits
  event in events.jsonl, `--force` and `--dry-run` mutual exclusion (thrum-6xjs,
  thrum-mfiv, thrum-x29q, thrum-i2fe)
- E2E init tests updated to match sharded file layout (`events.jsonl` +
  `messages/` directory) (thrum-lwls, thrum-xlig)
- `thrum setup claude-md` command generates recommended CLAUDE.md content for
  thrum-enabled repos. Prints to stdout by default; `--apply` appends to
  existing CLAUDE.md with duplicate detection; `--force` replaces existing
  section (thrum-rimg)
- `thrum setup` restructured with `worktree` and `claude-md` subcommands
  (backwards compatible — bare `thrum setup` still works)
- `thrum prime` shows a tip to run `thrum setup claude-md --apply` when
  CLAUDE.md lacks a Thrum section

### Changed (Infrastructure)

- Resilience test infrastructure refactored: shared fixture extraction via
  `TestMain` (extracts once, copies per-test), atomic JSON-RPC request IDs to
  prevent race conditions
- Race detection (`-race`) enabled in resilience test script
- CLI roundtrip test helper `runThrum` now enforces 30s context timeout to
  prevent hangs
- Benchmark `BenchmarkConcurrentSend10` protected with `select`-based deadlock
  timeout

## [0.4.2] - 2026-02-14

### Added

- Apple Developer ID codesigning and notarization for macOS release binaries
- CI/CD signs darwin binaries during GoReleaser build and submits to Apple for
  Gatekeeper approval

## [0.4.1] - 2026-02-14

### Fixed

- Context prime identity resolution in worktrees and unread hint
- 6 bugs closed (thrum-pwaa, thrum-16lv, thrum-pgoc, thrum-5611, thrum-en2c,
  thrum-8ws1): daemon accept loop race condition, gofmt formatting,
  golangci-lint errors, macOS quarantine attribute in install script

### Changed

- Documentation audit: updated 9 files across website docs, llms.txt, and plugin
  files to reflect v0.4.1 changes

## [0.4.0] - 2026-02-13

### Added

#### Agent Groups

Named groups for organizing agents and targeting messages. Groups are flat
collections of agents and roles.

- `thrum group create|delete|add|remove|list|info|members` CLI commands
- Auto-detection of member type (`@alice` = agent, `--role` = role)
- `@everyone` built-in group auto-created on daemon startup
- Group-scoped messaging via `thrum send --to @groupname`
- 6 new MCP tools: `create_group`, `delete_group`, `add_group_member`,
  `remove_group_member`, `list_groups`, `get_group`
- `get_group` supports `expand=true` to resolve roles to agent IDs

#### Reply-to Messages

Simple message threading via parent references, replacing the thread system.

- `thrum reply MSG_ID` creates a `reply_to` reference on the new message
- Replies copy audience (mentions) from parent message
- Inbox clusters replies under parent messages with `↳` prefix
- `send_message` MCP tool accepts optional `reply_to` parameter

#### Tailscale Peer Sync

Cross-machine event synchronization over Tailscale's encrypted mesh network.

- Human-mediated pairing: `thrum peer add` generates 4-digit code,
  `thrum peer join <address>` connects
- 3-layer security: Tailscale WireGuard encryption + pairing code + per-peer
  token auth
- Event-sourced sync with sequence-based checkpoints and deduplication
- Periodic sync scheduler with configurable intervals
- 5 CLI commands: `thrum peer add|join|list|remove|status`
- Peer registry with persistence to `.thrum/var/peers.json`
- Supports both Tailscale SaaS and self-hosted Headscale control planes

#### Runtime Preset Registry

Multi-runtime support for AI coding agents with auto-detection and config
generation.

- Auto-detection for 6 platforms: Claude Code, Codex, Cursor, Gemini, Auggie,
  CLI-only
- `thrum runtime list|show|set-default` CLI commands
- `thrum init --runtime <name>` generates runtime-specific config files (MCP
  settings, hooks, instructions)
- Embedded templates for each runtime with shared startup script
- File marker detection (`.claude/settings.json`, `.codex`, `.cursorrules`,
  `.augment`) with env var fallback

#### Configuration Consolidation

`.thrum/config.json` as single source of truth for all settings.

- `thrum config show` displays effective configuration resolved from all sources
  with provenance (config.json, env, default, auto-detected). Supports `--json`.
- `thrum init` interactively prompts for runtime selection (non-interactive
  fallback for CI)
- Daemon reads `sync_interval` and `ws_port` from config.json
- `ws_port: "auto"` finds a free port dynamically
- Priority chain: CLI flags > env vars > config.json > defaults

#### Team Command

Rich per-agent status for all active agents.

- `thrum team` shows session, git branch, intent, inbox counts, and per-file
  change details with diff stats for every active agent
- Per-agent inbox shows directed messages; shared messages in footer section
- `--all` flag includes offline agents, `--json` for machine-readable output
- `THRUM_HOSTNAME` env var for friendly machine names
- Hostname tracking on agent registration (schema v11)

#### Context Prime

- `thrum context prime` gathers identity, session, agents, inbox, and git work
  context in a single command for agent initialization
- Graceful degradation when daemon, session, or git are unavailable
- Both human-readable and `--json` output

#### Enhanced Quickstart & Worktree Bootstrap

- `thrum quickstart` gains `--runtime`, `--dry-run`, `--no-init`, `--force`,
  `--preamble-file` flags
- Auto-detects runtime and generates config files during quickstart
- Auto-creates context file and default preamble on first registration
- `scripts/setup-worktree-thrum.sh` enhanced with `--identity`, `--role`,
  `--module`, `--preamble`, `--base` flags for single-command worktree bootstrap

#### Additional Commands

- `thrum whoami` — display current agent identity without daemon connection
- `thrum version` — version info with hyperlinks to repo and docs

### Changed

- **`thrum send --broadcast`** deprecated, maps to `--to @everyone` with notice
- **`broadcast_message` MCP tool** simplified to send via `@everyone` group
- **`thrum status`/`overview`** scope inbox counts to agent's actual messages
  and resolve local worktree identity correctly
- **`thrum who-has`** shows detailed file change info (+additions/-deletions,
  status, time ago)
- **Website** — added light/dark theme toggle with full light-mode CSS

### Removed

- **Thread system** — `thrum thread create|list|show` commands, `thread.create`,
  `thread.list`, `thread.get` RPC handlers, and `threads` table all removed.
  Replaced by reply-to references and groups.

### Infrastructure

- **Schema**: 5 migrations (v7→v12) — added `groups`, `group_members`, `events`,
  `sync_checkpoints` tables; added `file_changes` and `hostname` columns;
  dropped `threads` table
- **Dependencies**: Tailscale SDK v1.94.1, `golang.org/x/term` v0.38.0
- **Tests**: +39 test files (~6,000 lines added, ~1,200 removed)
- **New packages**: `internal/groups`, `internal/runtime`,
  `internal/daemon/checkpoint`, `internal/daemon/eventlog`

### Documentation

- 5 new guides: multi-agent coordination, Tailscale sync, Tailscale security,
  configuration, and design philosophy
- CLI progressive disclosure: 4-tier command organization (daily drivers,
  agent-oriented, setup/admin, aliases)
- All thread and nested-group references removed across 18+ docs
- Toolkit templates restructured into `agent-dev-workflow/` directory

## [0.3.1] - 2026-02-11

### Added

#### Context Preamble

Per-agent preamble support — a stable, user-editable header prepended when
showing context. Preambles persist across `thrum context save` operations,
acting as a persistent system prompt that survives session resets.

- `thrum context show` gains `--raw` and `--no-preamble` flags
- `thrum context load` alias for `thrum context show`
- New `thrum context preamble` subcommand with `--init` and `--file` flags
- Default preamble with thrum quick reference auto-created on first context save
- `context.preamble.show` and `context.preamble.save` RPC methods
- `thrum agent delete` now cleans up preamble files

#### Test Suite Quality Audit

Comprehensive cleanup of 140 test quality issues across Go, UI, and E2E suites.
All changes are test-only — zero production code modified.

- Replaced `time.Sleep` calls with proper synchronization (ready channels,
  socket polling)
- Fixed broken tests: signal handling, type assertions, error handling,
  incomplete stubs
- Strengthened UI tests: missing assertions, test spy conflicts, TypeScript
  errors, un-skipped disabled tests
- Replaced hardcoded sleeps with polling-based waits in E2E specs

## [0.3.0] - 2026-02-11

### Added

#### Agent Context Management

Per-agent context storage for persisting volatile project state across sessions.
Agents can save, view, clear, and sync markdown context files tied to their
identity.

- `thrum context save|show|clear|sync|update` CLI commands
- `context.save`, `context.show`, `context.clear` RPC methods
- `thrum status` shows context file size and age when context exists
- `thrum agent delete` removes agent context files
- `/update-context` Claude Code skill for guided context saving
- Context sync to a-sync branch for cross-machine sharing

### Fixed

- `thrum wait` no longer calls vestigial subscribe/unsubscribe RPCs that caused
  identity resolution failures in multi-agent worktrees. Subscription calls
  removed; `mention_role` filtering moved into the message list poll where it
  takes effect.

## [0.2.0] - 2026-02-10

### Added

#### Identity Resolution & Wait Command

Complete overhaul of agent identity resolution for multi-worktree repositories,
plus efficient blocking-based message listening.

- **Most-recent-wins auto-selection**: When multiple identity files exist for a
  worktree, automatically selects the one with the latest timestamp. Eliminates
  "cannot auto-select identity" errors.
- **Worktree identity guard**: Running from a worktree with no registered
  identities now returns a clear error instead of falling through to the main
  repo's identities.
- `thrum whoami` command displays current agent identity without daemon
  connection (lightweight alternative to `thrum status`)
- `thrum wait --all` subscribes to all messages (broadcasts + directed)
- `thrum wait --after` filters by relative time (e.g., `--after -30s`)
- Message-listener agent rewrite: replaced polling with blocking wait, reducing
  API calls from 120 to 12 for ~30 minute coverage

### Changed

- `resolveLocalAgentID` now returns errors; all 17 CLI call sites fail early
  with a "register first" message
- Inbox auto-filters by worktree identity, preventing message leakage across
  worktrees

### Fixed

- Formatting alignment in wait.go

## [0.1.0] - Earlier Development

### Added

#### Core Infrastructure

- Event-sourced messaging with JSONL append-only log and SQLite projection
- Message scopes, references (tags, mentions), threads, and edit history
- Agent registration, identity management, and session lifecycle
- Unix socket JSON-RPC server with handler registry and batch support
- Git-based message synchronization with conflict resolution
- Event subscription system with filtering and notification dispatch
- Test suite with >70% coverage
