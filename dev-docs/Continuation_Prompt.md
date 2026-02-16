# Thrum Project Continuation Prompt

## Agent Working Style

**Use auggie-mcp (Code Retrieval)**

Use `auggie-mcp codebase-retrieval` for exploring existing code. It's
context-efficient — prefer it over reading files manually when you need to
understand how existing code works, find patterns, or explore unfamiliar
packages.

**Use sub-agents (Task tool) for all research and exploration.** Delegate
codebase exploration, file reading, and pattern analysis to sub-agents so the
main context stays clean for decision-making and coordination.

- **Research/exploration**: Use `Task` tool with `subagent_type=Explore` or
  `subagent_type=general-purpose`
- **Planning**: Use `Task` tool with `subagent_type=Plan`
- **Code review**: Use `Task` tool with
  `subagent_type=feature-dev:code-reviewer` or
  `subagent_type=superpowers:code-reviewer`
- **Direct reads**: Only for short config files, issue tracking, or when you
  already know the exact 10-line snippet needed.

**You are the coordinator, not the implementer.** Use the template process in
`dev-docs/templates/` to plan features and create implementation prompts. Agents
are dispatched to worktrees with filled-in prompts from `dev-docs/prompts/`.

## On Session Start (MANDATORY)

**Always do these three things immediately when starting a new session:**

1. **Check Thrum** — Register, check inbox, and see who's online:

```bash
thrum quickstart --name claude_planner --role planner --module coordination --intent "Session N: <description>"
thrum inbox --unread
thrum agent list --context
```

**Verify registration succeeded** — you must see your agent name in the output
of `thrum status`. If it fails, check that the daemon is running with
`thrum daemon status`.

**Use the message listener** — Spawn a background listener to get async
notifications. Re-arm it every time it returns (both MESSAGES_RECEIVED and
NO_MESSAGES_TIMEOUT).


## Quick Resume

```bash
# Check project status (beads — local only, not in git)
bd ready
bd stats

# Check git state
git status
git --no-pager log --oneline -10

# Build, test, install (full build with UI)
make install

# Run tests
go test ./... -race

# Run E2E tests (daemon must be running)
thrum daemon start
npx playwright test --workers=1
```

## Local-Only Development Tooling

After the public release squash, the following are **gitignored and local only**.
They need to be re-initialized on a fresh clone or new machine:

| Tool | Setup | Purpose |
|------|-------|---------|
| **Beads** | `bd init` then `bd import < output/beads-full-export.jsonl` | Issue tracker (102 open issues) |
| **Thrum** | `thrum init && thrum daemon start` | Agent messaging coordination |
| **CLAUDE.md** | Restore from backup or recreate | Agent instructions |
| **AGENTS.md** | Restore from backup or recreate | Beads onboarding for agents |
| `.claude/agents/` | Restore from backup | Agent definitions (thrum-agent, message-listener, beads-agent) |
| `.claude/commands/` | Restore from backup | Custom skills (update-context) |

**Beads config**: `.beads/config.yaml` has `no-push: true` — beads works locally
without pushing the sync branch to the public remote.

**Gitignored paths**: `dev-docs/`, `.beads/`, `.claude/`, `CLAUDE.md`, `AGENTS.md`,
`.agents/`, `.ref/`, `output/`

**Backups** (in gitignored directories):
- `output/beads-full-export.jsonl` — Full export of all issues
- `dev-docs/open-issues-backlog.md` — Human-readable open issues with full descriptions

## Worktree Setup (as needed)

Recreate worktrees as needed for feature work:

```bash
# Create a feature worktree
git worktree add ~/.workspaces/thrum/<name> -b feature/<name>

# Set up thrum + beads redirects (share daemon and issue tracker with main repo)
scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/<name>
scripts/setup-worktree-beads.sh ~/.workspaces/thrum/<name>

# Or run without args to auto-detect and set up all worktrees:
scripts/setup-worktree-thrum.sh
scripts/setup-worktree-beads.sh
```

## Development Workflow (Templates)

Feature work follows a three-phase template process:

