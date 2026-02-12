# Changelog

All notable changes to Thrum will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.2] - Unreleased

### Added

#### Agent Groups

Named groups for organizing agents and targeting messages. Groups support nested
membership (groups containing other groups) with automatic cycle detection.

- `thrum group create|delete|add|remove|list|info|members` CLI commands
- Auto-detection of member type (`@alice` = agent, `--role` = role, `--group` =
  group)
- `@everyone` built-in group auto-created on daemon startup
- Recursive group resolution via SQL recursive CTE for inbox queries
- Nested group support with cycle detection
- JSON output via `--json` flag on all group commands
- Schema migration v7 to v8 with `groups` and `group_members` tables

#### MCP Group Tools

Six new MCP tools for group management in native agent workflows.

- `create_group`, `delete_group`, `add_group_member`, `remove_group_member`,
  `list_groups`, `get_group`
- `get_group` supports `expand=true` to resolve nested groups and roles to agent
  IDs

#### Runtime Preset Registry

Multi-runtime support for any AI coding agent. Thrum auto-detects or accepts
explicit runtime selection, generating the correct configuration files for each
platform.

- `thrum runtime list|show|set-default` CLI commands for managing runtime presets
- 6 built-in presets: Claude Code, Codex, Cursor, Gemini, Auggie, Amp
- User config override via `~/.config/thrum/runtimes.json` (XDG-aware)
- `thrum init --runtime <name>` generates runtime-specific config files
  (settings.json, AGENTS.md, .cursorrules, etc.)
- Template engine with embedded templates for each runtime
- Runtime auto-detection from file markers and environment variables

#### Enhanced Quickstart

- `thrum quickstart` gains `--runtime`, `--dry-run`, `--no-init`, `--force` flags
- Auto-detects runtime and generates config files during quickstart
- Dry-run mode previews generated files without daemon connection

#### Worktree Bootstrap

One-command worktree setup with full agent context bootstrapping.

- `thrum quickstart --preamble-file` flag composes default preamble with custom
  project-specific content
- `thrum quickstart` auto-creates empty context file and default preamble on
  first registration (idempotent — preserves existing preambles)
- `scripts/setup-worktree-thrum.sh` enhanced with `--identity`, `--role`,
  `--module`, `--preamble`, `--base` flags for full single-command bootstrap
- Flag value validation with helpful error messages on missing values
- Error handling on `git worktree add` with contextual failure messages
- 7 new Go tests for context bootstrapping, 13-case shell test harness
- Three-layer context model: prompt (session) → preamble (persistent) → context
  (volatile)
- `toolkit/templates/` restructured into `agent-dev-workflow/` template set

#### Context Prime

- `thrum context prime` gathers identity, session, agents, inbox, and git work
  context in a single command
- Graceful degradation when daemon, session, or git are unavailable
- Both human-readable and `--json` output

#### Team Command

Rich per-agent status for all active agents, powered by a single server-side
SQL JOIN query.

- `thrum team` shows `thrum status`-like detail for every active agent
- Per-agent display: location, session duration, intent, inbox counts, branch,
  and per-file change details with diff stats
- `--all` flag includes offline agents
- `--json` flag for machine-readable output
- Schema migration v11: hostname tracking on agent registration
- `THRUM_HOSTNAME` env var override for friendly machine names
- `team.list` RPC handler with two-query architecture (agents+contexts, inbox
  counts)

#### Tailscale Sync

Cross-machine event synchronization over Tailscale's encrypted mesh network.
Daemons discover peers automatically and sync events in real-time via push
notifications with a periodic fallback.

- tsnet listener for encrypted peer-to-peer sync on port 9100
- `sync.pull` batched event pulling with sequence-based checkpoints and dedup
- `sync.notify` push notifications with per-peer debouncing (fire-and-forget)
- Periodic sync scheduler (5-minute fallback for missed notifications)
- Automatic peer discovery via Tailscale API filtering by `tag:thrum-daemon`
- Peer registry with persistence to `.thrum/var/peers.json`
- `thrum daemon sync`, `thrum daemon peers list|add` CLI commands
- Tailscale sync status in `thrum status` health endpoint
- Supports both Tailscale SaaS and self-hosted Headscale control planes

#### Tailscale Sync Security

Defense-in-depth security for the sync protocol with five layers of protection.

- Ed25519 event signing with canonical payload format
- Three-stage validation pipeline (schema, signature, business logic)
- Tailscale WhoIs authorization (hostname, ACL tags, domain checks)
- Per-peer token bucket rate limiting (configurable RPS, burst, queue depth)
- Quarantine system for invalid events with alert thresholds
- TOFU (trust-on-first-use) key pinning for peer public keys
- `THRUM_SECURITY_ALLOWED_PEERS`, `THRUM_SECURITY_REQUIRED_TAGS`,
  `THRUM_SECURITY_ALLOWED_DOMAIN`, `THRUM_SECURITY_REQUIRE_AUTH`,
  `THRUM_SECURITY_REQUIRE_SIGNATURES` environment variables

### Changed

- `--broadcast` flag on `thrum send` now maps to `--to @everyone` with a
  deprecation notice
- `broadcast_message` MCP tool simplified to send via `@everyone` group
- Website: added light/dark theme toggle and full light-mode support

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
