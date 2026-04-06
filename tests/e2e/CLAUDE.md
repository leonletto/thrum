# E2E Test Development Guide

## The PID Identity Problem

Thrum resolves agent identity by walking the process tree to find a `claude`
ancestor. When E2E tests run thrum commands from within a developer's Claude Code
session, the thrum binary finds the developer's Claude process and adopts the
wrong identity. This causes tests to register agents in the wrong daemon, create
identity files with wrong names, and produce silent failures.

## The Fix: tmux Isolation

All thrum CLI calls in E2E tests run inside isolated tmux sessions. tmux panes
have ancestry `sh → tmux → PID 1` — no `claude` in the chain, so PID identity
resolution never fires.

**global-setup.ts** cleans ALL `THRUM_*` vars from `process.env` before any tmux
server starts. The tmux server inherits this clean env, so every pane is born
without developer session state. No per-command cleanup needed.

## How to Run thrum Commands in Tests

Use the helpers in `helpers/thrum-cli.ts`. Never call `execFileSync` or `spawn`
with the thrum binary directly.

```typescript
import { thrum, thrumIn, thrumJson } from './helpers/thrum-cli.js';

// Run as default coordinator identity (e2e_coordinator)
thrum(['send', 'hello', '--to', '@e2e_implementer']);

// Run as a specific identity
const myEnv = { THRUM_NAME: 'my_agent', THRUM_ROLE: 'tester', THRUM_MODULE: 'all' };
thrum(['status'], 10_000, myEnv);

// Run in a specific worktree
thrumIn(getImplementerRoot(), ['agent', 'whoami'], 10_000, implEnv);

// Parse JSON output
const team = thrumJson<{ members: Agent[] }>(['team']);
```

## How It Works Under the Hood

1. `thrum()` / `thrumIn()` build a shell command with env prefix:
   `THRUM_NAME='my_agent' THRUM_ROLE='tester' '/path/to/thrum' 'status'`
2. `tmuxExec()` runs this command in a fresh tmux pane with the test repo as cwd
3. The pane inherits clean env from the tmux server (no `THRUM_*` vars)
4. The env prefix sets only the identity vars needed for this specific command
5. thrum resolves identity from env vars, not PID ancestry

## Rules for New Tests

- **Always use `thrum()` or `thrumIn()`** — never `execFileSync(BIN, ...)` or
  `spawn(BIN, ...)`
- **Always pass an env object** when using a non-default identity — the env
  prefix is the only way identity reaches the tmux pane
- **For background commands** (like `thrum wait`), use `tmuxExecAsync()` from
  `helpers/tmux-exec.ts`
- **Register test-specific agents** in `beforeAll` — don't rely on global-setup
  agents for test-specific identities
- **Use `--force`** on agent registration to handle re-runs without cleanup

## File Structure

| File | Purpose |
|------|---------|
| `helpers/tmux-exec.ts` | Low-level tmux primitives (tmuxExec, tmuxExecAsync, tmuxKillServer) |
| `helpers/thrum-cli.ts` | High-level wrappers (thrum, thrumIn, thrumJson, env helpers) |
| `helpers/fixtures.ts` | Agent registration helpers (registerAgent, quickstartAgent, etc.) |
| `global-setup.ts` | Creates test repo, daemon, agents; cleans THRUM_* env |
| `global-teardown.ts` | Preserves artifacts for inspection |
