# Resilience Testing Design

**Date:** 2026-02-15
**Status:** Approved
**Scope:** Performance baselines, stress testing, and resilience verification for Thrum

## Problem

All 769 existing tests operate against empty or trivially-small databases. There
are no tests that exercise Thrum at production-realistic scale, no performance
baselines to detect regressions, no concurrency stress tests, and no database
recovery scenarios.

## Goals

1. Create a deterministic, production-realistic test fixture (50 agents, 10K
   messages, 100 sessions, 20 groups, 500 events)
2. Test every Thrum facility at scale: CLI round-trips, RPC handlers,
   concurrent access, database recovery
3. Establish performance benchmarks that CI can track for regressions
4. Test multi-daemon coordination without external infrastructure (Docker-free)

## Non-Goals

- Multi-machine distributed testing (Tailscale sync across hosts)
- Load testing with actual network latency simulation
- Fuzz testing of input parsing

## Architecture

### Approach: Layered Tests with Shared Fixture

A Go generator creates a complete `.thrum/` directory (SQLite + JSONL +
identities + contexts), compressed as a checked-in `.tar.gz`. Separate test
files exercise different concerns in parallel.

### Data Volumes

| Entity           | Count  | Distribution                                       |
| ---------------- | ------ | -------------------------------------------------- |
| Agents           | 50     | 5 roles x 8 modules + 10 extras                   |
| Messages         | 10,000 | 60% broadcast, 30% directed, 10% threaded          |
| Sessions         | 100    | ~2 per agent, varied durations (5min - 8hrs)       |
| Groups           | 20     | Team groups, role groups, module groups, 3 nested   |
| Events           | 500    | Agent registrations, session lifecycle              |
| Work contexts    | 50     | One per active session, realistic git state         |
| Message reads    | ~7,000 | 70% of messages marked read by recipients          |
| Subscriptions    | 150    | Mix of scope-filtered and wildcard                  |
| Group members    | 80     | Agent members, role-based members, nested groups    |
| Sync checkpoints | 3      | For multi-daemon sync testing                       |

### Generated Artifacts

```
.thrum/
  config.json                    # Daemon config (local_only: true)
  thrum.db                       # Empty placeholder
  identities/
    {name}.json                  # 50 identity files
  context/
    {name}.md                    # 50 context files with content
  messages/
    {agent_name}.jsonl           # Per-agent JSONL message files
  events.jsonl                   # Non-message events (agent lifecycle)
  var/
    messages.db                  # Pre-populated SQLite (schema v13)
```

The generator creates BOTH JSONL files AND the SQLite database in sync, enabling
tests to verify projection consistency.

## Components

### 1. Test Data Generator (`internal/testgen/`)

**File:** `internal/testgen/generator.go`

A Go package with a `Generate(outputDir string, seed int64)` function that:

1. Creates `.thrum/` directory structure
2. Generates deterministic agent identities (seeded PRNG)
3. Creates sessions with realistic start/end times
4. Generates 10K messages with varied scopes, formats, and threading
5. Creates groups with agent, role, and nested group members
6. Writes JSONL event files (sharded by agent for messages)
7. Initializes SQLite database via `schema.InitDB()` and populates all tables
8. Creates identity JSON files matching the `identity` package format
9. Creates context markdown files with realistic session summaries
10. Compresses everything into a `.tar.gz`

**CLI entry point:** `internal/testgen/cmd/main.go`
```bash
go run ./internal/testgen/cmd -output tests/resilience/testdata/thrum-fixture.tar.gz -seed 42
```

All data is deterministic given the seed. Same seed = identical fixture.

### 2. Test Files

#### `tests/resilience/doc.go`

```go
//go:build resilience

// Package resilience contains performance and resilience tests for Thrum.
// Run with: go test -tags=resilience ./tests/resilience/ -v
package resilience

//go:generate go run ../../internal/testgen/cmd -output testdata/thrum-fixture.tar.gz -seed 42
```

#### `tests/resilience/fixture_test.go` — Shared Setup

- `TestMain` verifies the fixture exists
- `setupFixture(t)` extracts `.tar.gz` to `t.TempDir()`, returns path
- `startDaemon(t, thrumDir)` starts daemon on random port with `t.Cleanup()`
- `runThrum(t, thrumDir, args...)` runs CLI commands against the test daemon

#### `tests/resilience/cli_roundtrip_test.go` — CLI Integration

| Test                         | What it verifies                                      |
| ---------------------------- | ----------------------------------------------------- |
| `TestCLI_SendAndInbox`       | Send message, verify it appears in recipient's inbox  |
| `TestCLI_InboxFiltering`     | `--unread`, `--from`, `--module` filters at 10K scale |
| `TestCLI_TeamList`           | `thrum team` lists all 50 agents correctly            |
| `TestCLI_StatusOverview`     | `thrum status` and `thrum overview` with full data    |
| `TestCLI_GroupSend`          | Send to group, verify all members receive             |
| `TestCLI_WaitTimeout`        | `thrum wait --timeout 1s` returns correctly           |
| `TestCLI_QuickstartPopulated`| New identity in populated environment                 |
| `TestCLI_AgentContext`       | `thrum agent list --context` at scale                 |
| `TestCLI_ReplyChain`         | Multi-message reply thread                            |

#### `tests/resilience/rpc_direct_test.go` — RPC Layer

