# Daemon Resilience — Continuation Prompt

## Status: v0.4.3 READINESS IN PROGRESS — safedb Migration COMPLETE

**Epic thrum-fycc (Daemon Resilience Primitives) is fully closed** — 10/10
tasks.

**Epic thrum-chjm (v0.4.3 Readiness) is the active epic** — tracking remaining
resilience gaps, bug fixes, and test hardening.

## Current Work: thrum-chjm (v0.4.3 Readiness)

### safedb Migration — COMPLETE (thrum-o1a1)

All packages now use `*safedb.DB` with context propagation. No raw `*sql.DB`
remains in handler/service code (only `internal/schema` uses it intentionally
for setup/migrations via `safedb.Raw()`).

**Commits:**

- `3cb481e` — Migrate 5 remaining packages + fix ExpandMembers deadlock
- `0b94523..68c74c9` — Earlier safedb wiring (thrum-qvo6, thrum-i4v8)

**Bug found during migration:** `groups.ExpandMembers` had a deadlock — it
opened a rows cursor (consuming the single SQLite connection from
`SetMaxOpenConns(1)`), then called `queryAgentsByRole` which needed another
connection. Fixed by collecting all member rows first, closing the cursor, then
resolving roles.

### Resilience Test Gaps (from code review of team-fix merge)

| Beads ID   | Task                                             | Priority |
| ---------- | ------------------------------------------------ | -------- |
| thrum-a59i | Enable `-race` flag in resilience test script    | P1       |
| thrum-p2e6 | Add timeout enforcement tests                    | P2       |
| thrum-4gv6 | Add goroutine leak detection to concurrent tests | P2       |
| thrum-9r11 | Add crash-during-write recovery tests            | P2       |
| thrum-mbeo | Add CLI command timeout to `runThrum` helper     | P2       |
| thrum-ayxz | Add benchmark deadlock protection                | P3       |

### Bug Fixes Moved to v0.4.3

| Beads ID   | Task                                             | Origin                     |
| ---------- | ------------------------------------------------ | -------------------------- |
| thrum-620c | Cleanup subscriptions on session end             | thrum-4ski (listener epic) |
| thrum-efjv | Pass caller_agent_id in all subscription RPCs    | thrum-4ski (listener epic) |
| thrum-6xjs | AN-10: Agent delete removes all artifacts        | standalone                 |
| thrum-mfiv | AN-11: Delete non-existent agent returns error   | standalone                 |
| thrum-i2fe | AN-15: --force and --dry-run mutually exclusive  | standalone                 |
| thrum-x29q | AN-14: Agent cleanup emits event in events.jsonl | standalone                 |

### Broken Test Fixes

| Beads ID   | Task                                           | Origin              |
| ---------- | ---------------------------------------------- | ------------------- |
| thrum-lwls | Fix SC-01: init test file layout assertions    | sharding broke it   |
| thrum-xlig | Fix SC-02: re-init test file layout assertions | sharding broke it   |
| thrum-03ay | Fix smoke test: CLI message appears in UI      | UI rebuild broke it |

### Completed

| Beads ID   | Task                                                | Commit         |
| ---------- | --------------------------------------------------- | -------------- |
| thrum-o1a1 | Migrate remaining 5 packages to safedb              | 3cb481e        |
| thrum-xk2k | Add /thrum:load-context + fix PreCompact hook       | 6de4040        |
| thrum-d7po | Plugin consistency review for /tmp identity-scoping | (same session) |
| (merge)    | Merge team-fix resilience test harness (32 tests)   | f2d9415        |

## Completed Epic: thrum-fycc (Daemon Resilience Primitives)

All 10/10 tasks completed:

