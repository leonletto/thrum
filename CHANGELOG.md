# Changelog

All notable changes to Thrum will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

### Changed

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
