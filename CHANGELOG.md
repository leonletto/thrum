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

### Changed

- `--broadcast` flag on `thrum send` now maps to `--to @everyone` with a
  deprecation notice
- `broadcast_message` MCP tool simplified to send via `@everyone` group

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
