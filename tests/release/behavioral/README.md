# Behavioral Test Harness

A development tool for iterating on role preambles and runtime plugins.
The harness drives one or more live agents through scripted YAML test
cards, polling structural assertions against thrum daemon / filesystem /
tmux state, with an optional LLM-judge layer for fuzzy assertions and
auto-diagnose enrichment of failed steps.

## What this is NOT

- **Not a release gate.** Failing behavioral tests are signals to iterate
  on a preamble or plugin, not ship-blockers.
- **Not part of `make ci`.** The harness spawns real AI runtimes and
  makes real LLM API calls; it is opt-in via `make behavioral`.
- **Not a regression suite for thrum the codebase.** Use
  `tests/release/run.sh` (scenarios fixture) for that.

## Invocation

```bash
# All defaults: setup + run all v1 cards against claude with the project
# baseline preamble. Results land under dev-docs/behavioral/runs/.
make behavioral

# One-time setup only (generates go.work from .env, smoke-pings the LLM
# judge). Idempotent.
make behavioral-setup

# Direct runner (skips the setup target). Useful when iterating.
./tests/release/behavioral/run-behavioral.sh --runtime=claude --filter='01-*'
```

### Runner flags

| Flag                       | Description                                                                  |
|----------------------------|------------------------------------------------------------------------------|
| `--runtime=<name>`         | Runtime to test (default `claude`; v1 also exercises `codex` for dogfooding) |
| `--preamble=<role>:<path>` | Override a role preamble with a candidate file (repeatable)                  |
| `--filter=<glob>`          | Only run cards matching the glob (default `*.yaml`)                          |
| `--no-auto-diagnose`       | Disable LLM auto-diagnose on failed steps                                    |
| `--capture <name>`         | Save a baseline to `dev-docs/behavioral/baselines/<name>/`                   |
| `--compare <name>`         | Score the run against an existing baseline; emit `<test>.similarity.json`    |

## Environment

Both keys live in `.env` at the **main** repo root (worktrees don't share
gitignored files; the runner discovers `.env` via
`git rev-parse --git-common-dir`).

```
LLM_CLIENT_PATH=/absolute/path/to/llm-client-go-module-dir
ZAI_API_KEY=...
LLM_JUDGE_MODEL=glm-4.5-flash   # optional override
```

Without `LLM_CLIENT_PATH`/`ZAI_API_KEY`, the harness still runs in
pure-structural mode: `llm_judge` predicates fail closed with a clear
error, and auto-diagnose is skipped. A preflight WARN flags any card
that declares `llm_judge` predicates when those env vars are missing.

## Where results land

Everything is written under the gitignored `dev-docs/behavioral/`:

```
dev-docs/behavioral/
├── runs/<timestamp>-<runtime>-coord:<sha8>_impl:<sha8>/
│   ├── <test-id>.jsonl              # one record per step + __summary__
│   ├── <test-id>.transcripts.json   # per-step pane excerpts (sidecar)
│   └── <test-id>.similarity.json    # only when --compare was passed
└── baselines/<name>/
    └── <test-id>.json               # captured by --capture
```

Run dirs are ephemeral and safe to delete. Baselines are reference
artifacts; keep ones you reuse.

## Authoring a test card

Cards live under `tests/release/behavioral/cards/*.yaml`. Each card has:

- `id`, `description` — used in run output and JSONL records
- `agents:` — map of session-key → `{ role, module?, preamble? }`. The
  runner derives the agent name as `test_<role>` and registers each via
  `thrum quickstart` + `tmux new-session` + `thrum tmux launch`.
- `steps:` — list of `{ id, send?, assert?, timeout?, poll_interval?,
  diagnostic? }`.

Predicate kinds:

- `fs` — `dir_exists`, `file_exists`, `file_contains`, `file_matches`
- `daemon` — `agent_registered`, `message_delivered`,
  `agent_replied_to`, `agent_session_active`
- `tmux` — `session_exists`, `pane_running_runtime`, `pane_contains`
- `llm_judge` — `transcript_satisfies_rubric` (rubric, threshold,
  transcript_source.session, transcript_source.last_n_lines)

Variables substituted in card paths/messages: `${FIXTURE_REPO}`,
`${FIXTURE_WORKSPACES}`, `${FIXTURE_THRUM}`, `${PREAMBLE_COORDINATOR}`,
`${PREAMBLE_IMPLEMENTER}`, `${RUNTIME}`.

See `01-worktree-create-launch.yaml` for a worked example.

## Troubleshooting

- **Daemon fails to start in fixture.** Check that `mktemp` lands the
  fixture under `/tmp/bh-XXXXXX` (short path) — macOS AF_UNIX socket
  paths must stay under 104 bytes.
- **`agent list` doesn't see a freshly-quickstarted agent.** The daemon
  projection lags by up to one sync interval (60s default). Use the
  pre-warmed `test_coord`/`test_impl` names in self-tests, or shorten
  `sync_interval` in the fixture config.
- **Need to inspect a failed run.** Set
  `THRUM_BEHAVIORAL_NO_TEARDOWN=1`. The fixture is preserved at
  `/tmp/bh-XXXXXX/` when the run reports failures.
- **`thrum tmux create` aborts with "not a worktree".** The runner uses
  bare `tmux new-session` + `thrum tmux launch` instead, since the
  fixture is a standalone git repo (not a registered worktree). Don't
  use `thrum tmux create` here.

## Deeper reference

- Design spec:
  [`dev-docs/specs/2026-05-09-behavioral-test-harness-design.md`](../../../dev-docs/specs/2026-05-09-behavioral-test-harness-design.md)
- Implementation plan:
  [`dev-docs/plans/2026-05-09-behavioral-test-harness-plan.md`](../../../dev-docs/plans/2026-05-09-behavioral-test-harness-plan.md)
