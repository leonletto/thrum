# Resilience Testing - Implementation Progress

## Status: COMPLETE — All 32 tests passing (26s)

## Branch & Worktree
- **Worktree**: `/Users/leon/.workspaces/thrum/team-fix` (branch: `feature/team-fix`)
- **Base commit**: `c306f74` (main tip as of 2026-02-15)

## What's Done

### Tasks 1-6: Generator + Fixture (COMMITTED: afe71ef)
- `internal/testgen/generator.go` — Full generator with message_reads fix and active session fix
- `internal/testgen/generator_test.go` — 6 passing tests
- `internal/testgen/cmd/main.go` — CLI entry point
- `tests/resilience/testdata/thrum-fixture.tar.gz` — Checked-in fixture (50 agents, 10K msgs, 100 sessions, 20 groups)

### Tasks 7-14: Test Files
All 8 files in `tests/resilience/`:
- `doc.go` — build tag + go:generate with `-seed 42`
- `fixture_test.go` — shared helpers: setupFixture, setupCLIFixture, startDaemonAt, registerAllHandlers, rpcCall, rpcCallRaw, extractTarGz, fixtureAgentName, ensureSession, ensureSessionRaw
- `rpc_direct_test.go` — 11 RPC tests
- `concurrent_test.go` — 4 concurrency tests
- `recovery_test.go` — 5 recovery tests
- `multi_daemon_test.go` — 3 multi-daemon tests
- `benchmark_test.go` — 6 benchmarks
- `cli_roundtrip_test.go` — 9 CLI round-trip tests

### CLI Round-Trip Tests (NEW)
9 tests that exec the actual `thrum` binary against the fixture:
- `TestCLI_SendAndInbox` — Send directed message, verify in recipient inbox (29ms send, 29ms inbox)
- `TestCLI_InboxFiltering` — Full vs unread inbox at 10K scale (22ms full, 24ms unread)
- `TestCLI_TeamList` — Team list with 50 agents (121ms)
- `TestCLI_StatusOverview` — Status with populated data (23ms)
- `TestCLI_GroupSend` — Send to @coordinators group (25ms)
- `TestCLI_WaitTimeout` — Wait with 1s timeout returns promptly (524ms)
- `TestCLI_AgentContext` — Context show (15ms)
- `TestCLI_ReplyChain` — Multi-message reply thread (27ms reply)
- `TestCLI_QuickstartPopulated` — Register new agent in populated env (43ms)

### Scripts
- `scripts/run-resilience-tests.sh` — Convenience script for running resilience tests

## Test Run Results (32/32 pass)

```
PASS: TestCLI_SendAndInbox              (2.42s)
PASS: TestCLI_InboxFiltering            (0.21s)
PASS: TestCLI_TeamList                  (0.29s)
PASS: TestCLI_StatusOverview            (0.20s)
PASS: TestCLI_GroupSend                 (0.25s)
PASS: TestCLI_WaitTimeout              (0.73s)
PASS: TestCLI_AgentContext              (0.18s)
PASS: TestCLI_ReplyChain               (0.33s)
PASS: TestCLI_QuickstartPopulated       (0.23s)
PASS: TestConcurrent_10Senders          (10.74s)
PASS: TestConcurrent_ReadWriteMix       (2.90s)
PASS: TestConcurrent_InboxUnderLoad     (2.72s)
PASS: TestConcurrent_SessionLifecycle   (0.45s)
PASS: TestMultiDaemon_IndependentDaemons (0.21s)
PASS: TestMultiDaemon_DaemonRestart     (0.15s)
PASS: TestMultiDaemon_SharedFixture     (0.29s)
PASS: TestRecovery_FixtureRestore       (0.09s)
PASS: TestRecovery_ProjectionConsistency (0.89s)
PASS: TestRecovery_DaemonRestart        (0.15s)
PASS: TestRecovery_WALRecovery          (0.09s)
PASS: TestRecovery_CorruptedMessageJSONL (0.93s)
PASS: TestRPC_FixtureIntegrity          (0.09s)
PASS: TestRPC_HealthCheck               (0.09s)
PASS: TestRPC_AgentList                 (0.09s)
PASS: TestRPC_AgentListFilterByRole     (0.09s)
PASS: TestRPC_SendBroadcast             (0.11s)
PASS: TestRPC_SendDirected              (0.11s)
PASS: TestRPC_InboxPagination           (0.09s)
PASS: TestRPC_GroupList                  (0.09s)
PASS: TestRPC_GroupInfo                  (0.09s)
PASS: TestRPC_SessionList               (0.09s)
PASS: TestRPC_MessageReadTracking       (0.11s)

Total: 26.0s
```

## Fixes Applied

1. **Generator `message_reads` INSERT** — Added `session_id` column with agent->session lookup
2. **Generator active sessions** — First 10 sessions stay active (`ended_at IS NULL`)
3. **`fixtureAgentName(idx)`** — Correct `{role}_{idx:04d}` naming for all test references
4. **`ensureSessionForBench`** — Benchmark helper for session setup
5. **CLI fixture setup** — `setupCLIFixture` with `registerAllHandlers`, `startDaemonAt`, socket path redirect via `.thrum/redirect`
6. **`doc.go`** — Added `-seed 42` to go:generate directive

## Key Architecture Notes

### Fixture Agent Naming
```
fixtureAgentName(idx) = fmt.Sprintf("%s_%04d", roles[idx%5], idx)
roles = ["coordinator", "implementer", "reviewer", "planner", "tester"]
```
Active sessions (indices 0-9) always have `ended_at IS NULL`.

### Socket Path
Unix sockets have 108-char limit on macOS. `startDaemonAt` creates socket in `/tmp/ts-*/t.sock`.

## Next Steps
1. Run benchmarks: `go test -tags=resilience ./tests/resilience/ -bench=. -timeout 5m`
2. Commit everything
3. Merge back to main
