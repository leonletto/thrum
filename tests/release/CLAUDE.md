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

## Verifying changes — the subset workflow

A full gate run is ~25 minutes. When iterating on a scenario or
helper, use `run-subset.sh` instead. Same launcher, same harness,
sources the scenario files unmodified — zero drift from the gate.

```bash
# Single scenario in isolation
PATH="$(pwd)/bin:$PATH" bash tests/release/run-subset.sh 99

# Multiple scenarios, in declaration order
PATH="$(pwd)/bin:$PATH" bash tests/release/run-subset.sh 77 78 79 80

# Named groups (defined in run-subset.sh, see -l)
PATH="$(pwd)/bin:$PATH" bash tests/release/run-subset.sh -g restart-fixture

# Keep fixtures around for post-mortem
PATH="$(pwd)/bin:$PATH" THRUM_RELEASE_NO_TEARDOWN=1 \
  bash tests/release/run-subset.sh 99
```

`PATH=...` puts the local worktree's `bin/thrum` first so the run
uses the daemon binary you just built (`make dev`). Skip this only
when explicitly testing the installed `~/.local/bin/thrum`.

Logs land at `/tmp/reltest-<sess>.log`; per-fail pane snapshots at
`/tmp/thrum-release-failures/reltest-<sess>/`. Both persist after
the run completes — read them for post-mortem.

### When to suspect cascade vs real bug

If a scenario fails in the full gate but **passes in isolation or
in a 5-10 scenario subset**, the failure is a *cascade victim* of
something upstream corrupting shared state (COORD pane busy,
daemon's session bookkeeping, identity-guard state, etc.). Don't
fix the scenario — find the upstream cause.

If a scenario fails in **both** the gate and a small subset, it's
a real bug in the scenario itself OR the code it tests. Fix it.

The full-triage pattern is in
`/Users/leon/.thrum/worktrees/thrum/v0106-rc1/CLAUDE.md` under
"Release Test Triage Pattern" — that's the meta-doc; this file
is for agent-time invocation.

## Writing scenarios — pick the authoritative surface

The most common scenario-authoring mistake is asserting against
**pane scrollback** when the truth lives in **claude's JSONL**. The
pane is cosmetic (TUI rendering, alt-screen replays, terminal width
wrapping); the JSONL is what claude actually wrote and what its
model context contains. Always prefer JSONL surfaces when both are
available.

### JSONL location is deterministic

```
$HOME/.claude/projects/$(encode_cwd "<repo-path>")/*.jsonl
```

`encode_cwd` lives in `helpers/paths.sh`. Filenames are UUIDs that
rotate within a session; the helpers handle the glob.

### Helpers in `helpers/drive.sh` (most useful for scenario authors)

| Helper | Use when |
|--------|----------|
| `send_command <pane> <text>` | Type into a tmux pane with the `!`-prefix split for claude bash-mode |
| `send_slash_command <pane> <cmd>` | Type a `/thrum:foo` slash command with the `/`-prefix split |
| `send_bash_and_wait <pane> <repo> <cmd> <expected> [timeout]` | Send `! cmd` AND gate on a bash-stdout substring — prefer this over raw `send_command` for state-mutating ops; the gate prevents thrum-rbp6 keystroke races |
| `wait_for_pane_idle <pane> [timeout]` | Wait for tmux pane content to stabilize (~2 identical samples) |
| `wait_for_jsonl_match <repo> <jq-filter> [timeout] [floor_ts]` | Poll claude's JSONL for an entry where the filter is truthy. `floor_ts` (RFC3339-prefixed) gates against stale matches from prior scenarios |
| `wait_for_bash_stdout_contains <repo> <substring> [timeout] [floor_ts]` | Specialized: poll for `<bash-stdout>`-wrapped user-message entries from `!`-prefix invocations |
| `wait_for_attachment <repo> <hook-event> <substring> [timeout] [floor_ts]` | Poll for a hook attachment (SessionStart, UserPromptSubmit, Stop, PreToolUse, PostToolUse) whose stdout or content contains a substring. Caller input passed via jq `--arg` — safe against embedded quotes/backslashes |
| `wait_for_session_start <repo> [timeout]` | Specialization of `wait_for_attachment` for SessionStart presence (no body match) |
| `wait_for_banner_emit <pane> [timeout] [since-line-count]` | Wait for the daemon's identity banner sentinel (`PrimeTruncationSentinel`) to land in pane scrollback. Pass `since-line-count` (captured `tmux display-message -p -t "$pane" '#{history_size}'` BEFORE the launch/restart) to anchor against stale sentinels in shared panes |
| `assert_inbox_contains <agent-name> <repo> <substring> [timeout]` | Poll an agent's inbox via tmux-exec for a marker substring. `.messages[]?` null-safe against daemon hints-only responses |
| `assert_tool_use_bash <repo> <sid> <name> <floor_ts> <substring> [timeout]` | Emit pass/fail for an assistant tool_use Bash entry with a command-substring match |
| `capture_thrum_json <repo> <agent> <out-file> <thrum-args...>` | Run `thrum --json` in an ephemeral tmux-exec pane; writes JSON to host-side `<out-file>`. Used by `assert_inbox_contains` and others |
| `clear_trust <pane>` | Drive past claude's first-time-cwd folder-trust dialog |
| `spawn_sub_fixture_claude <tmux-name> <cwd> [launch-cmd]` | Raw `tmux new-session` + claude launch for sub-fixtures that need to bypass the daemon's worktree-identity guard |

### Common scenario shapes

- **"Did claude render a hook output?"** → `wait_for_attachment` against
  the hook event + a substring of the rendered text. Don't grep pane
  scrollback — the hook output goes to claude's model context, not to
  the visible pane.

- **"Did the daemon route a message?"** → If end-to-end matters:
  `wait_for_jsonl_match` against the recipient's claude JSONL (the
  daemon-typed nudge lands as a user-string message, and claude's
  autonomous Bash tool_result for `thrum inbox --unread` has the
  marker verbatim). If daemon state is what you really need:
  `assert_inbox_contains` — but be aware that under heavy gate load
  the daemon may return hints-only responses and the helper will
  correctly time out.

