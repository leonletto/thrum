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
| `45-queue-setup-no-agent-rejected` | Sets up the shared queue-test fixture (worktree + agent-registered tmux session running the SHELL runtime) used by scenarios 46-49, AND pins the daemon's --no-agent rejection contract: a sibling `--no-agent` session rejects `thrum tmux queue` with "no registered agent" / "queue requires" (migrates § 10E.1). **Scenarios 46-49 depend on this fixture; scenario 49 tears it down on the happy path; `helpers/teardown.sh` has a defensive fallback for partial-failure paths.** |
| `46-queue-sync-wait-mode` | `thrum tmux queue --wait` blocks until the command reaches a terminal state, returns the captured output (payload + "completed"), AND auto-suppresses the @system completion message (notify_on_complete=false in --wait mode). Compares pre/post @system inbox count to assert suppression (migrates § 10E.2). **Depends on scenario 45.** |
| `47-queue-async-system-delivery` | Async `thrum tmux queue` (no --wait) returns a `cmd_xxx` id synchronously; the captured output later arrives as an @system message in the requester's inbox containing both the command's payload and "completed" (migrates § 10E.3). **Depends on scenario 45.** |
| `48-queue-cancel-flow` | `thrum tmux cancel <cmd_id>` exits 0, the queue empties, and the requester gets an @system cancellation message containing the partial output captured BEFORE cancel (proves `HandleCancel`'s partial-capture path at `queue_rpc.go:627` works end-to-end) (migrates § 10E.4). **Depends on scenario 45.** |
| `49-queue-daemon-restart-recovery` | An active queue command (in StateSent) becomes `interrupted` after `thrum daemon restart`, and the requester gets an @system message mentioning both 'interrupted' and 'restart'/'daemon' (exercises `RecoverQueueState` on daemon startup). Also performs the kafm.10 fixture teardown — kills `queue-test` session + tears down the queue-test worktree (migrates § 10E.5; absorbs the deferred § 10E.6 cleanup). **Depends on scenario 45.** |
| `51-claude-tmux-sessions-created` | `thrum tmux status --json` (captured via `capture_thrum_json`) reports both `coord` and `impl` sessions as state=alive. Pin the daemon-side bookkeeping that setup-repo.sh's whoami probes don't directly cover (migrates § 7.1). |
| `52-claude-launched-in-panes` | Both COORD and IMPL panes have written claude JSONL transcripts (SessionStart attachments present in each project dir). Catches claude-launch regressions (broken `claude` shim, CLAUDECODE env leak, trust-dialog stalls) that would slip past 51's daemon-side check (migrates § 7.2). |
| `53-session-start-hook-both-panes` | Pari ty assertion to scenario 01: SessionStart hook auto-injects the "# Thrum Session Briefing (auto-loaded)" header AND `Agent: @` identity into the IMPL pane's attachment too — guards against impl-side regressions (e.g. worktree-redirect breaking the hook lookup) that 01's coord-only check would miss. Also re-asserts the coord briefing under a distinct tag (migrates § 7.3). |
| `54-slash-prime-routing` | `/thrum:prime` sent to COORD via `send_slash_command` produces a `<command-name>/thrum:prime</command-name>` user message in COORD's JSONL. Routing-only (skill-body execution covered by scenario 21's slash-chain sub-assertion). floor_ts scope guards against false-matching the setup-time auto-prime (migrates § 7.4). |
| `55-slash-inbox-routing` | Out-of-pane impl→coord pre-send + `/thrum:inbox` slash to COORD; routing tag `<command-name>/thrum:inbox</command-name>` lands in COORD's JSONL. Routing-only (model-eagerness rationale from scenario 20). Marker pre-seed guards against a routing regression that only surfaced on non-empty inbox (migrates § 7.6). |
| `56-nl-send-tool-use` | NL prompt "send a thrum message to @test_implementer saying ..." into COORD makes claude shell out via Bash to `thrum send`. assert_tool_use_bash polls JSONL for the assistant tool_use entry. Spec § 7.7 heading vs body drift documented in-file (heading is "Test /thrum:send" but body drives NL — same drift as § 9.1 / scenario 17). |
| `57-slash-overview-routing` | `/thrum:overview` slash to COORD produces routing tag `<command-name>/thrum:overview</command-name>`. Routing-only — skill body composes whoami + team + inbox + status which is non-deterministic under model eagerness (migrates § 7.8). |
| `58-nl-reply-tool-use` | Pre-deliver impl→coord, resolve msg_id from coord's inbox JSON via `capture_thrum_json`, then NL prompt "reply to message <id> with ..." into COORD; assert claude invokes Bash with "thrum reply" substring. Two assertions: preseed-msg-id-resolved + claude-invokes-thrum-reply. Daemon-RPC contract for reply lives in scenario 24; this scenario's value is the NL→tool_use chain (migrates § 7.9). |
| `59-nl-who-has-tool-use` | Out-of-pane dirty IMPL_REPO/README.md, then NL prompt "run thrum who-has README.md and tell me who is editing it" into COORD; assert "thrum who-has" tool_use Bash. Cleanup reverts README.md so subsequent scenarios see a clean impl repo (migrates § 7.10, video S7 demo). |
| `60-nl-set-intent-tool-use` | NL prompt "update my thrum intent to: ..." into COORD; assert tool_use Bash with substring "set-intent" (relaxed from full "thrum agent set-intent" path because claude's bash invocation may include intermediate whitespace / wrappers that bury the full command — set-intent is distinctive enough alone). Timeout 120s due to longer NL→action latency observed empirically (migrates § 7.11, video F4 demo). |
| `61-nl-team-tool-use` | NL prompt "show me the thrum team status" into COORD; assert "thrum team" tool_use Bash. Closes coverage for the deleted-because-redundant § 7.5 (/thrum:team slash variant) — together with setup-repo's whoami probes and scenario 26 (`thrum agent list --json`), the team-listing surface is fully covered (migrates § 7.12, video F2 demo). |
| `62-multi-runtime-launch-explicit` | Sets up the shared rt-scratch fixture (worktree under `$WORKTREE_BASE/repo/rt-scratch`) used by scenarios 65/66/68, AND pins tier 1 of the runtime resolution chain: `thrum tmux launch runtime-test --runtime shell` exits 0 against a `--no-agent` managed session and `thrum tmux status --json` reports state=alive (migrates § 10C.1). Runtime field on the status row intentionally not pinned (thrum-rfn4 P3 inconsistency, same precedent as scenario 45). **Scenarios 65/66/68 depend on this fixture; scenario 68 tears down rt-scratch on the happy path; `helpers/teardown.sh` has a defensive fallback for partial-failure paths.** |
| `63-multi-runtime-preferred-runtime-quickstart` | Sub-fixture (`$BASE/kafm8-63`) — `thrum quickstart --runtime opencode` writes `preferred_runtime=opencode` to the agent identity JSON (`.thrum/identities/<agent>.json`). On-disk source for tier 2 of the runtime resolution chain. au7k discipline: sub-daemon stopped at scenario end (migrates § 10C.2). |
| `64-multi-runtime-resolution-chain` | Sub-fixture (`$BASE/kafm8-64` + `$BASE/kafm8-64-wt`) exercises tier 2 of the runtime resolution chain: with no `--runtime` flag and an identity whose `preferred_runtime=shell`, `thrum tmux launch` produces stdout containing the literal "shell" (matches the daemon's "Launched shell in session …" success line). Status-row runtime field intentionally not asserted — same thrum-rfn4 caveat as scenarios 45/62. au7k discipline: sub-daemon stopped at scenario end (migrates § 10C.3). |
| `65-multi-runtime-invalid-name-rejected` | Pins runtime-name validation: `thrum tmux launch invalid-rt-test --runtime "rm -rf"` exits non-zero AND error output contains "invalid" (case-insensitive). Two assertions (exit + message) cover both contract surfaces (migrates § 10C.4). **Depends on scenario 62.** |
| `66-multi-runtime-prime-varies-by-runtime` | Pins auto-prime conditional on runtime: after `thrum tmux launch prime-rt-test --runtime shell` and a 3s settle, `thrum tmux capture --lines 100` contains 0 occurrences of "thrum:prime" or "thrum-prime". Uses `thrum tmux capture` (daemon-aware) rather than raw `tmux capture-pane` so the assertion is robust to the daemon's tmux-socket choice (migrates § 10C.5). **Depends on scenario 62.** |
| `67-multi-runtime-tmux-start-detached-worktree` | Per-scenario detached-HEAD worktree (`test-tmux-start`, `--detach`). `thrum tmux create + launch shell` exits 0 against the detached worktree AND `thrum tmux status --json` reports state=alive. The "tmux start" framing in markdown § 10C.7 is narrative — `thrum tmux start` itself is attach-blocking; this scenario migrates the underlying create+launch+alive contract on a detached worktree (migrates § 10C.7). |
| `68-multi-runtime-no-agent-bare` | Pins `--no-agent` semantics: a bare-session created with `--no-agent` does NOT register an agent (`thrum team --json` contains 0 agents matching the session name OR rt-scratch worktree path), launch shell exits 0, and `thrum tmux status --json` reports alive. **Tears down the rt-scratch shared fixture at scenario end** — last user of it in the kafm.8 batch (migrates § 10C.8; absorbs § 10C.9a cleanup). **Depends on scenario 62.** Authorized narrowing: § 10C.9 (interactive `thrum tmux connect`) is closed as superseded-by-manual-only — cannot be automated via tmux-exec. |
| `69-multi-runtime-restart-force` | Per-scenario branch-backed worktree (`test-force-restart`, `--branch feature/test-force-restart`) + agent-registered managed session (`force_agent`, role tester). The agent-registered tmux create is driven from `COORD_PANE` via `send_bash_and_wait` rather than tmux-exec — daemon's identity guard refuses inline-registration creates from tmux-exec ephemeral callers. After launch+settle, `thrum tmux restart force-restart-test --force` exits 0 and `thrum tmux status --json` reports state=alive within 10s — pins the `--force` skip-graceful-shutdown contract. Worktree shape changed from spec § 10C.10's `--detach` to `--branch` (orthogonal to the restart contract; see scenario header for rationale). (migrates § 10C.10) |

**§ 10C.6 OpenCode end-to-end** is closed as P3-deferred (OPTIONAL per markdown spec; requires opencode binary; not migratable in framework runs that don't gate on opencode availability). **§ 10C.9 tmux connect** is closed as superseded-by-manual-only (interactive attach can't be driven via tmux-exec).
| `81-agent-set-status-local` | `thrum agent set-status working` (no --agent) prints "✓ Status set to working", and the caller's identity file (`.thrum/identities/<agent>.json`) shows `agent_status="working"` plus a non-empty `agent_status_updated_at` timestamp. Driven via COORD pane `!`-bash so THRUM_NAME resolves to the pane's identity (test_coordinator_main); pure local file write that doesn't go through the daemon. Resets to idle on every exit path so coord's setup-time invariant survives the scenario (migrates `full_test_plan.md` § 10D.1). |
| `82-agent-set-status-remote` | `thrum agent set-status working --agent test_implementer` prints "✓ Status for test_implementer set to working" (daemon-RPC path, distinct format from 81's local write), and impl's identity file shows `agent_status="working"`. Uses the run-level test_implementer's live agent_pid so the daemon's live-target guard is satisfied without spinning up a sub-fixture. Resets to idle on every exit path so scenarios 28/91 see the fixture's idle baseline (migrates § 10D.2). |
| `83-agent-set-status-invalid` | `thrum agent set-status bogusstate` exits non-zero (literal `exit: 1`) AND error output contains "must be" — pinning the canonical-set rule (working/idle/blocked). No identity mutation; CLI rejects before any write so no cleanup needed (migrates § 10D.3). |
| `84-agent-set-status-nonexistent` | `thrum agent set-status working --agent ghost_agent_<RUNID>` exits non-zero AND error output contains "not found". Ghost name is RUNID-anchored to defend against a stale registration from a crashed prior run accidentally satisfying the lookup (migrates § 10D.4). |
| `85-worktree-create-thrum-beads-redirects` | Sub-fixture (`$BASE/kafm9-85-repo`) with both `.thrum/` AND `.beads/` directories: `thrum worktree create test-orchestrator` (no `--branch`) lands a worktree with all four canonical artifacts in one shot — `.thrum/redirect`, `.beads/redirect`, `.thrum/identities/`, AND default branch `feature/test-orchestrator`. Combined-shape contract distinct from scenarios 13+14 which split the redirect contracts into single-purpose probes; this catches a regression where the create writes one redirect but bails before the other. au7k discipline: sub-daemon stopped at scenario end (migrates § 10D.5). |
| `86-worktree-list-shows-agent` | `thrum worktree create kafm9-86-orch` from COORD pane → inline-register `kafm9_86_orch_agent` (role orchestrator) via `thrum tmux create` from COORD pane (caller-identity guard, scenario 69 pattern) → `thrum worktree list --json` (captured via `capture_thrum_json`) contains a row with `branch="feature/kafm9-86-orch"` AND `.agent == "kafm9_86_orch_agent"`. Pins the read-side projection that maps each worktree's `.thrum/identities/*.json` into the row's agent field. Per-scenario teardown of session+worktree (migrates § 10D.6). |
| `87-worktree-create-path-traversal` | `thrum worktree create '../../../tmp/evil'` AND `thrum worktree create '../../escape'` both exit non-zero AND each error output contains "invalid" (4 assertions). Distinct from scenario 16 (10B.4) which combined deep-traversal with `path/with/slash`; this scenario pins the depth-invariance of the validator with a shallower-traversal probe. No fixture mutation; CLI rejects before any write (migrates § 10D.7). |
| `88-worktree-teardown-cleans-up` | Per-scenario create + inline-register agent + kill tmux session + `thrum worktree teardown kafm9-88-orch` exits 0; teardown stdout contains "Worktree kafm9-88-orch removed" AND "Removing identity" (the identity-removal announcement); the worktree directory genuinely no longer exists on disk after teardown completes. Pins the explicit success shape § 10D.8 documents — distinct from the implicit teardown-as-cleanup paths in scenarios 13/14/85/86. Self-contained per-scenario fixture; defensive cleanup at end (migrates § 10D.8). |
| `89-config-keys-init` | Sub-fixture (`$BASE/kafm9-89-repo`) — fresh `thrum init` populates `.thrum/config.json` with non-empty `worktrees.base_path`, `orchestration.merge_target`, AND `orchestration.default_autonomy`. Sub-fixture is mandatory because setup-repo.sh patches `worktrees.base_path` post-init to point at $WORKTREE_BASE; reading the run-level fixture's config would assert the patched state, not the init-time contract. Sub-daemon stopped at end (migrates § 10D.9). |
| `90-orchestrator-quickstart-role` | Sub-fixture (`$BASE/kafm9-90-repo`) — `thrum quickstart --role orchestrator` exits 0 AND `thrum team --json --all` (captured via `capture_thrum_json`) contains a member with `agent_id=kafm9_90_orch` AND `role="orchestrator"`. Round-trip pin for the role-string write→read for the orchestrator role specifically (the new role added in this release cycle). au7k discipline: sub-daemon stopped at end (migrates § 10D.10). |
| `91-check-pane-auto-nudge` | Per-scenario branch-backed worktree + inline-registered nudge_test_agent + shell runtime → `thrum agent set-status working --agent kafm9_91_nudge_agent` (RPC path) → `thrum tmux check-pane kafm9-91-nudge` fires the daemon's working_but_idle dispatch → tmux capture-pane against the nudge target shows "New message from @daemon" within 10s. CLI output not used as anchor (CLI silently discards CheckPaneResponse); the pane-side nudge text is the only externally observable signal of the working_but_idle branch firing. Per-scenario teardown of session+worktree (migrates § 10D.11). § 10D.12 (cleanup) is deferred to framework teardown — handled by `helpers/teardown.sh`'s defensive sweep.

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
