# Release Test Framework

Bash runner that drives a real coordinator+implementer multi-agent thrum
fixture in tmux panes, asserts against agent JSONL transcripts via
`!`-prefix commands, and emits tailable per-assertion output. Replaces
(over time) the operator-driven markdown plans under
`dev-docs/release-testing/`.

**Design spec:** `dev-docs/specs/2026-04-26-release-test-framework-design.md`
**Phase 1 plan:** `dev-docs/plans/2026-04-26-release-test-framework-implementation.md`
**Phase 2 plan:** `dev-docs/plans/2026-04-27-release-test-framework-phase-2-implementation.md`

## Run all scenarios

```bash
./tests/release/run.sh
```

## Run one scenario

```bash
./tests/release/run.sh "01-*.test.sh"
```

The arg is a glob filter against `scenarios/`.

## Scenarios

| Scenario | What it verifies |
|----------|------------------|
| `01-session-start-injection` | The `inject-prime-context.sh` SessionStart hook injects the briefing envelope + drops the pre-injection nudge for a registered agent. |
| `02-restart-snapshot-preamble` | After IMPL pane saves a restart snapshot and is restarted, the new SessionStart attachment carries the loud `🛑 ACTION REQUIRED` preamble + `# Previous Session Context` heading + `## Resume Plan`. |
| `03-self-restart-preamble` | Same loud-preamble path as 02 but driven against the COORD pane (covers thrum-501a.2 step 10.11). Coord's prime output is larger so this scenario adds explicit `wait_for_pane_idle` between assertions. |
| `04-fallback-paths` | Three sub-cases for `inject-prime-context.sh`'s degraded paths, each in its own `$BASE/fallback-XX/` cwd: 4A (no thrum binary on PATH → silent no-op), 4B (thrum present, no agent → historical nudge), 4C (daemon down → degraded prime output wrapped in briefing envelope; tracks thrum-br6t for the eventual hook fix). |
| `05-cross-session-identity` | Each pane resolves to its OWN registered thrum identity + role (coord → @test_coordinator_main/coordinator, impl → @test_implementer/implementer). Read-only re-assertion of the setup-time invariant under post-restart conditions (migrates `full_test_plan.md` § 8.1). |
| `06-cross-session-send` | Coordinator can send a thrum message addressed to a different agent in a different pane. Asserts the success-path JSON envelope's `message_id`. Body carries a RUNID-anchored marker that scenario 07 matches against (migrates § 8.2). |
| `07-cross-session-receive-reply` | Implementer's inbox shows the message scenario 06 sent (matched by marker), and impl can send a response message back to the coordinator with a distinct RUNID-anchored reply marker. Uses `thrum inbox --json` (not `--unread`) and `thrum send` (not `thrum reply <msg_id>`) so assertions are robust to claude's autonomous mark-as-read handling (migrates § 8.3). |
| `08-cross-session-confirm-receipt` | Coordinator's inbox shows the implementer's reply (matched by reply marker). Closes the bidirectional loop. Adds an explicit `wait_for_pane_idle 60` so claude's autonomous handling of the inbound nudge can settle before `!`-bash mode is engaged (migrates § 8.4). |
| `09-snapshot-save-cli` | `thrum tmux snapshot save` direct CLI: emits the canonical "Restart snapshot saved for …" success line, writes a non-empty file at `.thrum/restart/<agent>.md` with the canonical agent-name header, and stamps the supplied `--reason` into the body (migrates `full_test_plan.md` § 10G.1). |
| `10-snapshot-check-cli` | `thrum tmux snapshot check` returns exit 0 when a snapshot file exists for the calling agent. Asserts the success branch of the check contract (migrates § 10G.2). **Depends on scenario 09** (snapshot file from 09's save). |
| `11-snapshot-restore-cli` | `thrum tmux snapshot restore` outputs the snapshot markdown to stdout (matched by scenario 09's reason marker) AND consumes the file. A follow-up `thrum tmux snapshot check` then exits 1 — pinning the inverse branch of the check contract (migrates § 10G.3). **Depends on scenario 09** (snapshot file + exported `SNAPSHOT_SAVE_REASON` from 09's save). |
| `12-snapshot-save-no-session` | `thrum tmux snapshot save` against a registered-but-not-launched agent (sub-fixture: `thrum init` + `thrum quickstart` in `$BASE/no-session-snapshot/`, no `thrum tmux start`) errors with non-zero exit, surfaces a "no agent PID" / "no running agent" message, and writes NO file. Guards against silent / fabricated snapshots when there's no live conversation to capture (migrates § 10G.4). |
| `13-worktree-setup-thrum-redirect` | `thrum worktree create <name>` produces the canonical worktree-side .thrum scaffolding: `redirect` pointer to the main repo's .thrum/, plus per-worktree `identities/` and `context/` directories. Drives via the COORD pane (registered-agent caller, mirrors setup-repo.sh's pattern) and tears down the worktree at scenario end (migrates `full_test_plan.md` § 10B.1). |
| `14-worktree-setup-beads-redirect` | When the main repo has `.beads/`, `thrum worktree create` writes a `.beads/redirect` in the new worktree. Sub-fixture (`thrum init` + `thrum quickstart` + `mkdir .beads` in `$BASE/kafm7-14-beads-repo/`); driven via tmux-exec with `THRUM_NAME=$SUB_AGENT`. Sub-daemon stopped at end (au7k-class) (migrates § 10B.2). |
| `15-worktree-setup-no-beads` | Inverse of 14: when the main repo has NO `.beads/`, `thrum worktree create` does NOT write `.beads/` in the new worktree. Sub-fixture without `.beads/`. Sub-daemon stopped at end (migrates § 10B.3). |
| `16-worktree-setup-name-validation` | `thrum worktree create '../../../tmp/evil'` and `thrum worktree create 'path/with/slash'` both exit non-zero AND each error output contains "invalid" (4 assertions: exit + error message per probe). Pins the path-traversal / separator rejection contract (migrates § 10B.4). |
| `17-context-update-project` | Pre-seeds a minimal `.thrum/context/project_state.md` under `$COORD_REPO`, sends the `/thrum:update-project` slash command to the COORD pane via the `send_slash_command` drive.sh helper, and asserts the file is mutated within 240s (mtime advance OR size delta). Tests the real skill body's sub-agent + Edit chain — not a no-op recognition. The mutation invariant is asserted (not edit correctness) because the skill's edits depend on sub-agent prose judgment, which isn't deterministic enough to assert on (migrates § 9.1). |
| `18-context-show-saved` | Saves a marker via tmux-exec + `thrum context save --file --THRUM_NAME=test_coordinator_main`, then reads it back via the same out-of-pane pattern + `thrum context show --session`. Storage-layer round-trip smoke independent of any claude session, intentionally separate from scenario 17's slash-command coverage so a regression in either path is attributable (migrates § 9.2). |
| `19-precompact-hook` | Direct invocation of `claude-plugin/scripts/pre-compact-save-context.sh` with `env -u THRUM_HOME THRUM_NAME=test_coordinator_main` writes a `/tmp/thrum-pre-compact-test_coordinator_main-coordinator-all-*.md` backup whose body contains the expected `Git State` / `Beads State` / `Thrum Agent Status` sections. No claude involvement (migrates § 9.3). |
| `20-load-context-slash` | Sends `/thrum:load-context` to the IMPL pane via `send_slash_command`, then asserts a user message containing `<command-name>/thrum:load-context</command-name>` lands in the agent's JSONL — the slash-routing signature Claude Code emits when it recognizes a slash command. Pane is IMPL (not COORD per § 9.4) because scenario 17's heavyweight `/thrum:update-project` skill leaves COORD mid-render for 60-180s. The skill-body execution contract (slash → claude → `thrum prime` Bash tool call → rendered context) is delegated to scenario 21's post-restart `context-survives-restart-slash` sub-assertion, which fires against a fresh pane where claude doesn't optimize away the call. Spec drift around "/tmp backup display" remains tracked in thrum-eq6q (P3) (migrates § 9.4). |
| `21-context-persists-restart` | Two sub-assertions: (1) **storage-CLI** — save a marker into IMPL via `thrum context save --file`, restart IMPL via `thrum tmux restart impl --force`, wait for new SessionStart, assert marker readable via `thrum context show --session`; (2) **slash-chain** — send `/thrum:load-context` to the post-restart pane, assert claude invokes `thrum prime` AND its `toolUseResult.stdout` contains the marker. Closes the full save→restart→load-context recovery chain § 9.5 documents (migrates § 9.5). |
| `22-mcp-send-caller-id` | COORD sends marker-bearing message to @test_implementer; out-of-pane jq lookup against IMPL's inbox JSON asserts the message's `.agent_id == "test_coordinator_main"`. Routing-parity contract: MCP `thrum_send` and CLI `thrum send` translate to the same daemon path; non-empty/non-null caller-id stamping must hold for both (migrates § 5.1). |
| `23-mcp-inbox-filters-by-agent` | COORD sends marker-bearing message to @test_implementer; asserts (1) coord's inbox has 0 messages matching the marker (no cross-routing leak) AND (2) impl's inbox has ≥ 1 (delivered to addressee). Receiver-side check is out-of-pane via tmux-exec to avoid claude autonomous-handling races (migrates § 5.2). |
| `24-mcp-reply-routes-back` | Original (coord→impl) + reply (impl→coord) chain, all out-of-pane after the COORD send. tmux-exec composite extracts msg_id from impl's inbox JSON file, runs `thrum reply` with that id, then polls coord's inbox for the reply marker. Exercises the `thrum reply` daemon RPC distinctly from scenarios 06-08 (migrates § 5.3). |
| `25-mcp-waiter-broadcast` | Two sub-assertions: (1) IMPL's `thrum wait --timeout 12s --json` (fired fire-and-forget) returns `"status": "received"` after COORD sends `--to @everyone` broadcast; (2) the broadcast marker actually lands in impl's inbox (defends against a "wait unblocked on some unrelated message" false positive). Receiver-side inbox check is out-of-pane (migrates § 5.4). |
| `26-mcp-list-agents-id` | `thrum agent list --json` (driven from COORD) returns 0 agents with empty/null agent_id AND both fixture identities (test_coordinator_main + test_implementer) are present. JSON shape: `.agents.agents[]` (per spec note: no "data" wrapper) (migrates § 5.5). |
| `27-mcp-serve-no-crash` | `thrum mcp serve` invoked via `tmux-exec --timeout 5 --clean` with `THRUM_NAME` pinned; captures stdout+stderr to a temp file. Two assertions: no Go runtime `panic:` header, no `fatal error:` header. Behavioral correctness of the stdio MCP protocol is out of scope; the test pins the no-crash invariant § 5.6 documents (migrates § 5.6). |
| `28-message-read-all` | Drain → COORD sends a marker via tmux-exec (out-of-pane) → IMPL `thrum message read --all` exits 0 with either "✓ Marked N messages as read" or "No unread messages." → IMPL `thrum inbox --unread --json` shows `.messages | length == 0`. Pre-condition is the read-all-then-no-unread chain, robust to claude's autonomous inbox handling (migrates § 4.1). |
| `29-send-unknown-recipient` | `thrum send "..." --to @ghost_agent` against an unregistered recipient exits non-zero AND output contains "unknown recipient". Driven via tmux-exec out-of-pane to preserve the process exit code (driving through a claude pane wraps the error in bash-stderr tags without the rc) (migrates § 4.2). |
| `30-roles-template-management` | `thrum roles list` reports both empty ("No role templates found") and populated states correctly. Creates a template under `.thrum/role_templates/` keyed to a non-fixture name to avoid colliding with subsequent scenarios; cleans up at end (migrates § 4.4). |
| `31-daemon-restart-port` | Sub-fixture (`$BASE/kafm1-31-restart`) — `thrum daemon restart` exits 0, status reports running, and `.thrum/var/ws.port` is identical before/after. au7k discipline: sub-daemon stopped at scenario end (migrates § 4.5). |
| `32-purge-preview-execute` | Sub-fixture covers four flag-/parser-level contracts: bare `purge` errors with "either --before or --all is required"; `--before 2d --all` errors with "mutually exclusive"; `--before 1s` previews with "Run with --confirm" hint; `--before <ISO>` parses successfully. The execute-and-verify mutation path (--confirm + re-register + counts==0) is intentionally narrowed — well-covered by daemon RPC unit tests; sub-fixture re-bootstrap mid-scenario adds little regression value (migrates § 4.6, partial). |
| `33-git-sync-async-branch` | Pure git read against `$COORD_REPO`: `a-sync` branch exists, has at least one commit, and `git show a-sync:events.jsonl` returns non-empty content. Read-only — no daemon involvement (migrates § 4.7). |
| `34-daemon-logs` | `thrum daemon logs` with four flag combos (`--lines 5`, `--since 1m`, `--lines 0`, `--lines 3 --since 1h`) all exit 0 against the run-level fixture's daemon. Output content (timestamps/levels) is not strictly asserted — slog format isn't a stable contract surface; the exit-code shape is § 4.8's documented contract (migrates § 4.8). |
| `35-single-agent-mode` | Sub-fixture toggles `thrum single-agent-mode` round-trip: bare reports current; `true` enables; bare reports "enabled"; `false` disables; bare reports "disabled (multi-agent)". Sub-fixture isolation is mandatory — toggling the run-level daemon would silently break subsequent multi-agent messaging scenarios (migrates § 4.9). |
| `36-roles-deploy` | Sub-fixture creates a `.thrum/role_templates/implementer.md` template, runs `roles deploy --dry-run` (asserts "Dry run — no files written"), then live deploy (asserts "Updated N/M agents" — the markdown spec's older "Deployed preamble for ..." string was retired; assertion accepts both forms). Verifies preamble file exists, content has agent name + no raw `{{ }}` markers, and `--agent <name>` targeted deploy exits 0 (migrates § 4.10). |
| `37-config-show` | Read-only: `config show` (human form) emits "Thrum Configuration" header + Runtime/Daemon section headers + Primary line; `config show --json` parses as valid JSON with non-null `runtime.primary` (migrates § 4.11). |
| `38-runtime-management` | Sub-fixture exercises `runtime list` (lists known presets), `runtime show claude` (success), `runtime show unknown-rt-xyz` (non-zero error), `runtime set-default codex` (success), and confirms `config show --json runtime.primary == "codex"` after the set-default mutation. Sub-fixture isolates the config-write so the run-level fixture's primary stays pinned to claude (migrates § 4.12). |
| `39-context-commands` | Sub-fixture: `context preamble` (read), `context preamble --init` (writes default), `context sync` (accepts success path OR known sparse-checkout error tracked in thrum-nt4c), `context clear` (mutates → exit 0). Sub-fixture isolation prevents the clear from affecting scenarios that depend on coord/impl context (migrates § 4.13). |
| `40-init-flags` | Scratch repo at `$BASE/kafm1-40-init-scratch`: `thrum init --dry-run --runtime claude` exits 0 AND does NOT create `.thrum/`; then `thrum init --stealth --runtime claude` exits 0, leaves `.gitignore` untouched (no `.thrum` line), AND adds `.thrum` to `.git/info/exclude`. Pinned `--runtime claude` on dry-run avoids the interactive runtime prompt (migrates § 4.14). |
| `41-daemon-local-flag` | Sub-fixture path: `thrum init` (auto-starts daemon) → stop → `daemon start --local` → assert ws.port populated + status reports running. Sub-fixture mandatory because spec § 4.15 explicitly stops the main-repo daemon as a step; replacing that with sub-fixture stop+start preserves the run-level fixture's daemon (migrates § 4.15). |
| `42-quickstart-flags` | Sub-fixture: `quickstart --no-init --force` exits 0 AND output does NOT contain runtime-config sentinel lines (`✓ .claude/settings.json`, `✓ scripts/thrum-startup.sh`); `quickstart --preamble-file <file> --force` exits 0 AND `context preamble` afterwards contains the custom marker from the supplied preamble file (migrates § 4.16). |
| `43-worktree-flags` | Driven via tmux-exec out-of-pane with `THRUM_NAME=test_coordinator_main` pinned (mirrors scenario 14's auth pattern). `worktree create --branch <custom>` exits 0 and the new worktree's HEAD branch matches the custom name; `worktree create --detach` exits 0 and `git branch --show-current` returns empty (detached HEAD). Both worktrees + the dangling `--branch` branch are torn down at scenario end (migrates § 4.17). |

## Output

Each assertion emits one line with status tag + scenario-id /
assertion-name. Failures stream inline with an indented `→` detail block
AND get repeated in the end-of-run `SUMMARY` block.

```text
[PASS] 01-session-start-injection / briefing-header
[FAIL] 01-session-start-injection / old-nudge-absent
       → expected: bash-stdout starting with 'FAILED old_nudge_absent'
       → got:      VERIFIED old_nudge_absent (1 hits in SessionStart:startup)
       → file:     scenarios/01-session-start-injection.test.sh:18
```

`tail -f run.log` shows failures inline. `grep '^\[FAIL\]' run.log` gives
a clean post-run failure list with file:line references.

Exit codes: `0` on all-pass, `1` on assertion failures, `2` on setup
failure (no scenarios attempted).

## Author a new scenario

1. Create `scenarios/NN-name.test.sh` (numeric prefix; explicit ordering).
2. Top-of-file preamble explaining what's verified, why it matters,
   fixture mutations (if any), and whether it's safe to re-run after
   another scenario.
3. Source nothing — `run.sh` already sourced `helpers/all.sh` before
   sourcing your scenario. You have access to: `send_command`,
   `assert_jsonl`, `wait_for_jsonl_match`, `wait_for_session_start`,
   `wait_for_pane_idle`, `wait_for_bash_stdout_contains`,
   `send_bash_and_wait`, `emit_pass` / `emit_fail` / `emit_skip`, plus
   env vars `COORD_PANE`, `IMPL_PANE`, `COORD_REPO`, `IMPL_REPO`,
   `BASE`, `WORKTREE_BASE`, `REPO`, `RUNID`, `THRUM_RELEASE_REPO_ROOT`.
4. Default to driving the IMPL pane for state-mutating tests; coord
   stays stable as the driver surface.
5. **Use distinct `check-context-value.sh <tag>` arguments per
   invocation, not just per scenario.** `assert_jsonl` reads the JSONL
   from the beginning and matches the first hit, so a repeated tag from
   earlier in the same session would match a stale entry. If a scenario
   needs to assert a needle twice, name them `foo_pre` / `foo_post`,
   not the same `foo` both times.
6. **Driver-side `thrum` calls must wrap through `tmux-exec`.** The
   plain `thrum` CLI walks the calling process's PID tree to derive
   identity; from inside the runner's bash tool the walk hits the
   parent runtime and adopts the wrong agent / fires cross-worktree
   guards. Wrap with `"$THRUM_RELEASE_REPO_ROOT/scripts/tmux-exec" exec
   --cwd "$REPO" --clean -- thrum <args>` so the call runs in an
   ephemeral pane whose ancestry chain ends at the tmux server.

## Helpers cheat sheet

`helpers/drive.sh` and `helpers/assert.sh` provide everything a scenario
should need; reach for these before reinventing.

| Helper | When to use |
|--------|-------------|
| `send_command <pane> <text>` | Type into a tmux pane. Splits `!`-prefix discretely + adds the post-text Enter delay so bash-prefix mode triggers reliably. |
| `send_bash_and_wait <pane> <repo> <cmd> <expected> [timeout]` | Send `!` bash + wait for a `<bash-stdout>` entry containing `<expected>`. Use this whenever you'd otherwise hand-roll a `wait_for_jsonl_match` over a typed bash result. |
| `assert_jsonl <pane> <repo> <sid> <name> <expected> [loc]` | Wait up to 30s for a `<bash-stdout>` OR `<bash-stderr>` entry whose region starts with `<expected>`. Emits PASS/FAIL. Use after every `!`-prefix assertion send. |
| `wait_for_session_start <repo> [timeout]` | Block until the FIRST SessionStart attachment lands. Required when a fresh cwd has no JSONL yet — `check-context-value.sh` ERRORs with "no project dir" otherwise. |
| `wait_for_pane_idle <pane> [seconds]` | Block until pane render stabilizes (≥1s of identical capture-pane hashes). Use BETWEEN sends in panes whose response is large (e.g. coord pane post-prime — `! cmd` rendering can exceed `send_command`'s default 10s idle gate, which leaks the next keystroke into mid-render). |
| `wait_for_jsonl_match <repo> <jq-filter> [timeout]` | Generic JSONL poller. Use for assertions that don't fit the bash-stdout shape. |
| `wait_for_bash_stdout_contains <repo> <substring> [timeout]` | Specialization of the above for `<bash-stdout>` substring matches. |
| `spawn_sub_fixture_claude <tmux-name> <cwd> [launch-cmd]` | Spawn a non-thrum-managed claude pane in a fresh cwd. Handles the first-time-cwd "trust this folder?" dialog automatically (wait_for_pane_idle gates the confirming Enter). Optional `launch-cmd` (default `"claude"`) overrides the launch invocation, e.g. for PATH-stripped variants. |
| `kick_session_then_wait <pane> <cwd> [timeout]` | Force a fresh claude pane to flush its first JSONL before any `assert_jsonl`. Sends `! true` and waits for the SessionStart attachment, so subsequent helpers don't ERROR with "no project dir at …". |

## Spawning sub-fixture claude panes (scenario 04 pattern)

Scenarios that need additional claude panes BEYOND the run-level
coord/impl (e.g. fallback-path tests in their own cwds) should use
the `spawn_sub_fixture_claude` + `kick_session_then_wait` helpers
rather than `thrum tmux create`. `thrum tmux create` enforces a
worktree-identity guard and rejects calls that target directories
outside the repo's worktree set ("caller pane belongs to a different
worktree" error).

```bash
spawn_sub_fixture_claude "$tmux_name" "$cwd"
kick_session_then_wait "$tmux_name" "$cwd"
# Now safe to run real assertions via send_command + assert_jsonl…
```

For PATH-stripped variants (e.g. scenario 04A's no-thrum-binary case),
pass a custom launch command:

```bash
spawn_sub_fixture_claude "$tmux_name" "$cwd" \
  "env PATH='$pathdir:/usr/bin:/bin' claude"
```

If the sub-fixture launches claude with a **PATH stripped of homebrew
bash**, the `! cmd` subshell will fall back to `/bin/bash` 3.2 on
macOS — which doesn't have `mapfile` and other bash 4+ builtins. Keep
helper scripts that run inside such subshells bash 3.2 compatible
(use `while read` loops in place of `mapfile`).

## Debugging a failed scenario

The fixture lives at `$HOME/.thrum_release_tests/$RUNID/`. By default
`run.sh` removes it via `run_teardown` on EXIT. To inspect after a
failure:

```bash
THRUM_RELEASE_NO_TEARDOWN=1 ./tests/release/run.sh
# After failure, inspect:
tmux attach -t coord
tmux attach -t impl
ls "$HOME/.thrum_release_tests/" | tail -1   # latest RUNID
ls "$HOME/.thrum_release_test_worktrees/" | tail -1
# Manual cleanup when done:
tmux kill-session -t coord; tmux kill-session -t impl
ps -eo pid,command | grep "thrum.*daemon.*thrum_release" \
  | grep -v grep | awk '{print $1}' | xargs -r kill
rm -rf "$HOME/.thrum_release_tests"/* "$HOME/.thrum_release_test_worktrees"/*
```

The runner's preflight cleanup at the start of `run_setup` will also
nuke leftover `coord`/`impl` tmux sessions and `thrum_release_tests`
daemons before the next run, so a forgotten `NO_TEARDOWN` won't block
subsequent runs.

## Tools required

`thrum`, `tmux`, `jq`, `git`, `claude` — all in `PATH`. Plus the
executable `scripts/check-context-value.sh` and `scripts/tmux-exec` in
this repo. The pre-release thrum claude-plugin must be installed (see
`dev-docs/prompts/release-test-framework-continuation.md` § 13 for the
plugin install flow).

### `scripts/check-context-value.sh`

Greps SessionStart hook attachments in the current pane's Claude Code
JSONL for a literal needle. Emits one of:

- `VERIFIED <tag> (<n> hits in <hook>)` — exit 0
- `FAILED <tag> (0 hits in <hook>)` — exit 1
- `ERROR <tag> (<reason>)` — exit 2

```bash
check-context-value.sh [--source=any] <test_tag> <needle> [hook_name]
```

`--source=any` (Phase 2) skips the cwd-encoded project lookup and scans
every JSONL under `~/.claude/projects/*/`. Useful for panes whose cwd
doesn't encode to a thrum project dir. **Unsafe for negative
assertions during full release-test runs** because the run-level
coord/impl panes also produce briefing-bearing SessionStart attachments
that would false-positive a "no briefing here" scan — prefer unique
sub-case cwds with default cwd-mode for negative assertions.

**Manual smoke** (run from any thrum-initialized cwd; mirrors the
verification steps in plan ns73.1 §§ 4-6):

```bash
# Default mode — regression sanity, output depends on the current pane:
./scripts/check-context-value.sh smoke_default "# Thrum Session Briefing" SessionStart:startup

# --source=any positive case — should report N≥1 hits across project dirs:
./scripts/check-context-value.sh --source=any smoke_any "Thrum Session Briefing" SessionStart:startup

# --source=any negative case — should report 0 hits:
./scripts/check-context-value.sh --source=any smoke_any_neg "this_string_should_match_nothing_xyzzy_42" SessionStart:startup
```

### `scripts/tmux-exec`

Wraps a thrum CLI invocation in an ephemeral tmux pane so the call's
PID ancestry chain ends at the tmux server, not at the runner's
parent runtime. Required for any driver-side `thrum` call (see § 6 in
"Author a new scenario" above).

## NOT part of `make ci`

Release tests run on demand via `make release-tests` (or directly via
`tests/release/run.sh`). They spawn real claude processes; wall time
and API cost both make CI integration impractical. See spec § 8.3.