- **"Did claude run a slash command?"** → `wait_for_jsonl_match` for
  an assistant tool_use Bash entry (or use `assert_tool_use_bash`).
  Accept multiple command shapes if the skill body offers more than
  one (scen 21 evolved this way — `thrum prime` and `thrum context
  show --session` are both valid responses to `/thrum:load-context`).

- **"Did the post-restart banner land?"** → `wait_for_banner_emit`
  with the pre-launch history_size as the third arg. Pane scrollback
  is the only surface for the daemon's cosmetic banner; the
  history_size anchor prevents matching a stale sentinel from an
  earlier scenario in the same pane.

- **"Did a CLI command succeed?"** → Use `send_bash_and_wait` to gate
  on the success-line bash-stdout. Don't gate on exit code alone if
  the operation goes through tmux-exec — see the warn-hint footgun
  below.

## Common failure modes (and how scenarios should defend)

### thrum-rbp6 — send_command keystroke race

The COORD pane is shared across all scenarios. Under load,
`send_command`'s text+Enter timing can race with the next scenario's
`!`-prefix keystroke: the next `!` arrives BEFORE the prior
command's Enter has fully submitted, splicing the `!` into the
trailing position of the prior command's input. The result is a
corrupted command like `--to @test_implementer!` (which the daemon
rejects as "unknown recipient") or `--force!` (which the CLI rejects
as "unknown flag").

**Defense**: prefer `send_bash_and_wait` over raw `send_command` for
state-mutating ops. The wait_for_bash_stdout_contains gate forces
the command's Enter to fully submit (claude renders the daemon
response) BEFORE returning, eliminating the race window. Scens 23 +
25 + 100 all rely on this pattern.

### thrum-9sxc — daemon warn-level hint causes CLI exit 1

When `tmux-exec` is used as the caller, the daemon emits a
`worktree.PaneTargetForIdentity refused` warn-level hint (because
the tmux-exec pool pane isn't in any agent's worktree). A downstream
CLI path turns this warn into a non-zero exit even when the
operation succeeded — the success line prints, the side effects
happen, but exit code is 1.

