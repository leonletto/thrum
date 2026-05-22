# tests/release — Agent-facing guide

Claude auto-loads this file when your working directory is under
`tests/release/`. Read it before running anything here.

## Don't worry about "where" — the harness self-isolates

You can invoke `bash tests/release/run.sh ...` or
`bash tests/release/behavioral/run-behavioral.sh ...` from anywhere, including
**from inside this active agent session**.

The harness detects a contaminated context (a `claude` or `codex` ancestor in
the process tree) and self-isolates into a detached default-server tmux
session so the run gets clean process ancestry. The wrapper waits and
propagates the inner exit code — your terminal sees the real result.

If you want to watch a self-isolated run live, the launcher prints the
session name and the attach command, e.g.:

```
tests/release: agent ancestor detected ('claude'); self-isolating into
detached tmux session 'reltest-12345' on the default server.
  attach to watch: tmux attach -t reltest-12345
```

You don't need `make deploy-remote` or a clean shell for the local gate.
Both are unnecessary now that the harness self-isolates.

## Why this exists

Pre-codification, running the harness from inside an agent pane leaked the
agent's pane identity into the daemon's caller resolver (PID-walk +
cross-worktree guard), causing fixture dispatches to refuse with `-32002` or
wrong-worktree errors before any test could start. The seam is process
ancestry, NOT env vars — stripping `THRUM_*` is insufficient. The proven fix
(scenario 29 + 58 green end-to-end through the launcher) is to re-exec the
harness in a default-server tmux pane with `TMUX`/`TMUX_PANE` stripped, so
the pane's parent becomes the tmux server (launchd/pid 1) — no `claude`
ancestor remains.

The sentinel `THRUM_RELEASE_ISOLATED=1` marks the re-exec'd process so we
never loop. If you ever see the "fail loud" diagnostic about an agent
ancestor + sentinel set, that means the re-exec didn't produce a clean
ancestry (something nested oddly); start from a clean shell instead of
re-running blindly.

## Invocations

| Run | Command |
|-----|---------|
| All run.sh scenarios (108) | `bash tests/release/run.sh` |
| Filtered run.sh scenario(s) | `bash tests/release/run.sh '58-*.test.sh'` |
| All behavioral cards (5) — auto two-pass | `bash tests/release/behavioral/run-behavioral.sh` |
| One behavioral card | `bash tests/release/behavioral/run-behavioral.sh --filter='01-*'` |

## Behavioral cards: claude vs codex (the two-pass)

A single `run-behavioral.sh` invocation runs:
- card `01-worktree-create-launch.yaml` under **claude**, and
- cards `02-codex-*` ... `05-codex-*` under **codex**.

Runtime is auto-selected per card from filename: `NN-codex-*` → `codex`, else
`claude`. If a card needs codex and `codex` isn't on PATH, that card is
marked failed with a clear `SKIP/FAIL: card requires --runtime=codex; codex
not installed` message; the rest of the run continues.

To force a single runtime across every card (override the auto-select), pass
`--runtime=claude` or `--runtime=codex`. Use sparingly — the default
auto-select is what the gate expects.

## What's NOT the gate

`run-remote.sh` is a small **2-scenario cross-machine smoke** under
`remote-scenarios/` (e.g. `r01`). It is NOT the release gate. The gate is:

- `run.sh` 108 scenarios under `scenarios/`,
- `run-behavioral.sh` 5 cards under `behavioral/cards/`,
- E2E (`tests/e2e/`) — separate, already self-isolating.

All three run locally on a single host via the self-isolating harness.

## Pointers

- Design doc:
  `dev-docs/plans/2026-05-22-release-harness-self-isolation-plan.md`
- Self-isolating launcher: `helpers/self-isolate.sh`
- Per-tool allowlist helper: `helpers/fixture-perms.sh` (`write_fixture_perms`)
- Trust-dialog clear: `helpers/drive.sh` (`clear_trust`)
- README (operator-facing): `tests/release/README.md`