```
1. PLAN (dev-docs/templates/planning-agent.md)
   → Brainstorm → design spec → beads epics + tasks

2. PREPARE (dev-docs/templates/worktree-setup.md)
   → Create/select worktree → beads redirect → verify

3. IMPLEMENT (dev-docs/templates/implementation-agent.md)
   → Fill in template → save to dev-docs/prompts/ → dispatch agent to worktree
```

**Active prompts** in `dev-docs/prompts/`:
- None currently (config-consolidation implemented and merged)

---

## Session Context

**Date**: 2026-02-16 (Session 38)
**Project**: Thrum — Git-backed messaging for AI agent coordination
**Repository**: https://github.com/leonletto/thrum
**Visibility**: Public
**Main Branch**: `main` — multiple unpushed commits (caaf4f3)
**Go Version**: 1.25.7
**Install**: `make install` (builds UI + Go binary → ~/.local/bin)
**CI**: GitHub Actions green (format, vet, golangci-lint v2, test -race, govulncheck, build)
**Latest Release**: v0.4.2 (published), v0.4.3 in progress
**Version String**: `v0.4.3 (build: 68c74c9, go1.25.7)`
**Install Script**: `curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh`

---

## Project Status Overview

### What is Thrum?

Thrum is an offline-first, Git-backed messaging system for multi-agent
coordination in code repositories. It enables AI agents (and humans) to
communicate persistently across sessions, worktrees, and machines using Git as
the sync layer.

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         Thrum                                │
├─────────────────────────────────────────────────────────────┤
│  CLI (thrum)     │  Daemon (single port)  │  Web UI (React) │
│  - send/inbox    │  - Unix socket (RPC)   │  - Embedded SPA  │
│  - agent/session │  - WebSocket at /ws    │  - Live feed     │
│  - message ops   │  - SPA at / (embedded) │  - Inbox view    │
│  - reply         │  - Sync loop (60s)     │  - Agent list    │
│  - coordination  │  - Heartbeat/context   │  - Data Terminal │
│  - user.identify │  - Auto-registration   │  - AuthProvider  │
│  - agent delete  │  - Agent cleanup       │  - Delete action │
│  - agent cleanup │  - Orphan detection    │                   │
│  - context cmds  │  - Context management  │                   │
│  - groups        │  - Agent groups        │                   │
│  - runtime       │  - Runtime presets     │                   │
│  - config        │  - Config show/manage  │                   │
│  - mcp serve     │                        │                   │
│  - team          │                        │                   │
├─────────────────────────────────────────────────────────────┤
│  MCP Server (thrum mcp serve)                               │
│    - stdio transport (JSON-RPC over stdin/stdout)            │
│    - Tools: send_message, check_messages, wait_for_message   │
│             list_agents, create_group, add_group_member      │
│             remove_group_member, list_groups, get_group      │
│    - WebSocket waiter for real-time push notifications       │
│    - Per-call daemon RPC client (thread-safe)                │
│    - Identity from .thrum/identities/{name}.json             │
├─────────────────────────────────────────────────────────────┤
│  Storage: Sharded JSONL → SQLite (projection)               │
│    - .git/thrum-sync/a-sync/events.jsonl (agent lifecycle)   │
│    - .git/thrum-sync/a-sync/messages/{agent}.jsonl (msgs)    │
│    - .thrum/identities/{name}.json (per-worktree identity)   │
│    - .thrum/context/{agent}.json (per-agent context files)   │
│    - .thrum/var/messages.db (SQLite projection cache)        │
│    - .thrum/config.json (runtime, daemon, sync settings)     │
│  Sync: Git a-sync branch worktree at .git/thrum-sync/a-sync │
│    - Sparse checkout (events.jsonl + messages/ only)         │
│    - Orphan branch via git plumbing (safe, no checkout)      │
│    - .thrum/ fully gitignored on main                        │
│  Path Resolution: .thrum/redirect for multi-worktree         │
│  Dedup: ULID event_id (globally unique, lexicographic sort)  │
│  Embed: //go:embed React SPA into Go binary (single port)   │
└─────────────────────────────────────────────────────────────┘
```

### Key Stats

| Category                         |                                              Count |
| -------------------------------- | -------------------------------------------------: |
| Total Go Files                   |                                              1,520 |
| Go Source                         |                                        913 files (non-test) |
| Go Tests                          |                                       607 test files |
| Go Test Packages                  |                      27 total (all passing with -race) |
| UI Tests                          |         332 passing (web-app) + 87 (shared-logic) |
| E2E Tests                         | 70 scenarios, 55 passing, 0 failing, 15 skip/fixme |

### Deployment Status

- Binary: `~/.local/bin/thrum`
- Daemon: Running
- WebSocket: `ws://localhost:9999/ws`
- UI: `http://localhost:9999` (embedded SPA via `make install`)
- Website: https://leonletto.github.io/thrum (GitHub Pages, auto-deploy)
- Identity: `.thrum/identities/{name}.json` — worktree-aware auto-selection
- Sync: 60s default interval (configurable via `--sync-interval`)
- MCP Server: `thrum mcp serve` (stdio transport, group tools, WebSocket waiter)
- CI: GitHub Actions green (`ci.yml`)
- Release: v0.4.2 published via GoReleaser (`release.yml`), 4 platform archives + install script, Apple codesigned