| Order | Beads ID   | Task                                   | Commit           |
| ----- | ---------- | -------------------------------------- | ---------------- |
| 1     | thrum-07dy | Create safedb package                  | a5ef2f7          |
| 2     | thrum-11uy | Create safecmd package                 | deec8cd          |
| 3     | thrum-ak83 | Fix client timeouts (5s dial, 10s RPC) | 2d8e1fb          |
| 4     | thrum-rwzt | Reduce server timeout 30s→10s          | 26dbe38          |
| 5     | thrum-wqjr | Fix pairing context propagation        | 45b1629          |
| 6     | thrum-kxks | Cap sync.notify goroutines             | 03e0ae0          |
| 7     | thrum-rwl1 | WebSocket handshake timeout            | d327d49          |
| 8     | thrum-qvo6 | Wire SafeDB into State (6 sub-steps)   | 4e29e17..0b94523 |
| 9     | thrum-z97t | Migrate git commands to safecmd        | c306f74          |
| 10    | thrum-i4v8 | Lock scope refactoring                 | 68c74c9          |

## Key Files Reference

| Area            | Files                                      | Status                   |
| --------------- | ------------------------------------------ | ------------------------ |
| SafeDB          | `internal/daemon/safedb/safedb.go`         | ✅                       |
| SafeCmd         | `internal/daemon/safecmd/safecmd.go`       | ✅                       |
| CLI Client      | `internal/cli/client.go`                   | ✅                       |
| Daemon Client   | `internal/daemon/client.go`                | ✅                       |
| Server          | `internal/daemon/server.go`                | ✅                       |
| State           | `internal/daemon/state/state.go`           | ✅                       |
| Projector       | `internal/projection/projector.go`         | ✅                       |
| Eventlog        | `internal/daemon/eventlog/query.go`        | ✅                       |
| RPC Handlers    | `internal/daemon/rpc/*.go`                 | ✅                       |
| Groups          | `internal/groups/resolver.go`              | ✅ safedb + deadlock fix |
| Subscriptions   | `internal/subscriptions/*.go`              | ✅ safedb                |
| Checkpoint      | `internal/daemon/checkpoint/checkpoint.go` | ✅ safedb                |
| Cleanup         | `internal/daemon/cleanup/contexts.go`      | ✅ safedb                |
| Event Streaming | `internal/daemon/event_streaming.go`       | ✅ safedb                |

## Timeout Value Summary

| Layer                      | Before          | After                |
| -------------------------- | --------------- | -------------------- |
| CLI dial timeout           | None            | **5s**               |
| CLI RPC call timeout       | 30s             | **10s**              |
| Server per-request timeout | 30s             | **10s**              |
| SQLite queries             | No context      | **Context-enforced** |
| Git commands (local)       | No timeout      | **5s**               |
| Git commands (network)     | No timeout      | **10s**              |
| WebSocket handshake        | No timeout      | **10s**              |
| Lock scopes                | Held during I/O | **Minimized**        |

## Plugin Changes

- **New:** `/thrum:load-context` — restores saved work context after compaction
- **Updated:** `/thrum:prime` — added tip about `/thrum:load-context`
- **Fixed:** PreCompact hook now runs `pre-compact-save-context.sh` (was wasted
  `thrum prime`)
- **Fixed:** `/tmp` backups use identity-scoped filenames for multi-agent safety
- **Updated:** SKILL.md context management docs

## Design & Research Documents

- **Design**: `dev-docs/plans/2026-02-15-daemon-resilience-design.md`
- **Implementation Plan**: `dev-docs/plans/2026-02-15-daemon-resilience-plan.md`
- **Research Findings**: `dev-docs/daemon-resilience/findings_*.md` (5 files)
- **Consolidated Report**: `dev-docs/daemon-resilience/consolidated_report.md`
- **Current Patterns**: `dev-docs/daemon-resilience/current_patterns.md`
- **Original Bug Report**:
  `dev-docs/bug_reports/eof-on-send-during-daemon-restart.md`

## Known Pre-Existing Issues

- `internal/cli/TestRenderTemplate` — pre-existing template rendering test
  failures unrelated to resilience work.