**Defense**: gate scenarios on the AUTHORITATIVE side effect (file
on disk, JSONL entry, identity-file presence), not on the CLI exit
code alone. If you need both, accept either exit-0 OR the
success-line in output. Scens 85 + 88 use this pattern.

### thrum-6hqy.1 / thrum-gdf8 — banner emit is async + sometimes skipped

The daemon's post-launch banner (`emitIdentityBanner`) fires from a
goroutine ~10s after `launchCmd`. It sends the banner printf INTO
claude's running input prompt via `sendKeysAndSubmit`. Two
implications:

1. Banner content renders inside claude's TUI response area with
   leading whitespace — column-0 regex assertions never match. Use
   substring match or check the JSONL for the SessionStart hook
   attachment (which has the canonical identity content directly).
2. On the INITIAL launch path (scen 99 surface), the banner emit
   skips entirely when the identity file's `TmuxSession` field
   isn't populated yet. The hook attachment is still authoritative.

**Defense**: when checking "agent identity reached claude",
`wait_for_attachment "$REPO" "SessionStart" "# Role: <role>"` against
the hook output. When checking "post-restart banner landed in the
pane" (cosmetic UX assertion), `wait_for_banner_emit` with a
`since-line-count` anchor.

### Hook output format ≠ daemon banner format

The SessionStart hook
(`claude-plugin/scripts/inject-prime-context.sh`) renders identity
in markdown:

```
# Agent: @<name>

- **Role:** <role>
- **Worktree:** ...
- **Branch:** ...
```

The daemon's `emitIdentityBanner` renders the same identity but in a
different layout:

```
Agent: @<name>
Role:  <role>
Worktree: ...
Branch: ...
```

Different surfaces, different layouts. Pick the one that matches
your assertion intent and stick with it.

### Subset state pollution

`THRUM_RELEASE_NO_TEARDOWN=1` keeps fixture state across the
post-run cleanup. If you run two subsets back-to-back without
clearing leaked sessions (e.g. `kafm6-self-restart-test`,
`force-restart-test`), the next setup-repo's `thrum tmux create
<same-name>` will fail with "session already running". Clean up
manually before re-running:

```bash
tmux ls | grep -E "kafm|force-restart|reltest-pool" | cut -d: -f1 | xargs -n1 tmux kill-session -t
```

## Lessons from RC1 triage (May 2026)

1. **Pane scrollback is a cosmetic surface, JSONL is the
   authoritative one.** Many of the v0.10.6 RC1 residual failures
   were scenarios checking the pane for content that lived in claude's
   model context but never appeared on screen (post-thrum-6hqy.1
   reality). Always ask: "where did the daemon/hook actually write
   this?"

2. **Timestamp filters prevent stale-match silent passes.** Any helper
   that polls JSONL or bash-stdout for a marker substring should take
   a `floor_ts` to scope matches to "this command's output". Without
   it, a prior scenario's stale entry containing the same substring
   will short-circuit the wait — a silent false positive that hides
   real regressions.

3. **"Load-flake" is usually a real bug under a load-only repro.**
   Before characterizing a failure as load-flake, isolate it in a
   subset. If it passes in isolation, look for shared-state cascade
   (COORD pane state, daemon bookkeeping, identity-guard). If it
   fails in isolation too, fix the test or the code. Don't
   accumulate load-flake as a category — each one usually has a
   specific cause.

4. **`make dev` + `PATH="$(pwd)/bin:$PATH"` is the iteration loop.**
   `make install` touches the shared `~/.local/bin/thrum` and
   restarts other agents' daemons. `make dev` builds the worktree
   binary; the PATH override puts it first so the harness's `thrum
   daemon restart` (and subsequent CLI calls inside fixtures) all use
   the new binary. The shared binary is untouched.

5. **Don't mock daemon-state queries — pivot to JSONL.** Several
   scenarios (95, 80, 99) originally queried daemon state via
   `thrum inbox --json` / `tmux status --json` through tmux-exec.
   Under load this hits the daemon's identity guard and returns a
   hints-only response with no data. The same information is in
   claude's JSONL (via the inbound nudge, the autonomous Bash
   tool_result, or the SessionStart attachment) — deterministic,
   no daemon-call dependency. Pivot the assertion's SURFACE rather
   than retrying the broken call.