### CLI Commands (Full Suite)

```
thrum init              Initialize Thrum in a repository
thrum setup             Configure feature worktrees with .thrum/redirect
thrum migrate           Migrate old-layout repos to worktree architecture
thrum daemon            Manage the Thrum daemon (start/stop/status)
thrum quickstart        Register + start session + set intent in one step
                        Supports --name for human-readable agent names
                        Supports --runtime to detect and configure AI runtime
thrum overview          Combined status, team, inbox, and sync view
thrum team              Team roster with per-agent inbox counts

# Identity & Sessions
thrum agent             Manage agent identity (register, list, start/end aliases)
thrum agent delete      Delete an agent (removes identity, messages, SQLite record)
thrum agent cleanup     Detect and remove orphaned agents (--force, --dry-run)
thrum session           Manage sessions (start, end, list, heartbeat, set-intent/set-task)
thrum status            Show current agent status with work context

# Agent Context
thrum context save      Save agent work context (--message, auto-detect CLI vs MCP)
thrum context show      Display current work context for an agent
thrum context clear     Clear context for an agent or all agents (--all)
thrum context prime     Generate LLM preamble from git history and beads context

# Messaging
thrum send              Send a message (--to @name, --group @groupname)
thrum inbox             List messages (auto-filters to you, --all for everything)
thrum reply             Reply to a message (creates reply-to reference)
thrum message           Manage messages (get, edit, delete, read)

# Agent Groups
thrum group             Manage agent groups (create, add, remove, list, delete)
                        Groups replace broadcast for targeted multi-agent comms

# Coordination
thrum who-has           Check which agents are editing a file
thrum ping              Check if an agent is online (active/offline + intent)

# Notifications
thrum subscribe         Subscribe to notifications
thrum subscriptions     List active subscriptions
thrum unsubscribe       Unsubscribe from notifications
thrum wait              Wait for notifications (for hooks)
                        Supports --all (broadcasts), --after (time filter)

# Sync
thrum sync              Control sync operations

# Runtime Presets
thrum runtime           Manage runtime presets (list, show, edit)
                        Built-in support for Claude Code, Augment, Cursor, Windsurf

# Config Management
thrum config            Manage configuration (show, interactive init)
                        Single source of truth: .thrum/config.json
                        Runtime tiers, daemon settings, sync intervals

# MCP Server
thrum mcp serve         Start MCP stdio server for native agent messaging
                        --agent-id NAME to override identity
```

### Agent Naming System

Agents support human-readable names:

- `thrum quickstart --name furiosa --role implementer --module auth`
- Names: `[a-z0-9_]+` (lowercase alphanumeric + underscores)
- Reserved: `daemon`, `system`, `thrum`, `all`, `broadcast`
- Env vars: `THRUM_NAME` (highest priority), `THRUM_ROLE`, `THRUM_MODULE`
- Multi-agent per worktree: each agent gets own identity file
- Backward compat: legacy `agent:{role}:{hash}` IDs still work

---

## What's Done (Completed Epics)

All completed epics are in the squashed initial commit. Key completed work:

