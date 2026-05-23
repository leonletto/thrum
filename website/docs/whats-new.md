---
title: "What's New"
description:
  "Release-by-release highlights, breaking changes, and migration notes for
  recent Thrum versions."
category: "overview"
order: 2
tags: ["release-notes", "changelog", "migration", "version"]
last_updated: "2026-05-23"
---

## What's New

This page tracks the user-visible changes in recent Thrum releases — highlights,
breaking changes, and anything that needs attention when you upgrade. The full
machine-readable history lives in
[CHANGELOG.md](https://github.com/leonletto/thrum/blob/main/CHANGELOG.md).

## v0.10.6 — In Soak (RC)

v0.10.6's headline is a **sync re-architecture** (thrum-s6os): the cross-machine
wire stream now derives from per-agent and per-bridge-group state files rather
than from a synced event journal. The 60-second polling ticker is gone — sync is
event-triggered on a structural-event whitelist (`agent.register`, `group.*`,
`message.create`). Idle daemons produce zero commits on `a-sync`, and busy
multi-agent clusters stop accumulating heartbeat noise; the falcon-backend
11K-commits-per-week stream becomes a trickle proportional to actual message
volume.

⚠ **Mixed-cluster upgrade warning.** v0.10.6 writes ONLY to the new
`messages-v2/<id>.jsonl` path; v0.10.5 peers don't know about it and will
silently miss messages authored by upgraded peers until they upgrade. v0.10.6
peers retain a legacy-read fallback for v0.10.5-authored messages, so upgraded
peers stay fully functional in mixed clusters — but the asymmetry means you
should **upgrade every peer in one window** rather than running mixed long-term.

Other notable v0.10.6 changes: pre-migration DB backups are now timestamped
(`.pre-migration-v<N>-<UTC>.bak`) and a failed backup halts the migration so you
can't end up without a recovery snapshot; an RC-tag plugin distribution scheme
(per-RC `X.Y.Z-rc.N` suffix on `plugin.json` + `marketplace.json`) lets
`/plugin update` upgrade Claude Code plugins cleanly through the rc pipeline
with no uninstall/reinstall; two new daemon config keys
(`events_retention_days`, `compaction_size_threshold_mb`) tune the local
events-journal window and messages-v2 compaction; a schema forward-port to v36
keeps a v0.10.6 binary openable on DBs touched by v0.11-substrate work
(intentionally dead-end here — no consumer code reads them); and the skill
review loop gained a `verify-against-source` prose-conformance reviewer plus a
Phase 0 review gate in `project-setup`. The `daemon.sync_interval` config key
and `DefaultSyncInterval = 60` constant are removed; legacy configs are silently
ignored.

See the [Beta Channel](beta-channel.md) guide for how to opt in. Stable
promotion follows the standard 48-hour soak window once no P0/P1 bugs are open
against the RC.

## v0.10.5 — 2026-05-21

v0.10.5 shipped to stable on 2026-05-21 after eight RCs. The latest tag was
`v0.10.5-rc.6`, which adds a `--from` filter to `thrum inbox`, a daemon-side
backstop nudger that replaces the user-side cron pattern for stale-unread
re-delivery, a `--delete-branch` flag for `thrum worktree teardown`, an expanded
downgrade-guard error with actionable recovery hints + a new "Multi-Binary
Worktree Footgun" section in CLAUDE.md, a self-echo regression fix for the tmux
nudge dispatcher (`thrum-1zfk`), and a schema forward-port to v32 so the binary
can open DBs previously touched by v0.11-substrate work (intentionally dead-end
on v0.10.5 — no consumer code). The `/thrum:restart` skill now mandates a §1 Big
picture section and follows an 11-section numbered structure so snapshots double
as searchable session log entries.

rc.7 (in soak) adds: `thrum monitor stop`, `show`, `logs`, and `restart` now
accept the monitor's human-readable name in addition to the ULID-style ID — use
the name shown in `thrum monitor list` instead of hunting for the ID.
`thrum monitor stop` reliability is improved: the RPC no longer blocks the
daemon's critical-path handler, and child processes are killed as a process
group so shell wrappers can't outlive the stop command. `thrum init` now
JSON-merges hook entries into an existing `.claude/settings.json` rather than
skipping the file when it already exists; the `bd setup claude` hook is
auto-installed (and kept current) whenever `bd` is on PATH. The Class B/C
cross-worktree diagnostic banner is now wired uniformly across all eight
daemon-management leaves (status/logs/start/stop/restart/run, backup status,
telegram status).

See the [Beta Channel](beta-channel.md) guide for how to opt in. Stable
promotion follows the standard 48-hour soak window once no P0/P1 bugs are open
against the RC.

## v0.10.4 — 2026-05-16

Fixes a P1 cross-worktree-identity-drift bug where mutating CLI commands
(`thrum send`, `thrum reply`, `thrum inbox`, etc.) silently swallowed the guard
error and proceeded — including silent wrong-author sends and destructive
wrong-agent auto-marks-as-read. The bug surfaced in the wild on 2026-05-16 when
an architect's review was attributed to the implementer it was reviewing.
Standard same-cwd flow is unchanged.

The cross-worktree guard response is now classified per verb on an orthogonal
`cross_worktree_response` axis:

- **Class A — Abortable**: mutating + identity-filtered commands (send, reply,
  inbox, message edit/delete/mark-read/mark-unread, session start/end, agent
  register, etc.) fail closed with exit 1 + a 4-line guard error.
- **Class B — DiagnosticBanner**: identity-agnostic diagnostics (team, agent
  list, version, daemon status, peer/sync/backup/telegram/tmux status) exit 0
  with normal stdout and a one-line `⚠ Cross-worktree` banner on stderr —
  supports cross-repo housekeeping flows.
- **Class C — Whoami**: `thrum whoami` prepends the banner to BOTH stdout and
  stderr so downstream tooling parsing stdout sees the warning inline. `--json`
  mode correctly suppresses the stdout banner and routes equivalent context
  through the slog bridge's hints array.

A `TestEveryLeafHasCrossWorktreeResponse` CI gate fails the build if any new
cobra leaf lacks a class annotation, preventing silent taxonomy drift.

## v0.10.3 — 2026-05-16

The v0.10.3 line shipped after RC soak. The headline change closes the
cross-agent inbox read-race silent-loss class (watermark-gated `markRead` plus
an honest "hidden by filter" count on inbox listings). The rc cycle also
produced the v2 `/thrum:restart` skill with in-context-composed prose
continuations writing directly to `.thrum/restart/<agent>.md`, a pre-launch
readiness gate that polls pane stability instead of a hardcoded sleep, and a
bundle of quieter fixes around env scoping, self-echo, and preamble framing.

This release is largely about the runtime experience: codex gains the same
first-class plugin treatment claude has had, fresh and restarted tmux panes no
longer sit idle when an agent misses its prime banner, and the first-launch
trust dialogs both codex and claude show are now recognized as a distinct class
so Thrum's startup keystrokes don't accidentally answer them. A bundle of
quieter fixes around env scoping, self-echo, and preamble framing rounds it out.

### Added

- **Codex plugin first-class support.** `codex-plugin/plugins/thrum/` now ships
  the same hook surface the Claude plugin has: SessionStart auto-prime,
  PreToolUse safety block, and a Stop hook that flags unread inbox messages.
  Fourteen role-discipline skills sync over from `claude-plugin`. Installation
  is a one-command bootstrap that also handles a third-party cache-staging gap
  in codex 0.130.0, so you can install and start using it in the same step:

  ```bash
  bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/thrum-dev/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
  ```

- **Tmux silence watchdog after launch and restart.** When `thrum tmux launch`
  or `thrum tmux restart` returns, the daemon watches the agent's pane for
  silence and sends a contextual nudge if the agent hasn't engaged within the
  configured threshold. Closes the long-standing gap where fresh codex agents
  (and large-context restarts in claude) would sit idle at a welcome banner or
  post-restart screen because they'd missed the prime instruction. Configurable
  via the new `restart.silence_watchdog_seconds` key (default 30s; negative
  disables; see [Configuration](configuration.md)). Internally this also
  replaces the previous hardcoded 10-second pre-inject sleep with a
  pane-stability readiness probe.

- **Trust-gate detection for codex and claude first-launch dialogs.** Thrum
  recognizes the trust prompt both runtimes show on first launch in a new
  directory as a distinct class. When a trust gate is up, keystroke injection
  paths (identity banner, prime nudge, watchdog nudge) are skipped so the user
  can answer the prompt without interference from Thrum. Normal
  permission-prompt handling and supervisor notify are unchanged — see
  [Trust-Gate Detection](permission-prompts.md#trust-gate-detection).

- **Per-session env scoping at session create.** `tmux.CreateSessionWithEnv`
  passes `THRUM_NAME/AGENT_ID/ROLE/MODULE/HOME/INTENT` per-session, so distinct
  tmux sessions on a shared server present distinct identities to their initial
  shells. Subsequent split-window and new-window panes inherit correctly.

- **Anti-rush discipline in coordinator and orchestrator preambles.** Five
  operational rules codify patterns the project has hit before — skipping review
  gates on small diffs, bucketing findings as "follow-ups" without
  justification, shipping a fix labeled X when the actual cause is something
  else, declaring DONE without verifying the user-visible bug is gone, and
  taking the cheapest path on autopilot.

### Changed

- **`thrum init` lowercases the default agent name.** The wizard previously
  derived names like `coord_316Redesign` from a capitalized repo directory and
  then rejected its own suggestion at the validator (`a-z`, `0-9`, `_` only).
  The default is now always validator-compatible.

- **Pre-launch readiness is gated on pane stability, not a fixed sleep.** The
  post-launch / post-restart inject path polls the tmux pane every second and
  proceeds once two consecutive captures are byte-identical (TUI rendered,
  runtime at input-ready), with a 60s ceiling. Replaces the brittle
  `Sleep(10s) + retry` pattern that drove an earlier trust-prompt regression.

- **Role preambles now say skills MUST be invoked, not auto-loaded.** The
  "Available skills (situational)" section in seven role preambles previously
  implied skills fire automatically when the runtime matches trigger phrases.
  Skills do not auto-load — agents must invoke them explicitly via the `Skill`
  tool. Wording is now directive rather than descriptive.

- **Enhanced role preambles regain the in-tmux listener-suppression carveout.**
  Twelve enhanced preambles
  (`deployer / documenter / monitor / planner / tester / reviewer` ×
  `strict / autonomous`) had restated the "spawn a listener on session start"
  instruction without preserving the tmux carveout from the base preamble,
  causing tmux-managed agents to spawn a redundant background listener that
  burned context for zero delivery benefit. Carveout restored; a regression test
  now fails any preamble that issues the spawn directive without the
  SKIP-when-in-tmux qualifier.

- **`thrum worktree create` propagates SessionStart hook scripts** into the new
  worktree's `scripts/` directory. `scripts/thrum-startup.sh` and
  `scripts/thrum-check-inbox.sh` are gitignored, so `git worktree add` doesn't
  carry them across; without the per-worktree copy, the Claude Code SessionStart
  hook fired against a missing script and the agent never quickstarted in
  worktree-created subdirs. Copy is idempotent on size+mtime.

- **`codex` runtime preset is marked `HasSessionStartHook: true`** (parity with
  `claude` and `cursor`). The codex SessionStart hook shipped earlier but the
  preset wasn't updated, so `HandleLaunch` / `HandleRestart` were routing codex
  through the non-hook branch (typing `/thrum:prime` into the TUI) instead of
  emitting the identity banner once the runtime rendered.

### Fixed

- **Self-echo phantom nudge.** Outbound `thrum send` from agent A to agent B was
  producing a phantom `New message from @<A>` reminder in A's own pane on every
  send. Root cause: `CLAUDE_PROJECT_DIR` leaks through a shared tmux server's
  default-environment, so Claude Code in B's pane was resolving the
  `${CLAUDE_PROJECT_DIR}/scripts/thrum-check-inbox.sh` hook against A's
  worktree. Fix scrubs `CLAUDE_PROJECT_DIR` at the existing `cleanTmuxEnv`
  chokepoint, alongside the `THRUM_*` and `TMUX/TMUX_PANE` scrubs. Scope is
  narrow to the single variable with documented leak evidence; `CLAUDE_API_KEY`
  and other `CLAUDE_*` vars are explicitly preserved by a regression test.

- **Defense-in-depth self-echo guards.** Independent of the env-leak fix, both
  the daemon spool dispatcher and the inbox-check hook now drop any spool entry
  whose `from` matches the receiving agent ID — so a future regression upstream
  cannot reach the user-visible self-echo nudge.

- **`project-philosophy` skill respects `AskUserQuestion`'s 4-option limit.**
  Step 5 of the skill prompted with option lists that could exceed the tool's
  hard cap of 4, failing with `Invalid tool parameters`. The skill now uses
  sequential questions (`category?` → `specific item within category?`) when the
  natural option count exceeds four. Synced across all four runtime plugin
  copies.

- **`thrum init --force` no longer silently disables messaging.** A hard-coded
  assignment in the runtime-selection step was overwriting `single_agent_mode`
  to `true` on every init run, which disables the inbox listener and stop-hook
  checks. Agents appeared healthy in `thrum team` but inbound messages never
  arrived and replies silently dropped. Affected users upgrading via the common
  `thrum init --force` refresh path; fresh installs were unaffected. If you hit
  this on an earlier release, open the project's `.thrum/config.json` and flip
  `single_agent_mode` back to `false`.

### Upgrade Notes

- **One new config key.** `restart.silence_watchdog_seconds` (default 30s,
  negative disables) — only relevant if you want to tune the post-launch /
  post-restart nudge cadence.
- **Codex users:** the one-command installer above is the easiest path. The
  legacy `~/.codex/skills/` extra-roots loader is gone as of codex 0.130.0; if
  you previously installed there, see the migration note in
  [Codex Plugin](codex-plugin.md).
- **No CLI flag removals or behavior reversals** since v0.10.2.

## v0.10.2 — 2026-05-04

Hotfix release closing two related foot-guns from the v0.10.x identity work: the
SessionStart hook (`scripts/thrum-startup.sh`) breaking on every
already-registered worktree, and a tmux env-hijack class that caused new panes
to resolve identity to the daemon-starter's agent. Plus a long-standing
`thrum purge` regression where message JSONL files weren't actually shrinking.

### Fixed

- **`thrum quickstart` no longer rejects idempotent same-name re-register.** The
  `quickstart_self_rename` guard was firing on every call where the caller
  already owned an identity, even when the requested `--name` matched the
  existing one. This broke `scripts/thrum-startup.sh` — the SessionStart hook on
  every claude session — at step 3 (`thrum quickstart`); `set -e` aborted before
  steps 4 (inbox check), 5 (announce), and 6 (cron install) ran.
  `thrum tmux start` against an existing worktree hit the same guard and aborted
  before claude launched. **Same-name re-register is now allowed without
  `--force`** (it's an idempotent no-op); a real rename (different `--name`)
  still requires `--force`.

- **`tmux.create`-spawned panes no longer inherit `THRUM_*` env from the
  daemon.** When the daemon was started from a primed shell, every tmux pane it
  spawned inherited the daemon's `THRUM_AGENT_ID`, `THRUM_HOME`, etc. — causing
  the new pane's `thrum whoami` to resolve to the daemon-starter's identity
  instead of the pane's intended agent. Two-layer fix: scrub `THRUM_*` from the
  daemon's tmux exec env, AND pass per-session `-e KEY=` overrides so even
  long-running tmux servers (which cache the environ from server-start time)
  produce clean panes.

- **`thrum purge --confirm` now actually shrinks JSONL message files.** The
  filter was passing the wrong field name (`"created_at"` vs the on-disk
  `"timestamp"`) when iterating message JSONLs, so every record was kept and
  on-disk files grew unboundedly. Verified live: `--before 30d` against a 335MB
  sync dir filtered 13 message files in 7.7s.

- **Misleading `unauthenticated_rpc` deny message** now points at `thrum prime`
  as the cache-warming recovery (was
  `cd into a registered agent worktree and retry`, which was the wrong fix when
  the caller already WAS in a registered worktree but the daemon's binding cache
  hadn't warmed after a restart).

### Internal

- Release-test harness coord-whoami probe now retries 3× 30s instead of a single
  60s wait — recovers from missed-keystroke races on saturated multi-agent dev
  boxes.
- Unit-test hardening: `internal/cli` and `internal/config` test packages now
  have `TestMain` env-isolation guards so tests don't silently inherit `THRUM_*`
  pollution from the operator's primed shell.

### Upgrade Notes

- **No CLI flag changes; no config changes.** Drop-in upgrade from v0.10.1.
- After upgrade: `make install` (or download the new binary), then
  `thrum daemon restart` so the running daemon picks up the new binary. **Also
  refresh the Claude Code plugin** (`/plugin update thrum`, or remove + add the
  marketplace) — v0.10.2 ships an updated `inject-prime-context.sh` that
  improves the SessionStart context delivery banner.

## v0.10.1 — 2026-05-03

Two related identity-resolver fixes shipped together. v0.10.0 is marked
prerelease; upgrade to v0.10.1.

### Fixed

- **`thrum quickstart` from a `.thrum/redirect`-using worktree no longer writes
  the agent identity into the parent repo.** When `THRUM_HOME` was set (which
  the wizard's daemon-inline path effectively does for spawned panes),
  `quickstart` was hijacking the identity-write target to
  `$THRUM_HOME/.thrum/identities/` and recording the parent path as the agent's
  `worktree`. Subsequent identity-resolution from any peer worktree could then
  cross-claim, with symptoms like `thrum whoami` returning the wrong agent and
  `unauthenticated_rpc` guard denying writes.

- **Boot-time identity reconcile** so write RPCs from any registered worktree
  succeed after a daemon restart, without re-running `thrum quickstart`.
  Previously, the peercred resolver matched caller CWDs against
  `session_refs JOIN sessions WHERE ended_at IS NULL` — that view is durable in
  SQLite but loses rows on shutdown / cleanup / long quiescence, so disk truth
  (identity files) and resolver truth (DB rows) drifted apart. `thrum send`,
  `thrum tmux start`, and other write RPCs would fail with
  `anonymous caller cannot invoke X` from a worktree where an identity file
  clearly existed; only `thrum quickstart --force` re-populated the rows
  (`thrum prime` did not). The fix walks `.thrum/identities/*.json` at daemon
  boot and inserts the missing `(sessions, session_refs)` pairs via `safedb` in
  a per-identity transaction. Local-only by design — direct SQL, no JSONL
  events, no cross-machine sync — because `session_refs` is intentionally
  local-only state. The same pass restores in-memory tmux pane-nudge bindings
  for any identity whose `tmux_session` is still alive. Closes thrum-soj8 +
  thrum-6kk6.

### How to verify after upgrade

```bash
# Existing redirect-using worktrees: re-quickstart from inside the
# worktree to refresh the identity file with the corrected location
# and worktree value.
cd <child-worktree>
thrum quickstart --name <name> --role <role> --module <module> --force
thrum whoami   # should report the child worktree, not THRUM_HOME
```

### v0.10.0 prerelease note

The v0.10.0 release page on GitHub is marked prerelease and the Homebrew tap was
reverted to v0.9.2 during the fix window. v0.10.1 promotes back to "latest" with
the regression closed.

## v0.10.0 — 2026-05-03

The v0.10.0 work centers on `thrum init`. The first-run experience used to be a
silent scaffold and a list of follow-up commands; now it walks you through
identity, worktrees root, role templates, and daemon start in one interactive
flow. Existing CI scripts keep working unchanged via the `--non-interactive`
flag (or any non-TTY stdin).

### New

- **`thrum init` interactive wizard.** On a TTY, `thrum init` prompts for agent
  name / role / module, worktrees-root path, role-template choice (enhanced /
  default / skip), and starts the daemon — all in one flow. Press enter through
  every prompt to accept the recommended defaults.
- **Pre-fill any prompt with a flag.** The wizard reads `--name`, `--role`,
  `--module`, `--worktrees-root`, `--roles=enhanced|default|skip`, and
  `--no-daemon` so you can script it end-to-end in fixtures.
- **`--force` re-init pre-seeds prompts from existing values.** Running
  `thrum init --force` on a previously-initialized repo loads identity and
  worktrees-root from the current `.thrum/` so pressing enter through the
  prompts is a no-op refresh.
- **Transactional rollback on failure or SIGINT.** If any wizard step errors
  after `.gitignore` / `.git/info/exclude` were touched, the wizard restores
  them byte-for-byte. Ctrl-C during prompts cleans up cleanly.
- **`implementer-worktree-write-only` role template.** The wizard's "enhanced"
  choice ships a stricter implementer preamble that pins writes to the agent's
  own worktree and forbids drive-by edits to the main repo.
- **tmux gate.** If `tmux` is not on `PATH` when the wizard reaches the
  daemon-start step, init exits early with an OS-appropriate install hint
  (`brew install tmux` / `apt install tmux`).

### Changed

- **Default worktrees base path migrated.** `worktrees.base_path` now defaults
  to `~/.thrum/worktrees/<project>` (was `~/.workspaces/<project>`). Repos with
  an explicit `Worktrees.BasePath` in `.thrum/config.json` are unaffected. If
  you relied on the implicit fallback and want existing worktrees to keep
  resolving, run
  `thrum config set worktrees.base_path "$HOME/.workspaces/<project>"` before
  the next worktree create. The wizard's worktrees-root prompt also accepts the
  legacy path.

### Fixed

- **`scripts/thrum-check-inbox.sh` excluded alongside `thrum-startup.sh`.** Init
  now adds the inbox-check helper to `.gitignore` (and `.git/info/exclude` in
  stealth mode), preventing it from leaking into tracked changes.

### Migration

- If you scripted `thrum init` in CI: add `--non-interactive` (or rely on
  non-TTY stdin) — both keep the legacy silent path. The wizard never fires
  under those conditions.
- If your worktrees lived under `~/.workspaces/<project>` and you want them to
  stay there: pin the path with
  `thrum config set worktrees.base_path "$HOME/.workspaces/<project>"` before
  the next `thrum worktree create`.

## v0.9.2 — 2026-04-29

The v0.9.2 work was mostly polish on agents and preambles — the parts of Thrum
agents wake up with. If you've been wishing role discipline survived restarts
better, or that Claude Code sessions actually loaded the briefing they're
supposed to, this is the release.

### New

- **`role_config` persists in `.thrum/config.json`.** When you run
  `/thrum:configure-roles`, your answers (autonomy + scope per role) save under
  a new top-level `role_config` key. The skill prefills from saved answers on
  re-run so you only re-confirm what you want to change. See
  [Role Templates](role-templates.md) and
  [Configuration → Role Config](configuration.md#role-config).
- **`thrum roles refresh`** — re-renders `.thrum/role_templates/<role>.md` from
  saved answers + the shipped templates embedded in the binary. Run this after
  upgrading Thrum without re-doing the interactive prompts.
- **`thrum prime` surfaces drift hints.** Three codes: `roles.config.migration`
  (rendered templates exist, no `role_config` block), `roles.config.schema-bump`
  (shipped schema newer than saved), `roles.config.body-diff` (shipped template
  body changed). Precedence is top to bottom; only one fires per repo.
- **User overlay composed into the rendered preamble.**
  `.thrum/context/<agent>.md` is auto-created empty by `thrum quickstart`.
  Anything you write into it gets appended after `DefaultPreamble` with a `---`
  separator. Per-agent tweaks ride on top of the role discipline without forking
  the template.
- **Pane-side identity banner.** Sessions launched via `thrum tmux create` and
  restarted via `thrum tmux restart` now display an Agent / Role / Worktree /
  Branch banner directly in the tmux pane, plus a `MUST READ` line pointing at
  the auto-loaded briefing. The banner only fires on runtimes that ship the
  SessionStart hook (Claude Code and Cursor today). See
  [Claude Code Plugin → Pane-side identity banner](claude-code-plugin.md#pane-side-identity-banner).
- **SessionStart hook injects `thrum prime` output.** The Claude Code and Cursor
  plugins' SessionStart hook now runs `thrum prime` and emits the full briefing
  inline as the hook's `additionalContext`. Restart-snapshot framing is hoisted
  to the top with a `🛑 ACTION REQUIRED` directive so it doesn't get
  read-and-rationalized-away.
- **Role-skills layer.** Ten new description-triggered skills deepen role
  discipline situationally without bloating the always-loaded preamble. Three
  for coordinator (`dispatching-work`, `running-review-cycles`,
  `managing-state-and-lifecycle`), four for implementer (`receiving-dispatch`,
  `tdd-and-quality`, `status-and-handoff`, `receiving-review-feedback`), three
  for researcher (`investigating`, `answering-queries`, `maintaining-memory`).

### Fixed

- **tmux pty leak (thrum-x6e8.5).** `tmux-exec` migrated from `respawn-pane` to
  a persistent-session pool. The previous approach leaked pseudo-terminals on
  every respawn, eventually exhausting the per-process fd limit on long-running
  daemons.
- **`thrum tmux status` and `thrum tmux connect` leaked sessions across daemons
  (thrum-zuz5).** Pass 2 of `HandleStatus` was filtering on `@thrum-managed=1`,
  which every Thrum daemon stamps — so sessions from unrelated worktrees and
  projects leaked into the picker. `HandleCreate` now also stamps
  `@thrum-thrum-dir=<this daemon's thrum_dir>` and pass 2 filters on it.
  **Migration:** sessions created before v0.9.2 won't appear in pass-2 output
  until you recreate them via `thrum tmux create`.
- **`runPreambleInit` fallback ignored `.thrum/redirect` (thrum-5hhx).**
  Worktree setups using the redirect indirection silently lost their custom
  preamble path. Fallback now follows the redirect.
- **Worktree preambles rendered relative strategy paths (thrum-rm4x,
  thrum-z9zl).** Generated preambles referenced `strategies/<file>` relative to
  the rendering CWD, breaking when read from a different directory. Paths are
  now absolute against the project root.
- **`thrum context preamble --init` overwrote customized templates
  (thrum-pk2o).** `--init` skipped `.thrum/role_templates/<role>.md` and went
  straight to the generic default. It now consults `RenderRoleTemplate` first
  and only falls back when no rendered template exists.
- **Peercred unknown-vs-anonymous (thrum-ndtw, backported from v0.9.1).** v0.9.0
  wrapped introspection failures (`tspeer.Get`, `gopsutil.Cwd`) with
  `ErrAnonymous`, which rejected mutating RPCs from registered Bash subprocesses
  on macOS. Steps 1+2 now return raw errors and fall through to legacy
  client-asserted identity. Provably-anonymous paths (steps 3+5) still wrap.

## v0.9.1 — 2026-04-24

- **`thrum setup claude-md --apply`** — the documented-but-unimplemented command
  from issue #8 now works. Bare `thrum setup claude-md` prints the template;
  `--apply` creates `CLAUDE.md` (or appends to existing); `--apply --force`
  replaces idempotently. Block markers: `<!-- BEGIN THRUM -->` /
  `<!-- END THRUM -->`.
- **Peercred resolver error taxonomy (thrum-ndtw).** Same fix described under
  v0.9.2 above; landed first in v0.9.1.

## v0.9.0 — 2026-04-23

### What's New

- **Permission-prompt detection** — the daemon detects when a tmux-managed agent
  hits a blocking permission prompt and routes an actionable nudge to configured
  supervisors. Approve or deny from the CLI, web UI, or Telegram. See
  [Permission Prompt Detection](permission-prompts.md).
- **Identity guards** — cross-worktree CWD enforcement hard-errors on CWD drift
  instead of silently misattributing actions to the wrong agent. See
  [Troubleshooting: Identity](troubleshooting-identity.md).
- **CLI hints (Phase B)** — contextual guidance printed before and after
  destructive or multi-step commands. See [CLI Hints](cli-hints.md).
- **Drift reconciliation** — peers self-heal address drift automatically without
  re-pairing. See [Peers](peers.md).
- **`thrum tmux quickstart`** — alias for `thrum tmux create`. Same command,
  clearer name. `thrum tmux create` now requires `--name`, `--role`, `--module`
  (or `--no-agent`) and runs quickstart inside the new pane automatically.
- **`thrum worktree setup`** — alias for `thrum worktree create`. Both commands
  now accept optional quickstart flags (`--name`, `--role`, `--module`,
  `--intent`, `--runtime`). Provide all three required flags and it creates a
  real tmux session, runs quickstart inside it, and prints the next-step
  `thrum tmux launch <name>` command. The agent identity is registered, but the
  runtime is not started until `tmux launch` runs — a clean two-step pattern
  with no manual `thrum quickstart`.
- **Single identity per worktree** — quickstart cleans up old identity files
  after writing the new one. You can't end up with a stale identity causing
  auto-select errors.
- **`thrum tmux launch` hard-errors on missing identity** — launch needs an
  agent identity to determine the runtime. Sessions created with `--no-agent`
  (or worktrees with no identity file) cannot be launched until you register an
  agent first.
- **Next-step guard messages** — `agent register`, `worktree create` (no agent),
  `purge --confirm`, `daemon stop`, `tmux restart`, and `tmux launch` now print
  explicit hints about what to do next (or what just broke).
- **Monitor Jobs v1** — `thrum monitor start/list/show/stop/logs/restart`.
  Attach a monitor to any long-running process and it emits matches as synthetic
  Thrum messages. Leading-edge debounce (default 60s, min 30s), auto-persist,
  local-socket-only.

### Breaking Changes

If you're upgrading from v0.8.x, read this section before starting the daemon.

- **Forged `caller_agent_id` rejected** — pre-v0.9.0 callers could pass any
  `caller_agent_id` and it was accepted at face value. Now cross-checked against
  kernel-verified PID resolution. Mismatches get "identity mismatch" and are
  dropped.
- **WebSocket non-localhost origin → HTTP 403** — pre-v0.9.0, `CheckOrigin`
  returned `true` for all origins. Browser pages on non-loopback origins can no
  longer upgrade to WebSocket.
- **`message.delete` by non-author rejected** — only the original author can
  soft-delete their own messages.
- **`message.deleteByAgent` requires caller == target** — agents can only
  bulk-delete their own messages.
- **`message.deleteByScope` is daemon-internal only** — no longer callable from
  any external client (CLI, browser, or unix socket).
- **Legacy daemon_id auto-rotated** — first v0.9.0 daemon start rotates
  pre-existing hostname-derived daemon IDs to ULID format. Existing peer pairs
  must be re-paired: `thrum peer remove <name>` then
  `thrum peer add --type tailscale <name>` on each peer.
- **Downgrade blocked by migration guard** — running a v0.8.x binary against a
  database migrated to schema v24 fails with a clear error. Recovery: stop
  daemon → restore `thrum.db.pre-migration-v<N>-bak` → run the older binary.
- **`~/.thrum/runtimes.json` replaces the old platform config path** — silently
  dropped on upgrade; custom runtimes disappear. Move the file manually: Linux:
  `~/.config/thrum/runtimes.json` → `~/.thrum/runtimes.json`; macOS:
  `~/Library/Application Support/thrum/runtimes.json` →
  `~/.thrum/runtimes.json`.
- **`peer add --type` and `peer join --type` are now mandatory** — the
  previously implicit `tailscale` default is gone. Add `--type tailscale` to any
  existing scripts that omit it.
- **`alert-silence` hook no longer triggers permission-prompt detection** —
  daemon-side poller replaces it (~20s detection latency). Existing
  `alert-silence` config in `.tmux.conf` is inert for this purpose.
- **Identity guards hard-error on CWD drift** — running thrum from the wrong CWD
  fails with `identity guard "cross_worktree" fired: pid_mismatch` instead of
  silently misattributing. Use `THRUM_HOME` to pin repo path. See
  [Troubleshooting: Identity](troubleshooting-identity.md).
- **`thrum daemon start` and `thrum init` refuse non-git directories** — pass
  `--force` for non-anchored use.
- **`thrum quickstart` refuses self-rename / name collision** — without
  `--force`. Previously a silent overwrite.
- **Telegram fresh-DM `y` resolves a pending nudge instead of routing to
  `--target`** — if the sender has a pending permission nudge, a bare
  `y`/`n`/`yes`/`no`/`allow`/`deny` DM resolves that nudge rather than
  delivering to the configured target agent.

## v0.7.x

- **Orchestrator role** — a dedicated coordinator agent that reads your plan,
  claims tasks, spawns implementers, and stops at every review gate without
  touching the merge button; see [Orchestrator Role](orchestrator-role.md)
- **Multi-runtime support** — Claude Code, Codex, OpenCode, etc. all work; Thrum
  picks the right tmux launch command for each; see
  [Multi-Runtime](multi-runtime.md)
- **Peer mesh** — agents on different machines join one team over Tailscale or
  local network with no extra servers; see [Peers](peers.md)
- **Single-agent mode** — Thrum's context management and session tracking work
  without any messaging layer; it's now the default for new installs; see
  [Single-Agent Mode](single-agent-mode.md)
- **Daemon-managed tmux sessions** — the daemon owns the session lifecycle,
  delivers messages the moment they arrive, and runs zero background listeners
  in the agent process; see [Tmux Sessions](tmux-sessions.md)
- **Command queue dispatch** — coordinators submit commands to agent panes via
  `thrum tmux queue`, with completion tracking, `@system` notifications, and
  restart recovery; see
  [Tmux Sessions — Queue Dispatch](tmux-sessions.md#command-queue-dispatch)
- **Worktree management** — `thrum worktree create/teardown/list` (alias:
  `thrum worktree setup`) handles git worktree setup with automatic Thrum and
  Beads redirect wiring
- **Daemon logging** — structured slog output with lumberjack rotation; view
  with `thrum daemon logs`; configurable via `daemon.log_level`

## Older Releases

For releases before v0.7.0, and for the full machine-readable history, see
[CHANGELOG.md](https://github.com/leonletto/thrum/blob/main/CHANGELOG.md).
