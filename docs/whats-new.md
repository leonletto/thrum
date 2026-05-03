## What's New

This page tracks the user-visible changes in recent Thrum releases — highlights,
breaking changes, and anything that needs attention when you upgrade. The full
machine-readable history lives in
[CHANGELOG.md](https://github.com/leonletto/thrum/blob/main/CHANGELOG.md).

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