| Area | Epics | Summary |
|------|-------|---------|
| **Core** | 1-9 | Daemon, CLI, WebSocket bridge, sync protocol |
| **UI** | 10-14, 21-24 | React SPA, AppShell, LiveFeed, InboxView, Data Terminal, Agent Work Context, Who-Has |
| **CLI** | A-E | Quickstart, message lifecycle, threads, output polish, coordination |
| **Embed** | F | React SPA embedded into Go binary (single port) |
| **E2E Tests** | E2E, E2E-S/S2, E2E-N/N2 | 70 scenarios, Playwright, sharding + naming test suites |
| **Refactoring** | JSONL Sharding, Agent Naming | Per-agent JSONL, ULID event_id, human-readable names |
| **MCP** | MCP Server | stdio transport, 5 tools, WebSocket waiter |
| **Sync** | Sync Worktree, Relocation | Git plumbing, sparse checkout, path redirect |
| **Security** | G104, Perms, Misc | 120 gosec findings fixed across 3 epics |
| **Infra** | CI/CD, Release Pipeline, Daemon Hardening | GitHub Actions, GoReleaser, Homebrew cask, flock/PID |
| **Docs** | Documentation Audit, Website | 16 docs, GitHub Pages site, llms.txt |
| **Identity** | Identity Resolution, Wait Flags | Auto-select, fail-fast, wait --all/--after |
| **Context** | Agent Context Management | Per-agent context storage with CLI detection |
| **Groups** | Agent Groups (simplified) | Flat groups, reply-to convention, inbox clustering |
| **Runtime** | Runtime Preset Registry | Enhanced quickstart, template engine, context prime |
| **v0.3.2** | Team Command, Per-File Tracking | thrum team, thrum version, thrum whoami, file_changes context |
| **Simplification** | Groups + Tailscale | Removed threads (3 commands, 486-line handler), flattened groups, deleted security layer (7 files, 1,074 lines), added pairing codes + token auth |
| **Config Consolidation** | thrum-6atk | Expanded config.json schema, interactive runtime selection, daemon reads config, thrum config show, documentation |
| **Bug Epic** | thrum-iisp | 6 bugs fixed and closed (thrum-pwaa, thrum-16lv, thrum-pgoc, thrum-5611, thrum-en2c, thrum-8ws1) |
| **Daemon Resilience** | thrum-fycc (10/10), thrum-chjm (in progress) | safedb, safecmd, timeouts, lock scope, resilience test suite |

---

## What's Remaining (Open Issues)

Full backlog: 102 open issues, 56 ready to work. Run `bd ready` for current list.

### Open Epics

| Epic | ID | Priority | Description |
|------|----|----------|-------------|
| v0.4.3 Readiness | thrum-chjm | P2 | Resilience test gaps, safedb migration, bug fixes (2/17 deps closed) |
| Listener Identity Failure | thrum-4ski | P1 | Remove vestigial subscriptions & fix cleanup |
| Browser Auto-Reg | thrum-z6ik | P1 | Integration test remaining |
| 20. Repo Cleanup | thrum-jje | P1 | Public release cleanup |
| 18. Documentation | thrum-6ay | P1 | Custom domain (P3) remains |
| 17. Release System | thrum-hqs | P1 | Homebrew tap repo + publish remain |
| 15. Relay Service | thrum-pm5 | P1 | Real-time sync relay server (0/13) |
| 19. Launch Marketing | thrum-hqm | P2 | Blog, video, README (0/5) |
| 16. GitHub App | thrum-dbq | P2 | Auto webhook setup (blocked by relay, 0/10) |

### Key Remaining Tasks

| ID | Priority | Title |
|----|----------|-------|
| thrum-chjm | P2 | v0.4.3 Readiness epic (15 children remaining) |
| thrum-a59i | P1 | Enable -race in resilience tests |
| thrum-4ski | P1 | Listener identity failure epic |
| thrum-620c | P2 | Cleanup subscriptions on session end |
| thrum-efjv | P2 | Pass caller_agent_id in subscription RPCs |

### v0.4.3 Readiness

**Epic**: thrum-chjm (P2) — 2 deps closed, 15 children open