| Test                          | What it verifies                                     |
| ----------------------------- | ---------------------------------------------------- |
| `TestRPC_SendAllScopeTypes`   | Broadcast, directed, module-scoped sends             |
| `TestRPC_InboxPagination`     | Pagination over 10K messages (offset, limit)         |
| `TestRPC_AgentListFilters`    | Filter by role, module, online status at 50 agents   |
| `TestRPC_GroupResolveNested`  | Nested group resolution (group containing groups)    |
| `TestRPC_SubscriptionLifecycle` | Subscribe, receive, unsubscribe, verify no delivery |
| `TestRPC_MessageReadTracking` | Read tracking across multiple sessions               |
| `TestRPC_WorkContextUpdates`  | Bulk work context updates and queries                |
| `TestRPC_EventStreamAtScale`  | Event streaming with 500+ events                     |

#### `tests/resilience/concurrent_test.go` — Contention

| Test                              | What it verifies                                 |
| --------------------------------- | ------------------------------------------------ |
| `TestConcurrent_10Senders`        | 10 goroutines sending messages simultaneously    |
| `TestConcurrent_ReadWriteMix`     | Readers and writers competing for SQLite          |
| `TestConcurrent_SubscriptionChurn`| Rapid subscribe/unsubscribe in parallel           |
| `TestConcurrent_InboxUnderLoad`   | Inbox queries during heavy write load             |
| `TestConcurrent_WALCheckpoint`    | WAL checkpoint doesn't block readers              |
| `TestConcurrent_SessionLifecycle` | Multiple sessions starting/ending simultaneously  |

#### `tests/resilience/recovery_test.go` — Database Recovery

| Test                              | What it verifies                                 |
| --------------------------------- | ------------------------------------------------ |
| `TestRecovery_FixtureRestore`     | Extract fixture, start daemon, verify integrity  |
| `TestRecovery_ProjectionConsistency` | JSONL replay matches pre-built SQLite DB      |
| `TestRecovery_MigrationWithData`  | Schema migration from v5 with populated data     |
| `TestRecovery_DaemonRestart`      | Stop daemon, restart, verify no data loss         |
| `TestRecovery_WALRecovery`        | Truncate WAL, verify daemon recovers             |
| `TestRecovery_BackupRestore`      | Copy DB, modify original, restore, verify        |
| `TestRecovery_CorruptedMessage`   | Malformed JSONL line, verify graceful skip        |

#### `tests/resilience/multi_daemon_test.go` — Multi-Daemon

| Test                              | What it verifies                                 |
| --------------------------------- | ------------------------------------------------ |
| `TestMultiDaemon_TwoWaySync`      | Two daemons exchange events via sync              |
| `TestMultiDaemon_DaemonRestart`   | Start, populate, stop, start new, verify state   |
| `TestMultiDaemon_ThreeNodeMesh`   | Three daemons forming mesh, message propagation  |
| `TestMultiDaemon_ConflictResolution` | Concurrent writes to separate daemons          |

#### `tests/resilience/benchmark_test.go` — Performance

| Benchmark                         | What it measures                                 |
| --------------------------------- | ------------------------------------------------ |
| `BenchmarkSendMessage`            | Single message send latency (ns/op)              |
| `BenchmarkInbox10K`               | Inbox query over 10K messages                    |
| `BenchmarkInboxUnread`            | Unread-only inbox query                          |
| `BenchmarkAgentList50`            | Agent list with 50 agents                        |
| `BenchmarkGroupResolve`           | Nested group resolution                          |
| `BenchmarkConcurrentSend10`       | 10 concurrent senders throughput                 |
| `BenchmarkMessageReadMark`        | Marking messages as read                         |
| `BenchmarkWorkContextUpdate`      | Work context update latency                      |

### 3. Build Tag Isolation

All test files use `//go:build resilience` so `go test ./...` skips them during
normal development. Run explicitly:

```bash
go test -tags=resilience ./tests/resilience/ -v -timeout 10m
```

### 4. Fixture Management

The compressed fixture (`~500KB`) is checked into git at
`tests/resilience/testdata/thrum-fixture.tar.gz`.

Regenerate after schema changes:
```bash
go generate -tags=resilience ./tests/resilience/...
```

CI verification that fixture is up-to-date:
```bash
go generate -tags=resilience ./tests/resilience/...
git diff --exit-code tests/resilience/testdata/
```

### 5. Convenience Script

`scripts/run-resilience-tests.sh`:
```bash
#!/bin/bash
set -euo pipefail
echo "Running resilience tests..."
go test -tags=resilience ./tests/resilience/ -v -timeout 10m -count=1
echo ""
echo "Running benchmarks..."
go test -tags=resilience ./tests/resilience/ -bench=. -benchmem -count=3 -timeout 10m
```

## Implementation Order

1. **Generator** — `internal/testgen/` package + CLI entry point
2. **Fixture infrastructure** — `fixture_test.go` with shared setup helpers
3. **CLI round-trip tests** — exercises the most user-facing functionality
4. **RPC direct tests** — core logic at scale
5. **Concurrent tests** — stress testing
6. **Recovery tests** — database integrity
7. **Multi-daemon tests** — coordination
8. **Benchmarks** — performance baselines
9. **Script + CI integration** — `run-resilience-tests.sh`

## Success Criteria

- All resilience tests pass on a clean checkout
- Benchmarks establish baseline numbers for: message send, inbox query, agent
  list, group resolve, concurrent sends
- No SQLite `BUSY` errors under concurrent load
- Fixture restore + daemon startup takes < 2 seconds
- Full test suite completes in < 5 minutes
- Fixture is deterministic (same seed = identical output)