**Completed**:
- safedb migration COMPLETE — all packages now use context-aware DB
- Team-fix resilience test harness merged (32 tests, 12 files, `f2d9415`)
- ExpandMembers deadlock fixed (nested query under SetMaxOpenConns=1)
- Plugin improvements: `/thrum:load-context`, bundled pre-compact script, identity-scoped backups
- Release test plan ready at `dev-docs/release-testing/full_test_plan.md`
- Test worktrees created: test-coordinator, test-implementer

**Remaining work**:
- 6 resilience test gaps (thrum-a59i, thrum-p2e6, thrum-4gv6, thrum-9r11, thrum-mbeo, thrum-ayxz)
- 6 bug fixes (thrum-620c, thrum-efjv, thrum-6xjs, thrum-mfiv, thrum-i2fe, thrum-x29q)
- 3 broken test fixes (thrum-lwls, thrum-xlig, thrum-03ay)
- Manual release testing using full_test_plan.md

**Reference**: See `dev-docs/daemon-resilience/Continuation_Prompt.md` for detailed resilience work context.

### Active Worktrees

| Worktree | Branch | Path | Status |
|----------|--------|------|--------|
| main | `main` | `/Users/leon/dev/opensource/thrum` | Multiple unpushed commits (caaf4f3) |
| context | `feature/context` | `~/.workspaces/thrum/context` | Behind main (72cb76a) |
| local-only | `feature/local-only` | `~/.workspaces/thrum/local-only` | Behind main (72cb76a) |
| team-fix | `feature/team-fix` | `~/.workspaces/thrum/team-fix` | Merged to main (f2d9415) |
| website-dev | `website-dev` | `~/.workspaces/thrum/website-dev` | Behind main (d1fac12) |
| test-coordinator | `test/coordinator` | `~/.workspaces/thrum/test-coordinator` | Release testing worktree (e039562) |
| test-implementer | `test/implementer` | `~/.workspaces/thrum/test-implementer` | Release testing worktree (e039562) |

---

## Recent Work (Last 2 Sessions)

### Session 38 (2026-02-16): v0.4.3 Readiness Work

**What was done:**

1. **Merged team-fix resilience test harness** — 32 tests, 12 files, commit `f2d9415` merged to main. Comprehensive test coverage for daemon resilience under load.

2. **Created v0.4.3 Readiness epic** — thrum-chjm (P2) with 17 children tracking test gaps, bug fixes, safedb migration.

3. **Completed safedb migration** — Remaining 5 packages (groups, subscriptions, checkpoint, cleanup, event_streaming) migrated to context-aware DB. Commit `3cb481e`.

4. **Fixed ExpandMembers deadlock** — Nested query under SetMaxOpenConns(1) caused rows cursor to hold single connection, sub-query blocked forever. Fixed by collecting rows first, closing cursor, then resolving roles.

5. **Plugin improvements**:
   - Created `/thrum:load-context` command for post-compaction context recovery
   - Bundled `pre-compact-save-context.sh` in plugin via `${CLAUDE_PLUGIN_ROOT}/scripts/`
   - Identity-scoped `/tmp` backup files for multi-agent safety
   - Updated `/thrum:prime` with tip about load-context

6. **Created comprehensive release test plan** — `dev-docs/release-testing/full_test_plan.md` with self-contained test scenarios.

7. **Created test worktrees** — test-coordinator and test-implementer for release testing.

8. **Beads closed**: thrum-o1a1, thrum-xk2k, thrum-d7po

### Session 37 (2026-02-15): Daemon Deadlock & SQLite WAL Fixes

**Summary** — Fixed critical daemon deadlock under load and SQLite WAL accumulation. Root causes identified by agents in falcon-backend during extended runtime. Fixed template issues with identity reuse, retagged v0.4.2, verified end-to-end.

**Key fixes:**
- P0 Bug (thrum-k8i0): Added SQLite busy_timeout, 30s server/client timeouts, 5min idle timeout
- P1 Bug (thrum-gpee): SetMaxOpenConns(1), synchronous=NORMAL to prevent WAL accumulation
- Template fixes: Removed hardcoded THRUM_NAME from runtime init templates

---
