# Changelog

All notable changes to Thrum will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.10.5] - 2026-05-21

### Added

- **`thrum inbox --from @agent` filter** — scope unread inbox to messages from a
  single sender. Useful for catching up on a specific agent's traffic without
  reading the rest of the queue.
- **`thrum worktree teardown --delete-branch` flag** — tear down a worktree and
  delete its branch in one step. Previously the branch was kept and required a
  separate `git branch -D` after teardown.
- **Daemon-side backstop nudger** — the daemon now polls for stale-unread
  messages and re-emits delivery nudges, replacing the user-side
  `thrum-inbox-poll.sh` cron pattern. More reliable (survives runtime restarts)
  and lower-overhead than a per-agent cron schedule.
- **Headless `worktree.Create` / `worktree.Destroy` Go API** — the worktree
  lifecycle moves to a single shared package (`internal/worktree`), used by both
  the cobra commands and future programmatic callers (notably the v0.11
  substrate epics). Behavior-equivalent to the previous cobra-only path; opens
  the door to ephemeral-worktree flows.
- **`thrum prime` first-turn ack instruction** — the prime briefing now asks the
  runtime to emit a short scrollback line on receipt, giving the agent visible
  signal that context loaded. Previously some runtimes (notably Claude Code's
  SessionStart hook) silently absorbed the prime briefing with no observable
  first-turn anchor.
- **`coordinator-post-restart-sweep` skill + `waiting-on-coord-agent-sweep.sh`
  script** (thrum-e1n0). New coordinator discipline tool that detects agents
  blocked waiting on coord decisions by pattern-matching the latest assistant
  message body from each Claude agent's JSONL transcript. Pattern library
  empirically mined from project conversation archive (20 regex rules +
  trailing-question-end-turn structural signal). Wraps with a decision-tree
  skill for post-prime use. Sibling sweep
  `scripts/error-and-context-agent-sweep.sh` (no rename on this branch; the
  release line didn't carry the prior `tmux-agent-sweep.sh` baseline) provides
  ctx-% / api-error sweeps with the same JSONL substrate.
- **`coordinator-context-monitoring` skill** (thrum-dtad backport). Tier-ladder
  discipline for managing live agent context limits during long coordination
  sessions — sweep + pre-emptive restart before an agent hits a 97%-context
  silent blow-up. Safe to wire into a recurring keepalive cron that INVOKES the
  skill; the skill itself applies the tier ladder and only fires autonomous
  restarts at the >85% tier. Closes the rc.7 "Unknown skill" error path that
  fired when the keepalive cron invoked this skill on a release-line install
  that didn't yet carry it. Mirrored to claude + cursor + opencode plugins.
- **`coordinator-running-brainstorm-cycles` skill: three explicit review gates**
  (thrum-dtad backport). The skill now owns the entire researcher-side design
  pipeline with dual-review gates at three stages (brainstorm, plan, impl
  prompt) — each a verify-against-plan + code-reviewer parallel-sonnet pass.
  Codifies the post-`project-setup` review discipline so plan→prompt translation
  errors get caught before the implementer executes against them. Mirrored to
  claude + cursor + opencode plugins.
- **Schema v25-v32 forward-port from thrum-agents** — `CurrentVersion` bumped
  from 24 to 32 with 7 new migration blocks (scheduler_job_state +
  scheduler_job_events, agent column extensions, agent_lifecycle_events,
  reminders, email_msg_seen + email_outbound_queue + email_peer_rate_state). The
  tables are intentionally dead-end on v0.10.5 — no consumer code reads from
  them. Goal is binary-supports-v32-schema-on-disk so v0.10.5 binaries can open
  DBs previously touched by v0.11-substrate work on multi-binary worktree
  machines. v29 is a deliberate gap (reserved for substrate follow-ups);
  `runMigrations` handles gapped sequences naturally.

### Changed

- **`thrum init` now JSON-merges `.claude/settings.json` instead of skipping
  when the file exists** (thrum-nh88, P0). Previously `runtime_init` skipped the
  file on existence (only `--force` would overwrite), which prevented thrum from
  refreshing hook strings once a project had any settings file — including
  project files written by `bd setup claude`. The new merge path preserves
  third-party entries (bd's `bd prime --hook-json` SessionStart hook,
  user-customized commands) and only adds missing thrum hook entries via
  exact-string match. Idempotent: re-running on an already-current file produces
  no diff (2-space `json.MarshalIndent` format matches bd's writer). Same merge
  runs against each worktree's settings.json from `worktree.EnsureRedirects` so
  worktrees get hooks too. Operator opt-out remains:
  `Worktrees.ThrumEnabled=false` skips the merge entirely.
- **`Worktrees.BeadsEnabled` default is now detection-based, not hardcoded
  true** (thrum-nh88 scope F). `thrum init` runs `bd --version`; on exit code 0
  the integration enables and the canonical `bd prime --hook-json` SessionStart
  hook is installed into `.claude/settings.json`; on missing bd the flag
  defaults false and a one-line install hint surfaces (`brew install beads` +
  re-run `thrum init`). Marketplace-plugin detection
  (`enabledPlugins.beads=true` in project / global / local settings)
  automatically skips install to avoid double-fire. Legacy bd entries on
  `.claude/settings.local.json` are stripped as part of the same pass.
  User-customized variants on the `bd prime` command string (e.g.
  `bd prime --hook-json --custom-flag`) are left untouched — only the bare-form
  legacy variants enumerated in the spec are swept. Sibling runtime configs
  (`.codex/hooks/`, `opencode.json`, `.gemini/settings.json`) do not use
  hook-array JSON; no equivalent merge applies — Claude Code's settings.json is
  the only target for this release.
- **Beads integration now via `bd setup claude`** (or auto-installed by Thrum's
  runtime-init when `Worktrees.BeadsEnabled=true` and `bd` is on `PATH`). The
  standalone Beads plugin is no longer recommended and should be uninstalled.
  Migration: `/plugin uninstall beads@beads-marketplace` →
  `/plugin marketplace remove beads-marketplace` → `brew install beads` →
  `bd setup claude` → restart Claude Code. Thrum auto-handles step 4
  (`bd setup claude`) in Thrum-managed projects once `bd` is on `PATH`. If `bd`
  state changes (installed or uninstalled) after `thrum init`, re-run
  `thrum init` to refresh the bd-hook presence in `.claude/settings.json`.
- **URLs migrated from `leonletto.github.io/thrum` to `thrum.team`** (Phase 6.3
  cleanup). README, website content, docs, and SEO references updated. Old
  GitHub Pages URLs still resolve via redirect; canonical now points at
  `thrum.team`.
- **`thrum-inbox-poll.sh` cron deprecated** in favor of the daemon-side backstop
  nudger. Existing installations continue to function but the cron is no longer
  recommended; the daemon backstop is enabled by default. Removal of the
  user-side cron is queued for a future release.
- **`project-setup` skill follows `.thrum/redirect` when checking
  `philosophy.md`.** Previously failed in redirected worktrees because it looked
  at the worktree-local path directly. Now resolves the redirect before the
  philosophy-presence check.
- **`thrum worktree create` / `thrum worktree teardown` rewired through
  `internal/worktree`** — cobra commands now delegate to the shared package
  rather than inlining worktree-lifecycle logic. No user-visible behavior
  change, but makes worktree-related bug fixes land in one place.
- **README dropped removed-feature references** (groups + subscriptions) — these
  features were removed in earlier releases; the README is now consistent with
  the current CLI surface.
- **`/thrum:restart` skill: §1 Big picture mandate + 11-section structure.**
  Restart snapshots must now begin with a
  `## 1. Big picture — what shipped this session` heading (1-3 specific
  sentences naming artifacts, decisions, cycles closed), so the snapshot doubles
  as the agent's own log entry visible in `thrum agent sessions list`. The
  free-form prose enumeration was replaced with a numbered structure (sections
  2-11) covering artifact state, players, decisions-with-context, repo-owner
  questions, outstanding work, patterns-that-burned-us, file paths, resume plan,
  honest unknowns, and end-of-continuation reflection. Forward-ported from
  thrum-agents into release/v0.10.5 so rc.4 ships the mandate.
- **`bd comments` invocation syntax corrected across docs.** Role-preamble
  templates (`internal/context/roleconfig/templates/roles/implementer-*.md`) and
  `bd` reference docs (`website/docs/beads-and-thrum.md`,
  `docs/beads-and-thrum.md`) listed the subcommand as
  `bd comments <id> add "note"`; the actual CLI is
  `bd comments add <id> "note"`. Silently failed on every invocation against the
  wrong shape. Corrected; column alignment preserved on code-block examples.
- **BREAKING: `thrum send` requires explicit recipient flag** (thrum-t698).
  Invoking `thrum send 'msg'` without either `--to @<agent>` or the new
  `--broadcast` flag now hard-errors (exit 1) with a conversational stderr
  message offering the two valid paths. Previously the no-flag default silently
  broadcast to every team agent — a real footgun that scaled with team size
  (coord live-demonstrated it during Session 75 with an accidental 94-agent
  broadcast). The CLAUDE.md convention has long said "always use
  `--to @<specific-name>`, never role names"; this aligns the CLI default with
  the convention. Migration: explicit `--to @<agent_name>` (the common case) or
  `--broadcast` (when team fanout is genuinely intended). `--to @everyone`
  continues to work as the legacy keyword form. `--to` and `--broadcast` are
  mutually exclusive. CLI_REFERENCE.md + thrum SKILL.md updated; the broader doc
  audit (quickstart.md, messaging.md, identity.md, llms.txt, llms-full.txt) is
  queued for the rc.6-cycle doc-cleanup pass.

### Fixed

- **`runtime-init` no longer leaves stale daemon-managed scripts in worktrees**
  (thrum-akqv, P1). Daemon-managed templates (`scripts/thrum-startup.sh`,
  `scripts/thrum-check-inbox.sh`, `.claude/settings.json`) were skipped on
  `runtime-init` when the files already existed, causing drift in long-running
  worktrees as the template content evolved across releases. The init logic now
  distinguishes daemon-managed scripts (overwrite on init) from user-customized
  configs (preserve on init).
- **`thrum prime` ack interpolation strips backticks from identity fields**
  (thrum-x7rb). Identity fields containing backticks were leaking literal
  markdown/shell interpretation into the rendered ack template. Now sanitized
  via explicit backtick strip in the interpolation path.
- **Inbox backstop spool envelopes preserved from janitor reaping** — the
  janitor was prematurely deleting backstop-pending envelopes, causing
  stale-unread messages to disappear from inbox before the backstop nudger could
  re-deliver them.
- **Self-delivery: `read_at` stamped at insert; author preserved in
  recipientSet** — E1 self-mention semantic fix. Messages with explicit
  self-mention now route correctly to the author's own inbox without being
  filtered out by the recipient-set construction.
- **Self-echo nudge guard for tmux dispatch** (thrum-1zfk; regression of
  thrum-kfn3 introduced by the self-delivery fix above). The DispatchTmux tmux
  nudge path was missing the self-skip guard symmetric to the spool path. With
  the author now preserved in `recipientSet` for `read_at` stamping, every send
  touching role-group expansion or self-@mention reached the unguarded tmux
  nudge path, firing phantom "new message" notifications back to the sender.
  Layer 4 (DispatchTmux) now self-skips with `[nudge] tmux.skip self` for grep
  parity with the existing `[nudge] spool.skip self` log.
- **SEO: BlogPosting JSON-LD non-critical warnings cleaned up** — Schema.org
  warnings on generated blog pages.
- **Downgrade-guard error message: actionable recovery hints + CLAUDE.md
  prevention** (thrum-quth, P1). When a binary's max supported schema is below
  the on-disk DB's schema (common after `make install` from a newer-schema
  branch on a multi-binary worktree machine), the daemon refuses to start.
  Pre-rc.4 the error named the version pair but gave no recovery path beyond a
  one-line hint. The expanded error now includes: binary's `CurrentVersion`,
  DB's current schema, two concrete recovery paths (re-install matching binary;
  daemon-stop-first then rm the DB + WAL/SHM with explicit
  `LOSES local message history` warning), and a pointer to a new "Multi-Binary
  Worktree Footgun" section in CLAUDE.md explaining why/avoid/recover. Test pin
  expanded from 1 to 9 contract substrings.
- **Post-launch silence watchdog detects ack-without-act** (thrum-qpw7). After
  the identity-banner printf injection at tmux launch/restart, the watchdog
  (`paneAgentEngaged`) treated the model's printf-mandated ack line
  (`@<name> primed (<role>). Standing by.`) as real agent output — a
  false-positive that suppressed the corrective nudge when the model
  acknowledged the printf body WITHOUT Reading the (possibly truncated) prime
  briefing. The engagement check now ignores lines matching the canonical ack
  pattern `@\S+\s+primed\s*\(`, so the watchdog still nudges the agent into
  running `thrum prime`. The regex is anchored on the literal `(` opener that
  all 5 runtime plugins emit (claude/cursor/opencode/codex/kiro), so unrelated
  prose like `@impl_v0105 primed the database` is not mis-classified and real
  agent output still suppresses the nudge correctly.
- **`thrum monitor stop` no longer hangs RPC critical path on slow-runner
  monitors + reaps grandchild processes** (thrum-puhr.9.2, P1). Two-part fix:
  (Path A) stop returns promptly even when the runner is slow — DB row marked
  stopped synchronously, then 2s best-effort wait, returns unconditionally;
  (Path B) `Setpgid: true` on the monitor command + `syscall.Kill(-pgid, ...)`
  on shutdown so grandchildren that inherited the stdout pipe are killed too
  instead of inheriting a dangling FD. Eliminates the zombie-hang failure mode
  where `monitor stop` blocked for tens of seconds before the runner finally
  exited.
- **`thrum monitor stop` / `logs` / `restart` / `show` accept name as well as
  ID** (thrum-puhr.9.1 / 09wl / tv6z). Operators reach for the monitor name —
  the prominent column in `monitor list` — and previously hit
  `RPC error -32000: monitor not found` because the CLI passed the user-supplied
  identifier straight to the daemon's ID-keyed RPC. The four subcommands now
  resolve name→ID at the CLI layer via `monitor.list` lookup before dispatching
  the real RPC. ULID-shape detection (`mon_<26-char>` validated) routes
  user-typed names like `mon_daily` through the lookup path rather than the
  not-found cliff. Not-found errors hint at `thrum monitor list` / `list --all`
  so operators can discover what's actually available. Daemon RPC surface
  unchanged.
- **Class B/C commands fire the cross-worktree diagnostic banner uniformly via
  `PersistentPreRunE` preflight** (thrum-7b84.11, P2). The 8 leaves that bypass
  `getClient` (daemon status / logs / start / stop / restart / run, backup
  status, telegram status) previously ran silently from the wrong worktree
  because `classifyRefreshError`'s banner emit lived inside `getClient`. A
  `crossWorktreePreflight` hook in `rootCmd.PersistentPreRunE` now fires the
  banner for Class B/C leaves regardless of whether the leaf later flows through
  `getClient`; an absorbed-flag dedups the second emit when both paths fire
  (e.g. `thrum team`). Annotation-gated; no-op for Class A (abort) and bypass
  (help/version) and explicit `--repo` overrides.
- **`tmux capture-pane` joins wrapped lines (`-J` flag)** (thrum-ktp8). The
  post-launch silence watchdog's `paneAgentEngaged` searches captured pane
  content for the `PrimeTruncationSentinel` to bound the decision region.
  Without tmux's `-J` flag, long identity-banner content (Agent + Role +
  Worktree + Branch + sentinel, frequently > 100 chars on typical terminals)
  wrapped mid-string, splitting the sentinel across two pane lines. The per-line
  `strings.Contains(line, sentinel)` check then failed to match on any single
  line → `topIdx = -1` → conservative `return true` → no nudge fired. This
  silently masked the rc.5 thrum-qpw7 ack-exclusion fix (which was correct but
  never reached because sentinel detection failed first). `-J` joins wrapped
  lines (and preserves trailing spaces); all 5 non-watchdog `CapturePane`
  callers (HandleCapture RPC, queue captured- output, alert-silence run-shell,
  permission paneStillMatches) are text- search consumers that benefit from
  joined output, none depend on wrap- preservation. Surfaced during Leon's
  @impl_writer_website_dev rc.5 spot-check.

## [0.10.4] - 2026-05-16

### Fixed

- **`thrum` mutating commands now fail closed under cross-worktree identity
  drift instead of silently sending/marking-read under the wrong identity**
  (thrum-7b84.6). Before v0.10.4, an agent CLI invocation from a worktree that
  didn't match the caller's PID-bound identity silently swallowed the
  `cross_worktree` guard error and proceeded — including silent wrong-author
  `thrum send`/`thrum reply` and destructive wrong-agent `thrum inbox`
  auto-marks-as-read. The bug surfaced in the wild on 2026-05-16 when an
  architect's review was attributed to the implementer it was reviewing —
  JSON-verified message attribution was wrong; the user only noticed because the
  reply made no sense in context. **Standard same-cwd flow is unchanged.**

### Changed

- **Cross-worktree guard response now classified per verb on an orthogonal
  `cross_worktree_response` axis** (thrum-7b84.6). Every cobra leaf gets one of
  three classes:
  - **Class A — Abortable**: mutating + identity-filtered (send, reply, inbox,
    sent, wait, message edit/delete/mark-read/mark-unread, message get, context
    get/save/delete, session start/set-intent/end/heartbeat, agent
    register/touch/set-intent/set-status, quickstart, prime, tmux
    send/create/restart/kill) → fails closed (exit 1, empty stdout, 4-line guard
    error on stderr).
  - **Class B — DiagnosticBanner**: identity-agnostic diagnostics (team, agent
    list, version, daemon logs/restart/run/start/stop/status,
    peer/sync/backup/telegram/tmux status) → exit 0, normal stdout, one-line
    `⚠ Cross-worktree` banner on stderr (flushed before any stdout writes) —
    supports cross-repo housekeeping flows (e.g., updating thrum + restarting
    daemons across N repos) without aborting.
  - **Class C — Whoami**: `thrum whoami` + `thrum agent whoami` alias → exit 0,
    banner prepended to BOTH stdout and stderr — whoami's stdout is
    identity-affirming and downstream tooling parsing stdout must see the
    warning inline. `--json` mode correctly suppresses the stdout banner to
    preserve the single-document JSON contract; equivalent context routes
    through the slog bridge's hints array.
  - The `cross_worktree` guard's remediation message reads
    `cd to the correct worktree or run 'thrum prime' to re-claim`. Agents that
    hit the abort should fix their cwd; there is no user-facing bypass.
  - `TestEveryLeafHasCrossWorktreeResponse` CI gate fails the build if any new
    cobra leaf lacks a class annotation — prevents silent taxonomy drift.

## [0.10.3] - 2026-05-16

### Changed

- **`/thrum:restart` skill: coordinator self-restart branch + drop residue.**
  Three rough edges in the rc.10 v2 prose-continuation skill surfaced by its
  first live invocation on the coordinator pane: (1) Step 3 unconditionally sent
  "Restart snapshot saved..." to `@coordinator_main`, which self-targeted and
  stalled when the invoker was the coordinator — new Step 5 branches on
  `thrum whoami --field role` and emits cross-pane `thrum tmux restart --force`
  instructions for coordinators; (2) Step 4 referenced a stale
  `thrum tmux snapshot restore` command from the pre-rc.10 structured-template
  flow — removed in favor of "auto-loaded by `thrum prime`"; (3) Step 1 embedded
  the CRITICAL DISCIPLINE block inside a `cat > file <<'EOF'` heredoc with a "do
  not run this literally" caveat — restructured into separate composition-guide
  (Step 2) and direct-`Write`-tool (Step 3) steps. Synced to opencode, codex,
  and cursor plugins via `scripts/sync-skills.sh` (thrum-7b84.2).

- **`/thrum:restart` skill rewritten with a v2 prose-continuation body.**
  Replaces the JSON-snapshot Resume Plan template + `thrum tmux snapshot save` +
  append flow with a direct-write of an in-context-composed prose continuation
  to `.thrum/restart/<agent_id>.md`. Skill drops from 134 lines to ~95.
  Continuation files are smaller and higher-signal than the prior structured
  template + lossy tail capture combined — field-tested across 7 v0.11 substrate
  agent restart cycles on 2026-05-15; continuations averaged ~200 lines.
  Coordinator-notify and non-tmux operator fallback preserved verbatim. Synced
  to opencode, codex, and cursor plugins via `scripts/sync-skills.sh`
  (thrum-7b84.1).

- **`thrum init` lowercases the default agent name** before suggesting it.
  Previously the wizard auto-derived a default like `coord_316Redesign` from a
  repo dir with capital letters, then rejected the same default at submit time
  via `identity.ValidateAgentName` (a-z, 0-9, `_` only). The default is now
  always validator-compatible (thrum-puhr.2).

- **Pre-launch readiness gates on pane stability, not a hardcoded sleep.** The
  post-launch / post-restart inject path now polls the tmux pane every 1s and
  proceeds only once two consecutive captures are byte-identical (TUI rendered,
  runtime at input-ready) — with a 60s ceiling. Replaces the brittle
  `Sleep(10s) + Sleep(3s) retry` pattern that drove the cluster-1/5 trust-prompt
  regression. Same primitive as the post-inject watchdog, inverted: stability
  signals "ready to type", silence signals "agent didn't engage" (puhr.10
  cluster 5).

- **Role preambles correct skill-invocation framing.** The "Available skills
  (situational)" section in 7 role preambles no longer claims skills "load
  automatically when the runtime detects matching trigger phrases" / "fire on
  context". Skills do NOT auto-load: agents MUST explicitly invoke them via the
  `Skill` tool when a trigger condition applies. Wording is now directive, not
  descriptive (thrum-puhr.8).

- **Enhanced role preambles regain the in-tmux listener-suppression.** 12
  enhanced preambles
  (`deployer / documenter / monitor / planner / tester / reviewer` ×
  `strict / autonomous`) had restated the "spawn a listener IMMEDIATELY on
  session start" instruction without preserving the tmux carveout in the base
  `DefaultPreamble` — causing tmux-managed agents under those roles to spawn a
  redundant background listener that burned context for zero delivery benefit.
  Carveout restored; an embedded-template regression test now fails any preamble
  that issues the spawn directive without the SKIP-when-in-tmux qualifier
  (thrum-puhr.1).

- **`thrum worktree create` propagates SessionStart hook scripts** into the new
  worktree's `scripts/` directory. `scripts/thrum-startup.sh` and
  `scripts/thrum-check-inbox.sh` are gitignored (added by `thrum init` to
  `.gitignore` / `.git/info/exclude`), so `git worktree add` doesn't carry them
  across. Without the per-worktree copy, every Claude Code SessionStart hook in
  a worktree-created subdir fired against a missing script and the agent never
  quickstarted or re-registered. Copy is idempotent on size+mtime and
  best-effort on a removed source (thrum-nne1).

- **`codex` runtime preset is marked `HasSessionStartHook: true`** (parity with
  `claude` and `cursor`). pm7n.4 shipped the codex `inject-prime-context.sh`
  SessionStart hook but the preset was never updated; HandleLaunch /
  HandleRestart were routing codex through the non-hook branch (typing
  `/thrum:prime` into the TUI) instead of the hook branch (emitting the identity
  banner after the runtime renders).

### Fixed

- **Claude 2.1.141 `· Twisting…` spinner glyph now matched by
  `claudeSpinnerRegex`.** The existing regex covered the `✻ <verb> for <N>s`
  shape but missed the new `· <verb>…` variant introduced in Claude Code
  2.1.141. The watchdog (`nudgeSilentPaneAfter`) and pane-state detection
  (`paneAgentEngaged`) misclassified panes spinning with the dot-glyph as
  silent, leading to occasional spurious nudges on actively-thinking agents.
  Extended the regex with an alternation branch covering the dot-glyph form;
  added `TestClaudeSpinnerRegex_DotGlyph` with 5 variant samples (thrum-fyza).

- **kfn3 self-echo phantom nudge.** Outbound `thrum send` from agent A to agent
  B was producing a phantom `New message from @<A>` system-reminder in A's own
  pane on every send. Root cause: `CLAUDE_PROJECT_DIR` leaks through the shared
  tmux server's default-environment, so Claude Code in repo-B's pane resolves
  the `${CLAUDE_PROJECT_DIR}/scripts/thrum-check- inbox.sh` hook against
  repo-A's worktree. Fix scrubs `CLAUDE_PROJECT_DIR` at the existing
  `cleanTmuxEnv` chokepoint, alongside the existing `THRUM_*` and
  `TMUX/TMUX_PANE` scrubs. Scope intentionally narrow to the single variable
  with documented leak evidence; `CLAUDE_API_KEY` and other CLAUDE\_\* vars are
  explicitly preserved by a positive regression test (thrum-jj0a.6).

- **Defense-in-depth self-echo guards.** Independent of the env-leak fix above,
  both the daemon spool dispatcher (`cmd/thrum/main.go`) and the inbox-check
  hook (`scripts/thrum-check-inbox.sh` + the shared template) now drop any spool
  entry whose `from` field matches the receiving agent ID — so a future
  regression upstream cannot reach the user-visible self-echo nudge
  (thrum-44sy).

- **`project-philosophy` skill respects `AskUserQuestion`'s 4-option limit.**
  Step 5 of the skill instructed implementers to surface project rules via
  prompts whose natural option counts exceeded the tool's hard limit of 4
  options. First prompt failed with `Invalid tool parameters` /
  `expected array to have <=4 items`, forcing visible retries. Step 5 now
  directs implementers to use multiple sequential questions (`category?` →
  `specific item within category?`) rather than packing options into one prompt.
  Synced across all 4 runtime plugin copies (thrum-2ut8).

- **Tmux silence watchdog now actually fires for Claude Code agents.** The
  v0.10.3-rc.1 watchdog at `internal/daemon/rpc/tmux.go` compared two pane
  snapshots taken 30s apart and bailed if they differed. Claude Code's 1Hz
  animated thinking spinner (`✻ Sautéed for 4s` → `Churned for 5s`) makes
  consecutive snapshots never byte-equal, so the nudge never fired even when the
  agent had demonstrably not engaged. Codex shows the same shape. Replaced with
  a two-anchor semantic engagement check: find the banner sentinel (top anchor),
  find the per-runtime horizontal-rule chrome separator (bottom anchor), inspect
  lines between them, ignore blanks and the runtime's spinner pattern, treat
  anything else as "agent has engaged → no nudge". Trigger switched from a blind
  30s sleep to silence-driven polling of
  `tmux display-message #{window_activity}` (500ms ticks, 5s silence threshold,
  30s hard deadline) — converges in 5-15s in the common case, exits silently
  when the agent is genuinely busy past the deadline (no spurious nudge into
  real work). Added observability logs on both decision branches so the silent
  bail that hid this bug can't recur (thrum-84xc).

- **CLI now prefers cwd-anchored identity over `THRUM_*` env vars.** Closes the
  CLI half of the cross-worktree-misidentification footgun. The rc.5 daemon-side
  fix (peercred cwd resolution via lsof on macOS) made the daemon correct — but
  the CLI side still consulted env vars FIRST when deciding which worktree to
  dial and which agent_id to claim. Stale env inherited at fork time from a
  parent shell anchored elsewhere caused the CLI to dial a daemon socket that
  didn't exist (or worse, the wrong daemon), bypassing rc.5's correct daemon
  identity resolution entirely. Three coordinated changes in
  `internal/paths/paths.go`, `internal/config/config.go`, and
  `cmd/thrum/main.go`:
  - `EffectiveRepoPath` now walks up from the supplied repoPath looking for a
    `.thrum/` ancestor; if found, that worktree wins. `THRUM_HOME` is now a
    fallback hint used only when cwd has no thrum worktree at or above it
    (preserves the legitimate "pin to bound checkout from outside any worktree"
    use case).
  - `resolveLocalAgentID` now loads cwd-anchored config first; `THRUM_AGENT_ID`
    is a fallback when no cwd identity exists.
  - `loadIdentityFromDir` now falls through to directory scan when `THRUM_NAME`
    points at a file that doesn't exist in cwd's worktree (instead of
    hard-erroring), so stale env hints don't block resolution. Verified
    end-to-end on zarambp14 — a Claude process with stale `THRUM_HOME` /
    `THRUM_AGENT_ID` pointing at `falcon_llm_client` while cwd is in
    `falcon-agent` now correctly self-identifies as the `falcon-agent`
    coordinator without needing `env -u THRUM_*` (thrum-qofl).

- **macOS peer-credential cwd resolution finally actually works.** From sec.2
  (pre-v0.9.0) through v0.10.3-rc.4, the daemon's peer-credential identity
  resolver called `gopsutil.Process.Cwd()` to look up a caller's working
  directory — documented upstream as "not implemented yet" on Darwin and
  returning an error on EVERY call. That error wasn't `ErrAnonymous`, so the
  daemon fell through to the legacy client-asserted-identity path that trusts
  whatever `agent_id` the CLI sends. The CLI builds that claim from
  `THRUM_AGENT_ID` env vars (when set) or a cwd-based identity file lookup, so
  stale `THRUM_*` env vars inherited from parent shells silently overrode
  cwd-based identity on every macOS call. The footgun surfaced repeatedly as
  "agent is misidentified" symptoms that were diagnosed as other things
  (binding-cache staleness, tmux env-leak, etc.). Replaced the gopsutil
  delegation on Darwin with an `lsof -p PID -Fn -d cwd` subprocess — slow path
  (~30ms per call) but reliable; lsof is a system tool always present on macOS
  and the `-F` output format is stable. Linux and other unix continue to use
  gopsutil unchanged. New unit test (`TestProcessCWD_SelfPID`) exercises the
  real path against `os.Getpid()` so the regression can't recur silently. The
  30ms cost will be reduced to microseconds in v0.10.4 by switching to native
  libproc proc_pidinfo (via pure-Go syscall or matrix-built cgo darwin runners —
  both options require goreleaser changes that exceed rc.5 scope) (thrum-2t7d).

- **Watchdog engagement check now ignores Claude's footer-region tip lines.**
  Manual rc.2 verification surfaced a latent bug in the rc.2 engagement check:
  Claude renders contextual tip lines (e.g.
  `tmux focus-events off · add 'set -g focus-events on' to ~/.tmux.conf and reattach for focus tracking`)
  in the band between the spinner and the horizontal-rule divider. The rc.2
  algorithm treated those tips as real agent output → false-positive "engaged" →
  no nudge. Fix: use the spinner line itself as the bottom anchor (with divider
  as fallback for the brief pre-spinner window). Tips below the spinner are now
  out of the decision region. Caught before any user hit it via the deliberate
  post-publish manual verification step (thrum-84xc).

- **Pane-readiness detection rebuilt around tmux silence + 2s settle.** The rc.3
  `waitForPaneReady` still used byte-equality on consecutive 1s pane captures —
  the same broken pattern the watchdog rewrite already replaced. Result: on
  Claude Code the function would either declare ready prematurely (returning
  while a paint cycle was still in flight) or hit its 60s ceiling. Manual rc.3
  verification on zarambp14 caught the symptom: `thrum tmux start` would put the
  printf banner into Claude's input box but the immediately-following Enter was
  swallowed because Claude was not yet input-ready. `thrum:restart` exhibited
  the same shape (no banner emitted at all). Fix: replace the byte- equality
  loop with the same silence-driven polling used by the watchdog
  (`tmux #{window_activity}`, 5s silence threshold, 60s ceiling) plus a 2s
  settle pause after silence is detected before declaring the pane ready for
  keystroke injection. Both `HandleLaunch` and `HandleRestart` paths benefit by
  construction (they share `waitForPaneReady`).

- **TUI input submission no longer races with paste-mode detection.** Even with
  chrome fully rendered, modern TUI runtimes (Claude Code, others) interpret a
  long string immediately followed by Enter as "Enter inside paste" rather than
  "submit", swallowing the submission. New helper
  `sendKeysAndSubmit(target, text)` inserts a 200ms gap between text and the
  Enter keystroke so the input widget exits paste-mode before Enter arrives.
  Used everywhere the daemon submits input to a runtime pane: the identity-
  banner emit (`emitIdentityBanner`), and the `/thrum:prime` send for non- hook
  runtimes from both launch and restart paths.

- **Launch and restart nudge text is now a direct prompt, not a shell command.**
  rc.3 launched with `nudgeSilentPaneAfter` configured to send
  `"thrum inbox --unread"` on the launch path — a shell command, not a prompt.
  An agent receiving that text into its input box has no clear directive to
  re-engage with the prime briefing. Both nudge sites now send a direct
  imperative phrased to handle the partial-engagement case:
  `"Finish reading the prime output and follow your instructions if you have not"`
  (launch) and the same phrasing with the resume-plan reference for restart.

- **Beta-channel install snippets place `VERSION=` on the correct side of the
  shell pipe.** The published install commands at `website/docs/beta-channel.md`
  used `VERSION=vX.Y.Z-rc.N curl ... | sh`, which sets the env var on the curl
  process — sh never sees it and falls back to `latest`. Real-world hit during
  rc.1 soak: the documented command installed v0.10.2 instead of v0.10.3-rc.1.
  Snippets now use `curl ... | VERSION=vX.Y.Z-rc.N sh` and a callout explains
  the gotcha. Same fix applied to the "Current pre-release" callout at the top
  of the page.

### Added

- **Codex plugin first-class support.** `codex-plugin/plugins/thrum/` ships
  SessionStart auto-prime, PreToolUse safety block, and Stop unread-inbox hooks
  plus 14 role-discipline skills synced from `claude-plugin`. New install path
  is a one-command bootstrap:

  ```bash
  bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/thrum-dev/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
  ```

  The script handles the per-plugin cache-staging gap codex 0.130.0 has for
  third-party marketplaces (thrum-pm7n.4).

- **Tmux silence watchdog after launch and restart.** When `thrum tmux launch`
  or `thrum tmux restart` completes, the daemon watches the agent's pane for
  silence and sends a contextual nudge if the agent doesn't engage within the
  configured threshold. Closes the long-standing UX gap where fresh codex agents
  (and large-context restarts in claude) sat idle at a welcome banner or
  post-restart screen. Configurable via the new
  `restart.silence_watchdog_seconds` key (default 30s; negative disables).
  Eliminates the previous hardcoded 10-second pre-inject sleep in favor of a
  pane-stability readiness probe (thrum-puhr.10).

- **Trust-gate detection for codex and claude first-launch dialogs.** A new
  `IsTrustGate` / `IsPaneSafeToType` detector in
  `internal/daemon/permission/detect.go` recognizes the trust prompts both
  runtimes show on first launch in a new directory. The watchdog, identity
  banner, and `/thrum:prime` keystroke paths all consult this detector and skip
  injection when a trust gate is active — preventing the agent from being killed
  by stray keystrokes into a security gate. Match uses a durable
  runtime-agnostic `1.Yes / 2.No / "trust"` proximity pattern plus per-runtime
  exact phrases for defensive precision (thrum-puhr.10 cluster 8).

- **Per-session env scoping at session create.** `tmux.CreateSessionWithEnv`
  injects `THRUM_NAME/AGENT_ID/ROLE/MODULE/HOME/INTENT` per-session via
  `tmux new-session -e KEY=VALUE` AND
  `tmux set-environment -t <session> KEY VALUE` (so subsequent split-window /
  new-window panes inherit correctly). Distinct sessions on a shared tmux server
  now present distinct identities to their initial shells. Complements the
  existing `cleanTmuxEnv` source-side scrub (thrum-jj0a.1).

- **Anti-rush / anti-shortcut discipline in coordinator + orchestrator
  preambles.** Five operational rules codifying the patterns the project has hit
  before — skipping review gates on small diffs, bucketing findings as
  "follow-ups" without justification, shipping a fix labeled X when X's actual
  cause is something else, declaring DONE without verifying the user-visible bug
  is gone, accepting the cheapest path on autopilot (thrum-fu9j).

## [0.10.2] - 2026-05-04

### Fixed

- **`thrum quickstart` no longer rejects idempotent same-name re-register.** The
  G1a `quickstart_self_rename` guard refused every re-register from a caller who
  owned an existing identity, even when the requested `--name` matched the owned
  identity. This broke `scripts/thrum-startup.sh` (the SessionStart hook for
  every claude session) on already-registered worktrees: step 3
  (`thrum quickstart --name $AGENT_NAME`) errored, `set -e` aborted, and steps 4
  (inbox check), 5 (announce), and 6 (cron install) never ran.
  `thrum tmux start` against an already-registered worktree hit the same guard
  via `runInlineQuickstart`, aborting `HandleCreate` before claude launched.
  Same-name re-register is now allowed (idempotent); a real rename (different
  `--name`) still requires `--force` (thrum-gmz2).

- **`tmux.create`-spawned panes no longer inherit `THRUM_*` env vars from the
  daemon.** When the daemon was started from a primed shell, every tmux pane it
  spawned inherited `THRUM_AGENT_ID`, `THRUM_HOME`, etc. — causing the pane's
  `thrum whoami` to resolve to the daemon-starter's identity instead of the
  pane's intended agent. Fix scrubs `THRUM_*` at the central tmux exec
  chokepoint (`safecmd.cleanTmuxEnv`), covering every code path that goes
  through `safecmd.{Tmux,TmuxRun,TmuxExec}` (thrum-8nro.4).

- **Long-running tmux servers no longer leak stale `THRUM_*` env into new
  sessions.** Companion to thrum-8nro.4. Tmux session env is sourced from the
  SERVER's environ at server-start time, not the client connection. So even
  after the daemon-side scrub, sessions created against a long-running tmux
  server inherited whatever environ the server captured weeks/months ago. Fix
  adds per-session `-e KEY=` overrides on `tmux new-session` to neutralize
  server-cached `THRUM_*` values (thrum-t8mj).

- **`thrum purge --confirm` now actually shrinks JSONL message files.**
  `filterSyncFiles` was passing the wrong field name (`"created_at"` vs the
  actual on-disk `"timestamp"`) when filtering messages JSONLs, so every record
  was kept and the on-disk files grew unboundedly. Verified live: a
  `--before 30d` purge against this dev box's 335MB sync dir filtered 13 message
  files in 7.7s, with `supervisor` going 145MB → 134MB and `system` going 80MB →
  76MB (thrum-yzps).

- **Release-test harness coord-whoami probe now retries on saturated dev
  boxes.** The 60s timeout on `setup-repo.sh`'s coord whoami was firing on
  saturated boxes (~60+ tmux sessions, long daemon uptime) due to a
  missed-keystroke race when claude's interactive-input handler wasn't bound at
  the moment the probe sent the bash-prefix. Fix replaces the single 60s wait
  with a 3-attempt × 30s retry loop (90s total), each attempt resending the
  idempotent `thrum whoami --json`. Pure shell-loop, fixture-only, no
  daemon/tmux behavior change (thrum-vjqn).

- **`unauthenticated_rpc` deny message now points at `thrum prime` as the
  cache-warming recovery.** The prior remediation
  (`cd into a registered agent worktree and retry`) was misleading when the
  caller WAS in a registered worktree but the daemon's binding cache hadn't been
  warmed (post-restart, post-edit, etc.). New text leads with `thrum prime` and
  retains cd-into-worktree as the fallback (thrum-8nro.3).

- **r02 remote scenario no longer fails on macOS `/tmp` symlinks.** The
  worktree-field assertion now uses `pwd -P` on both sides of the comparison so
  `/tmp` → `/private/tmp` symlink resolution doesn't trip the test on macOS
  remotes (thrum-vry8).

- **`release.yml` skips `publish-opencode-plugin` when version unchanged.**
  Every thrum tag where `opencode-plugin/package.json` wasn't bumped failed the
  publish job with HTTP 403
  (`You cannot publish over the previously published versions`), making release
  runs look broken even when the rest of the pipeline succeeded. Pre-check via
  `npm view` now gates the publish step; bumping the plugin's version still
  triggers a real publish (thrum-ygf2).

- **`setLocalAgentStatus` no longer applies `paths.EffectiveRepoPath`
  asymmetrically.** Latent trap where the save site wrapped the path through
  `EffectiveRepoPath` while the load site did not, leaving open a scenario where
  a `THRUM_HOME`-set primed shell could write status into the wrong worktree's
  identity file (thrum-8nro.1).

### Added

- **Manual release-test scenario** for the daemon-side `THRUM_*` env scrub
  (`dev-docs/release-testing/full_test_plan.md` Step 10H). Documents the
  procedure to confirm `thrum tmux start` produces a pane whose `thrum whoami`
  resolves to the intended (per-pane) agent even when launched from a daemon
  poisoned by an earlier primed shell. Promotion to an automated scenario is
  tracked under thrum-yopu (deferred to v0.10.3).

### Internal

- **G1a guard tests now exercise the rename-refusal path with an explicit
  different `RequestedName`.** Pre-thrum-gmz2 the test fired the guard with no
  `RequestedName` set, codifying the buggy behavior as the spec. Now
  `TestG1a_CallerOwnsAnExistingIdentity_Refuses` uses
  `RequestedName: "impl_bar"` (different from owned `"impl_foo"`) and a new
  sibling `TestG1a_SameName_ReregisterIsIdempotent` pins the fixed allow-path.

- **Documents the daemon-side env-scrubbing chokepoint** with new doc comments
  on `safecmd.Tmux`, `safecmd.TmuxRun`, and
  `internal/daemon/rpc/tmux.go HandleCreate` so future readers see the scrub
  trail without re-litigating the design choice.

- **Test isolation hardening:** `internal/cli/client_test.go` and
  `internal/config/config_test.go` now have package-level `TestMain`s that
  `os.Unsetenv` `THRUM_*` before `m.Run()`, so tests don't silently inherit
  operator-shell pollution. (Same pattern should be added to
  `internal/identity/guard`, `internal/daemon/rpc`, etc. — tracked under
  thrum-2jbv.)

## [0.10.1] - 2026-05-03

### Fixed

- **`thrum quickstart` from a `.thrum/redirect`-using worktree no longer writes
  the agent identity to `$THRUM_HOME`.** Latent bug since `299131e434` (Mar 6),
  surfaced in v0.10.0 because Epic-D's wizard + worktree-create flow routes
  through `buildInlineQuickstartCmd` more often, sending `thrum quickstart` into
  daemon-spawned panes that inherit `THRUM_HOME` from the daemon's environ. The
  fix has three parts:
  - `cmd/thrum/main.go` PersistentPreRunE now exempts `init` and `quickstart`
    from the `paths.EffectiveRepoPath` substitution that `THRUM_HOME` triggers.
    Register-shape commands use cwd; runtime commands continue to honor
    `THRUM_HOME`.
  - `internal/worktree/worktree.go` `BuildQuickstartCmd` accepts a `repoPath`
    argument and emits `thrum --repo <path> quickstart ...`.
    `internal/daemon/rpc/tmux.go buildInlineQuickstartCmd` forwards `req.Cwd` so
    daemon-inline quickstarts always pin the target explicitly, regardless of
    the daemon process's environ.
  - `internal/cli/quickstart.go` G1a/G1b guard and Step 2.5 enrichment paths no
    longer re-apply `EffectiveRepoPath` to an already-resolved `flagRepo`.
    `internal/config/config.go LoadIdentityWithPath` drops its inner
    `EffectiveRepoPath` call. Two release-test scenarios were added as a
    regression gate:
    `tests/release/scenarios/108-quickstart-redirect-regression.test.sh` (local
    sub-fixture) and
    `tests/release/remote-scenarios/r02-quickstart-redirect-regression.test.sh`
    (remote via run-remote.sh). The local scenario pre-stages a stale parent
    identity with a known intent; the new `parent-identity-untouched` and
    `child-intent-not-inherited-from-parent` assertions catch both file-location
    and Step 2.5-enrichment paths of the bug.

  Refs: thrum-tc4w. v0.10.0 marked prerelease; users should upgrade to v0.10.1.

- **Boot-time identity reconcile.** Write RPCs (`thrum send`,
  `thrum tmux start`, etc.) from a worktree that holds a registered
  `.thrum/identities/ <name>.json` no longer fail with
  `anonymous caller cannot invoke X` after a daemon restart or cold start. The
  peercred resolver matches caller CWDs against
  `session_refs JOIN sessions WHERE ended_at IS NULL` — that view is durable in
  SQLite but loses rows on shutdown / cleanup / long quiescence, so disk-truth
  (identity files) and resolver-truth (DB rows) drift apart. Previously, only
  `thrum quickstart --force` re-populated the rows; `thrum prime` did not. The
  fix walks `.thrum/identities/*.json` at daemon boot via
  `internal/daemon/bootstrap/Reconcile` and inserts the missing
  `(sessions, session_refs)` pairs through `safedb` in a per-identity
  transaction with `INSERT OR IGNORE`. Local-only by design — direct SQL only,
  no JSONL events, no cross-machine sync — because `session_refs` is
  intentionally local-only state in the projector rebuild path. The same pass
  restores the in-memory `sessionCwds` pane-nudge map for any identity whose
  `tmux_session` is still alive, closing a related restart gap. Defensive
  `filepath.IsAbs` guard skips malformed identity files (resilience fixture stub
  `worktree: "test"`). Wires into `cmd/thrum/main.go daemonRun` between
  `tmuxHandler.ReconcilePoller` and `paneSilencePoller.Run` with a 10 s
  `context.WithTimeout` boundary; failure is non-fatal at boot. New release-test
  scenario
  `tests/release/scenarios/109-reconcile-on-boot-restores-write-rpc.test.sh` is
  the regression gate. Refs: thrum-xloz, thrum-soj8, thrum-6kk6.

## [0.10.0] - 2026-05-03

### Added

- **`thrum init` wizard** — `thrum init` on a TTY now launches an opinionated
  interactive setup walking new users through identity, worktrees root, role
  templates, and daemon start in one flow. The legacy silent path is preserved
  when stdin is not a TTY or `--non-interactive` is set, so existing CI scripts
  continue to work unchanged. Pre-fill any prompt with `--name`, `--role`,
  `--module`, `--worktrees-root`, `--roles=enhanced|default|skip`, and
  `--no-daemon` to script the wizard end-to-end.
- **New role template `implementer-worktree-write-only.md`** for the wizard's
  "enhanced" choice. Pins implementers to writes inside their own worktree and
  forbids drive-by edits to the main repo.

### Changed

- **Default worktree base path migrated from `~/.workspaces/<project>` to
  `~/.thrum/worktrees/<project>`.** Users with explicit `Worktrees.BasePath` in
  `.thrum/config.json` are unaffected. Users who relied on the implicit fallback
  can keep existing worktrees in place by setting the override before the next
  worktree create:

  ```bash
  thrum config set worktrees.base_path "$HOME/.workspaces/<project>"
  ```

  The wizard's worktrees-root prompt also accepts the legacy path; pressing
  enter through the prompt with a previously-configured value preserves it.

### Fixed

- **`scripts/thrum-check-inbox.sh` is now correctly added to `.gitignore` (and
  `.git/info/exclude` in stealth mode) alongside `thrum-startup.sh`.**
  Previously only `thrum-startup.sh` was excluded, so the inbox-check helper
  could leak into tracked changes on stealth-mode repos.

## [0.9.2] - 2026-04-29

### Fixed

- **tmux pty leak under repeated `respawn-pane` (thrum-x6e8.5)** — tmux-exec
  migrated from `respawn-pane` to a persistent-session pool. The previous
  approach leaked pseudo-terminals on every respawn, eventually exhausting the
  per-process fd limit on long-running daemons. The pool reuses sessions with
  flock-based coordination (with a portable fallback when flock is unavailable)
  and documents the lifecycle/marker contract for future maintainers.
- **`runPreambleInit` fallback ignored `.thrum/redirect` (thrum-5hhx)** — when
  the rendered template lookup failed and the handler fell back to
  `RoleAwarePreamble`, it skipped the `.thrum/redirect` indirection used by
  worktree setups. Fallback now follows the redirect before resolving the role.
- **`runPreambleInit` and worktree preambles rendered relative strategy paths
  (thrum-rm4x, thrum-z9zl)** — generated preambles referenced
  `strategies/<file>` relative to the rendering CWD, which broke when the
  preamble was read from a different directory. Paths are now rendered absolute
  against the project root.
- **SessionStart identity banner + auto-load directive (thrum-6hqy / 6hqy.1 /
  a6sw / tfrv / xupf / 2qe2)** — Claude Code sessions launched via
  `thrum tmux create` and restarted via `thrum tmux restart` now display a
  pane-side identity banner and a size-aware `MUST-READ` directive pointing at
  the briefing. The plugin SessionStart hook also injects `thrum prime` output
  via `additionalContext` so the briefing reaches the model even when the
  pane-side banner is truncated. Restart-snapshot framing was hoisted to the top
  of `additionalContext` and rephrased as a directive rather than passive prose.

- **`thrum tmux status` and `thrum tmux connect` leaked sessions across daemons
  (thrum-zuz5)** — pass 2 of `HandleStatus` filtered only on `@thrum-managed=1`,
  which is set by every thrum daemon — so sessions from unrelated worktrees and
  projects appeared in the status response and the `connect` picker, and broke
  `make ci` locally on dev machines with any live thrum-managed tmux session.
  `HandleCreate` now also stamps `@thrum-thrum-dir=<this daemon's thrum_dir>`,
  and pass 2 filters on the matching value. **Migration:** sessions created
  before this release will not appear in `thrum tmux status` pass 2 (or the
  `thrum tmux connect` picker) until they are recreated. They are not lost —
  just un-scoped. Recreate via `thrum tmux create` to restore visibility.
- **`thrum context preamble --init` ignored customized role templates
  (thrum-pk2o)** — `--init` called `RoleAwarePreamble(role)` directly, so
  customized templates at `.thrum/role_templates/<role>.md` were silently
  overwritten with the generic default. The handler now consults
  `RenderRoleTemplate` first and only falls back to the generic preamble when no
  rendered template exists.

### Added

- **User overlay composed into rendered preamble (thrum-z2et.19.1)** —
  `RenderRoleTemplate` now appends non-empty `.thrum/context/<agent>.md` content
  after `DefaultPreamble` with a `---` separator. The overlay file is
  auto-created empty by `thrum quickstart` for hand-written customization;
  whitespace-only files are treated as absent so empty overlays don't add a
  stray separator.
- **`role_config` persistence in `.thrum/config.json` (thrum-z2et.20)** —
  `/thrum:configure-roles` answers persist under a new `role_config` top-level
  key (per-role autonomy + scope + rendered_hash). Atomic temp+rename writes
  preserve every other top-level key (backup/daemon/identity/telegram)
  byte-identical via `json.RawMessage` round-trip. New schema version field
  allows future migrations.
- **`thrum roles refresh` regenerates from saved answers** — new CLI subcommand
  under `thrum roles` that re-renders `.thrum/role_templates/<role>.md` from the
  embedded shipped variants + saved answers, then updates `rendered_hash` to the
  current shipped body_hash. Per-agent template tokens (`{{.AgentName}}` etc.)
  are kept literal so the existing per-agent deploy pass can substitute them.
- **`thrum prime` surfaces 3 drift hints** — `roles.config.migration` (rendered
  templates exist, no `role_config`), `roles.config.schema-bump` (shipped
  schema_version > saved), `roles.config.body-diff` (shipped body_hash != saved
  rendered_hash). Hints route via `slog.Warn` → `installSlogBridge`, surfacing
  in the `--json` hints array or stderr at warn level. Precedence is migration >
  schema-bump > body-diff (only one fires per repo).
- **`thrum roles save-config` and `thrum roles templates print`** — internal CLI
  shims used by the rewritten `configure-roles` skill so the skill can persist
  answers and read embedded shipped content via CLI rather than filesystem path.
- **Shipped role templates embedded under `internal/context/roleconfig/`** — the
  19 shipped templates moved from `toolkit/templates/roles/` to
  `internal/context/roleconfig/templates/roles/` and embedded via `//go:embed`
  so they're available regardless of binary cwd. All 19 carry
  `schema_version: 1` YAML frontmatter; body_hash excludes the frontmatter so
  whitespace-only metadata edits don't trigger drift.

### Changed

- **`/thrum:configure-roles` skill rewrite** — uses `AskUserQuestion` for all
  interactive prompts, persists answers via `thrum roles save-config`, and reads
  shipped reference content via `thrum roles templates print` instead of raw
  filesystem paths. Prefills values from saved `role_config` on re-run so the
  user only re-confirms.

## [0.9.1] - 2026-04-24

### Fixed

- **`thrum setup claude-md --apply` documented-but-missing subcommand (issue
  #8)** — external user followed the quickstart and hit
  `Error: unknown flag: --apply` because the `setupCmd` stub at
  `cmd/thrum/main.go` only suggested `thrum worktree setup`. The command is now
  implemented: `thrum setup claude-md` prints the template to stdout, `--apply`
  creates `CLAUDE.md` (template-only) or appends the template to an existing
  file, and `--apply --force` replaces an existing Thrum block idempotently.
  Block is wrapped in `<!-- BEGIN THRUM -->` / `<!-- END THRUM -->` markers for
  detection. Template (`internal/cli/templates/claude-md/thrum-block.md`,
  embedded via `go:embed`) is intentionally minimal for users not running the
  Thrum plugin; plugin users should not run this command since it would
  duplicate what the plugin already injects.
- **Peercred resolver error taxonomy (thrum-ndtw)** — v0.9.0 wrapped
  introspection failures (kernel peer-creds via `tspeer.Get`, `gopsutil.Cwd`)
  with `ErrAnonymous`, which routed through `server.go`'s anonymous-allowlist
  and rejected mutating RPCs. Observed 2026-04-24: claude-code Bash subprocesses
  on macOS hit `gopsutil.Cwd` races (subprocess exits before introspection
  completes), and interactive zsh callers hit the same path — both surfaced as
  `anonymous caller cannot invoke X: cd into a registered agent worktree and retry`
  even from correctly registered CWDs. Fix: drop the `ErrAnonymous` wrap at
  steps 1 (`tspeer.Get`, PID=0) and 2 (`gopsutil.Cwd`). Raw errors now return,
  and `server.go` falls through to legacy client-asserted identity (pre-v0.9.0
  behavior). Steps 3 (`findGitRoot` empty) and 5 (`matchWorktree` no-match)
  still wrap `ErrAnonymous` — those are provable evidence that the caller is
  outside every registered worktree. `slog.Warn` now fires at both introspection
  paths (`step=pid failed`, `step=cwd failed`) for diagnostics. Net-zero
  security regression: reinstates pre-v0.9.0 behavior only on the "unknown
  state" path. Provably anonymous callers still hit the allowlist.

## [0.9.0] - 2026-04-23

### Added

- **CLI hints infrastructure (thrum-rqkf Phase A-B)** — pre/post-action hint
  pipeline wired into `thrum init`, `thrum send`, `thrum tmux create`; shape-B
  text + shape-C JSON renderers; stable hint-code registry; `THRUM_NO_HINTS=1`
  opt-out. Unified framework for actionable CLI warnings.
- **CLI `--json` output contract (thrum-swg2)** — `slog→hint` bridge installed
  in `PersistentPreRunE`; all `--json` commands emit via central `cli.EmitJSON`
  / `EmitJSONWithHints` helpers. `slog.Warn` records are grafted under a
  top-level `"hints"` key instead of polluting stdout. 46 JSON sites migrated.
  Stable contract for agent harnesses that merge stdout+stderr via
  `tmux capture-pane`.
- **Identity guard system (thrum-xir.20 family, thrum-38u7)** — cross-worktree +
  cross-host PID guards prevent accidental identity hijack; daemon-side resolve
  authenticates against peercred-verified worktree.
- **Tmux identity invariants (thrum-x6e8 family)** — `--no-agent-pid` flag for
  inline quickstart; `HandleLaunch` clears stale subshell PID before writing
  `tmux_session`; absolute worktree path + `NormalizeWorktreePath` helper;
  bare-name self-heal. `HandleCreate` now blocks until the inline quickstart
  writes the identity file (thrum-ns0b), eliminating a create→launch race.
- **No-agent tmux sessions (thrum-ufv5.11/.12)** — `thrum tmux status` lists
  sessions created with `--no-agent` via a new `@thrum-managed=1` user-option
  tag; `thrum tmux send` bypasses the queue and injects raw keystrokes when the
  session has no registered agent.
- **Role-scoped prime (thrum-ir2a)** — `thrum prime` filters `project_state.md`
  sections by the calling agent's role so implementers / testers don't see
  coordinator-only content.
- **Plugin skills slate** — 5 new skills: `project-philosophy`,
  `verify-against-plan`, `efficient-multi-agent-research`,
  `adversarial-critique`, `project-setup`.
- **Cross-host mesh tooling** — `scripts/remote-tmux-exec` wrapper +
  `tmux-exec shell-run` subcommand for Topology B/C (local ↔ mac mini ↔ ubuntu)
  validated bringup; required on macOS because fresh `sh -c` subshells fail
  peercred authentication for mutating RPCs.
- **Permission prompt detection and supervisor nudge** — when a tmux-managed
  agent hits a permission prompt it cannot auto-approve, thrum detects the stuck
  state and routes a rich actionable notification to configured supervisors.
  Supervisors reply `y`/`n` from the CLI, web UI, or a Telegram message, and the
  answer is replayed into the agent's pane as real keystrokes. Works across a
  synced thrum network — a reply on any repo in the network is dispatched to the
  daemon that owns the pane. Supports Claude Code, Codex, Cursor, OpenCode,
  Kiro-CLI, and Auggie (tool-approval pattern). See
  `dev-docs/specs/2026-04-14-permission-prompt-detection-design.md`.
- **`@supervisor_<project>` reserved pseudo-agent** — registered at daemon boot
  as the canonical author of permission nudges. Visible in
  `thrum team --system`; hidden from default `thrum team` listings with a new
  `⊙` reserved glyph in compact output.
- **`thrum team --system`** flag — surfaces reserved pseudo-agents in team
  listings, including the permission supervisor and any future daemon-internal
  agents.
- **Permission nudge reminder cadence** — exponential backoff at 0 / 5m / 15m /
  45m / 2h / 4h. After six nudges without a supervisor response, the scheduler
  marks the agent as `stuck` in its identity file so the UI, `thrum team`, and
  other consumers can reflect that the agent is blocked.
- **Restart resilience** — pending nudges persist in a new `permission_nudges`
  SQLite table and survive `thrum daemon restart`; the daemon logs
  `permission found N pending nudge(s) still in flight` on startup, and
  reminders resume at the correct cadence automatically.

### Changed

- **`HandleCheckPane` is the single source of truth for runtime resolution** —
  the CLI `thrum tmux check-pane` no longer computes a reason string locally. It
  forwards only `(session, content)` and the daemon resolves identity via
  `findIdentityForSession`, reads runtime from the identity file, and runs
  `DetectPaneState` itself. Eliminates a class of bugs where the CLI and daemon
  disagreed on which identity owned a session (the CLI was reading identity from
  tmux-server cwd, not the agent's worktree).
- **`permission_supervisors` config key** — per-project list of supervisor
  agents to nudge. Defaults to role `coordinator` when unset. See
  `internal/config/daemon.go` `PermissionSupervisors`.
- **`project_name` config key** — owner identifier for the local
  `@supervisor_<project>` pseudo-agent. Falls back to `filepath.Base` of the
  repo path.

### Breaking changes

- **`thrum peer add` and `thrum peer join` now require `--type <transport>`
  (xir.27).** The previously-implicit `tailscale` default has been removed. Four
  values are accepted, each gates both the peercode emission and the handshake
  dial:
  - `--type tailscale` — current behavior (tsnet peercode, Tailscale dial,
    requires `THRUM_TS_AUTHKEY`).
  - `--type local` — same-host loopback (`127.0.0.1` peercode, direct localhost
    dial, no tsnet). Use for sibling-repo bridges on one host.
  - `--type network` — same-LAN, no Tailscale. Requires `--address <ip>`; the
    subnet is inferred from the NIC that owns the supplied IP. Direct TCP, no
    tsnet.
  - `--type repair` — re-verify and reconcile an EXISTING peer entry using
    stored secrets in `peers.json`. Valid only on `peer join`; rejected on
    `peer add`. Used to recover from drift (e.g., after a peer `daemon_id`
    rotation). Implemented as a dedicated `peer.repair` RPC
    (token-authenticated, NOT an extension of `pair.request`): the dialer looks
    up the stored `Token`, `Address`, and `Transport`, dials the peer's
    WebSocket with `Authorization: Bearer <token>`, calls `peer.repair` with its
    current identity, and receives the listener's refreshed metadata in return.
    Both sides re-key the peer entry if the daemon_id has rotated; `Name` and
    `Token` are preserved. Works for `local`, `network`, and `tailscale`
    transports. Migration: any script calling `thrum peer add` (no flag) must
    add an explicit `--type tailscale` for the same behavior. Missing `--type`
    errors with a help block listing all four options and a one-line "when to
    use" for each — the canonical instance of the CLI-hint pattern.

### Security

- **WebSocket origin allowlist (sec.1)** — `CheckOrigin` now restricts browser
  WebSocket connections to `http://{localhost,127.0.0.1}:{daemon_port}` and
  `ws://` equivalents. Foreign origins receive HTTP 403 at the handshake.
  Previously returned `true` unconditionally, allowing any website to connect to
  the local daemon.
- **Kernel-verified caller identity (sec.2 + sec.3)** — unix-socket connections
  are now identified via `SO_PEERCRED` (Linux) / `LOCAL_PEERPID` (macOS) peer
  credentials, replacing the client-asserted `CallerAgentID` trust model. The
  connecting process's PID → CWD → git root → registered agent worktree match is
  resolved server-side. Forged `caller_agent_id` claims are rejected with a
  clear "identity mismatch" error. Uses `tailscale/peercred` (already an
  indirect dependency) and `gopsutil/v3` for cross-platform PID → CWD
  resolution.
- **Anonymous caller read-only allowlist (sec.3)** — callers without a resolved
  identity (CLI invoked outside any registered worktree) may only invoke
  read-only RPCs (30 methods: team.list, agent.list, message.list, health,
  session.list, etc.). Mutating RPCs are rejected at the dispatcher before the
  handler runs. Preserves the `cd ~ && thrum team` workflow.
- **Author-only message deletion (sec.4)** — `message.delete` now resolves
  caller identity and verifies the caller authored the target message, mirroring
  the existing `message.edit` author check. Previously, any caller could
  soft-delete any message by ID.
- **Bulk hard-delete identity enforcement (sec.8)** — `message.deleteByAgent`
  now requires caller == target agent (agents can only bulk-delete their own
  messages). `message.deleteByScope` is restricted to daemon-internal callers
  only. Both operations were previously callable by any local process without
  identity verification, enabling cascading hard-deletes across 5+ FK tables.
- **Bulk hard-delete removed from WebSocket transport (sec.8)** —
  `message.deleteByAgent` and `message.deleteByScope` are no longer registered
  on the WebSocket handler registry. A source-scan structural guard test
  prevents re-registration.

### Fixed

- **macOS daemon peercred anonymous-latch (thrum-g8e8, thrum-9165)** —
  peer-credential identity resolution now runs per-RPC instead of once per
  unix-socket connection. The old connection-level cache latched `ErrAnonymous`
  if the agent lister was momentarily empty at connect-accept time (e.g.
  `quickstart`'s connection accepts before `session_refs` is written), forcing
  `thrum daemon restart` as a workaround on macOS before mutating RPCs worked.
  Also added diagnostic slog at each resolve step and canonicalized worktree
  paths at `session_refs` write time via `filepath.EvalSymlinks` (pair with the
  resolver's both-sides canonicalization to close `/tmp` ↔ `/private/tmp`
  asymmetry on macOS).
- **Cross-host recipient-stale hint (thrum-has1)** — `send.recipient-stale` hint
  is now suppressed when the recipient's `origin_daemon` is a peer daemon.
  Heartbeats are DB-only by design (thrum-iyrt) and don't propagate across peer
  daemons, so a peer-hosted recipient's `last_seen` is structurally stale; the
  old warning fired on every cross-host send with a misleading "may be idle"
  message. Added `IsLocal bool` to `TeamMember` and `AgentSummary`; `sendHints`
  gates on it.
- **`thrum peer add` no longer prompts for `THRUM_TS_AUTHKEY` when the daemon's
  tsnet is already up (xir.26).** The CLI now queries `health` before prompting;
  if `health.tailscale.enabled` is `true` the daemon already has a tsnet node
  with cached credentials and the prompt is skipped. The prompt still fires when
  env is empty AND the daemon's tsnet is missing or unreachable.
- **`thrum init` now correctly attaches to existing `origin/a-sync` on fresh
  clones.** Previously, running `thrum init` in a freshly-cloned repo whose
  remote already had an `a-sync` branch would create a disjoint local orphan
  that could never be reconciled with the remote — every daemon sync tick
  rejected with "non-fast-forward", silently blocking outbound sync forever. The
  fix adds a decision matrix that checks for `refs/remotes/origin/a-sync`
  (populated by `git clone`) and attaches local `a-sync` to it instead of
  creating an orphan. `--force` reinit also reconciles content-based state: if
  both local and remote have events, init errors out with the two recovery
  commands the user can pick between instead of clobbering either side. A future
  `thrum doctor --fix` (tracked as `thrum-uvpp.1`) will automate recovery for
  machines already in the bad state.
- **`SendSupervisorMessage` @-prefix normalisation** — supervisor nudges no
  longer ghost to recipients with a leading `@` that doesn't match the
  `message_refs` / `message_deliveries` schema (which store bare agent IDs).
  Normalises `@name` to `name` before writing, matching the
  `internal/daemon/rpc/message.go` TrimPrefix convention used by the regular
  send path.
- **`queryAgentsByRecipient` reserved-identity fallback** — replies addressed to
  `@supervisor_<project>` no longer fail with `unknown recipient` at validation
  time. The validator falls through to a single-file identity lookup when the
  agents-table query returns empty, accepting names that have `Reserved=true` in
  their identity file.
- **Quickstart runtime field backfill (thrum-yl3k)** — new quickstart runs now
  populate the `runtime` field from `preferred_runtime` at identity-save time. A
  one-shot self-heal at daemon boot scans all identity files and backfills
  `runtime` from `preferred_runtime` where missing. Eliminates the need for
  manual re-registration of pre-runtime-tracking agents.
- **Permission reminder replies now resolve (thrum-zaxt)** — replying to
  reminder #N's message_id (rather than the original firstDetect message_id) now
  correctly resolves and deletes the pending nudge row via a thread_id fallback
  lookup in `TryResolve`. Previously, only replies to the original firstDetect
  message worked; replies to subsequent reminders were silently ignored.
- **Permission recovery after queued command (thrum-4ten)** — `HandleCheckPane`
  now runs `OnRecovery` unconditionally when the pane is not in a permission
  state, removing the `paneState == "idle"` guard that caused stale
  `permission_nudges` rows to persist after a supervisor answered via
  `thrum tmux send <session> Escape`. The guard excluded the `command_completed`
  and `working_but_idle` branches from cleanup.

### Known issues

- **Pre-existing agent worktrees** — agents quickstarted before the
  runtime-tracking field was added previously required manual re-registration.
  This is now auto-healed at daemon boot (see "Quickstart runtime field
  backfill" above).

## [0.8.2] - 2026-04-13

### Added

- **Cursor Agent plugin** — Full plugin with 5 hooks, 2 rules, 4 skills, 11
  commands, MCP config, and `local-install.sh` for deployment
- **Reusable test infrastructure** — `scripts/test-setup.sh` and
  `scripts/test-teardown.sh` for isolated plugin testing across all runtimes
- **Unified agent test plan** — Runtime-parameterized test plan covering hooks,
  skills, commands, MCP, registration, and messaging round-trip
- **Tmux session titles** — Terminal tabs show `@agent_name` via
  `tmux rename-window` and `set-titles` on session creation
- **`safecmd.TmuxExec`** — Process replacement for tmux attach, enabling proper
  terminal title propagation
- **Pre-commit guard** — `scripts/hooks/pre-commit` blocks accidental commits of
  `dev-docs/` files; hooks moved to repo-tracked `scripts/hooks/`
- **`sync_cursor()` in sync-skills.sh** — Cursor plugin added as sync target
  alongside codex and opencode

### Fixed

- **Monitor delivery (P0)** — `HandleStart` now registers synthetic
  agent+session for `monitor:<name>` sender identity so matched lines actually
  deliver messages
- **Sync worktree (P2)** — `SyncLoop.Start()` now calls `CreateSyncWorktree`
  before starting the loop, fixing "must be run in a work tree" errors in
  local-only mode
- **Daemon auto-start** — Restored in `thrum init` (accidentally removed during
  CLI audit)
- **Runtime set-default** — Now persists to `.thrum/config.json` in addition to
  user-level `runtimes.json`
- **Worktree base_path validation** — Auto-appends repo name to stale configs
  missing it, preventing worktrees from colliding in a flat directory
- **Tmux identity write** — `writeTmuxToIdentity` now scans all worktree
  identity dirs, not just the main repo
- **Resilience tests** — Removed reference to deleted
  `rpc.NewSubscriptionHandler`
- **tmux-exec quoting** — `cmd_exec` now uses `printf '%q'` for proper argument
  quoting

### Changed

- **CLI audit** — Removed groups as user-facing concept, restricted `--to` to
  agent IDs + `@everyone`, removed subscribe commands, -2400 lines across 24
  files
- **Git history cleanup** — Purged `dev-docs/` from git history via filter-repo
  (~9.5 MB removed)
- **Branch cleanup** — Deleted 21 stale remote branches, pruned local branches

## [0.8.1] - 2026-04-10

### Fixed

- **npm publish CI** — `opencode-plugin/package-lock.json` was gitignored,
  causing `npm ci` to fail in the release workflow. Un-ignored via `.gitignore`
  negation pattern.

## [0.8.0] - 2026-04-10

### Added

- **Tmux command queue** — daemon-managed per-session FIFO queue for sending
  commands to tmux panes. `thrum tmux queue`, `thrum tmux queue-status`, and
  `thrum tmux cancel` CLI commands. `tmux.queue`, `tmux.queue-wait`,
  `tmux.queue-status`, and `tmux.cancel` RPC methods. SQLite persistence (schema
  v18/v19), configurable per-command `silence_ms` and `notify_on_complete`
  flags, `@system` virtual identity for result delivery, restart recovery for
  interrupted commands, dead session drain.
- **Multi-runtime tmux** — `ClaudePID` renamed to `AgentPID` across RPC,
  projection, and schema (v17). `PreferredRuntime` field in identity file.
  `--runtime` flag on `thrum quickstart`. Runtime-agnostic `HandleLaunch` —
  OpenCode, Codex, and other runtimes now launch via tmux alongside Claude Code.
  JSONL extraction skipped for non-Claude runtimes.
- **Orchestrator role** — `thrum worktree create/teardown/list` commands for
  managing agent worktrees. `thrum agent set-status` CLI + `agent.set-status`
  RPC. Auto-nudge for agents with working status but idle pane. Orchestrator
  role preamble template. `thrum:orchestrate` execution playbook skill.
  `COORDINATOR` renamed to `SUPERVISOR` in implementation prompts. Review gate
  template between epics.
- **Daemon logging** — lumberjack log rotation for `daemon.log`.
  `thrum daemon logs` command with `--since`, `--tail`, `--follow` flags.
  Configurable `daemon.log_level` via slog. Telegram debug logging gated behind
  log level.
- **Open Code plugin** — `opencode-thrum` npm package with TS hooks, asset
  installer, runtime-aware prime. `opencode` runtime preset in registry.
- **Codex plugin** — skill bundle aligned with claude-plugin source of truth.
- **Website restructure** — hub-and-spoke landing page, scenario-based
  onboarding, new sidebar categories, orchestrator/multi-runtime/peers reference
  docs, voice pass across all pages.

### Changed

- **safecmd migration** — 47 `exec.Command` call sites migrated to `safecmd`
  wrappers across 11 production files (thrum-xir.3). New `safecmd.Tmux`,
  `safecmd.TmuxRun`, `safecmd.TmuxLocal` with 5-second timeouts and clean
  environment. `safecmd.GitConfig` wrapper for reading git config without thrum
  user overrides. `cleanEnv` consolidated from tmux package into safecmd as
  `cleanTmuxEnv`.
- **`who-has` live git extraction** — `HandleListContext` now calls
  `gitctx.ExtractWorkContext` on each result's worktree path, replacing stale
  cached data with live git state (~500ms for all worktrees).
- **Prime identity refresh** — `thrum prime` now updates `PreferredRuntime` and
  `Branch` in the identity file when they differ from detected values.
  Previously only `TmuxSession` was written back.
- **Queue RPC logging** — all 11 `log.Printf` calls in `queue_rpc.go` migrated
  to `slog` structured logging, routing through the daemon log handler.
- **Unified sync-skills.sh** — single script syncs all runtime plugins from
  claude-plugin source of truth.
- **Telegram docs reframed** — positioned as "unified team inbox" rather than
  standalone bridge feature.
- **Documentation** — 11 doc pages updated for v0.8.0: CLI reference (8 new
  commands), RPC API (5 new methods), identity v4→v5, schema v16→v19,
  configuration (log_level, worktrees, orchestration), tmux command queue
  dispatch, orchestrator role.

### Fixed

- **Queue dispatch for detached sessions** — `alert-silence` tmux hooks only
  fire for sessions with an attached client. Added `IsSilent` immediate dispatch
  when a command is enqueued at position 1 with no active command, plus
  `pollSilenceFlag` fallback that polls the tmux `window_silence_flag` at the
  configured threshold interval.
- **Queue `detectPaneState` gate** — `check-pane` CLI now always calls the
  daemon instead of returning early for normal state, enabling queue dispatch
  notifications for all sessions.
- **Telegram reply routing** — inbound replies now route to the parent message's
  author instead of hardcoded `@coordinator_main`.
- **Prime inbox filtering** — two bugs fixed: `ContextPrime` missing
  `ForAgent`/`ForAgentRole` (new agents saw 380+ messages); time boundary
  missing in `buildForAgentClause` (new agents saw all historical
  group/broadcast). Fixed with whoami fields + `registered_at` floor.
- **TUI retry Enter** — 3-second delayed second Enter in `HandleLaunch` and
  `HandleRestart` for Bubble Tea TUI runtimes (OpenCode) that swallow the first
  Enter during startup.
- **Duplicate prime removed** — CLI `/thrum:prime` send removed; `HandleLaunch`
  owns the T+10s prime.
- **tmux-exec quoting** — `printf '%q'` preserves multi-word arguments in
  `scripts/tmux-exec` run command.
- **slog timestamp parsing** — `thrum daemon logs --since` now parses slog's
  timestamp format correctly.

## [0.7.2] - 2026-04-08

### Changed

- **`thrum tmux launch` auto-primes** — after launching the runtime, the daemon
  sends `/thrum:prime` automatically (via background goroutine with 10s delay).
  This matches the behavior of `thrum tmux start` and ensures agents always load
  their session context on launch. Also applied to `thrum tmux restart`.

### Fixed

- **Tmux server isolation** — daemon-spawned tmux commands (`HasSession`,
  `KillSession`, `SendKeys`, `CapturePane`, `SetMonitorSilence`) now strip
  inherited `TMUX`/`TMUX_PANE` environment variables, ensuring they connect to
  the default tmux server. Fixes `thrum tmux launch/restart/kill` failures when
  the daemon was started inside tmux-exec or other nested tmux sessions.
- **Identity reload guard** — `quickstart` and `init` cobra handlers now load
  existing identity files with agent name-match validation, preventing stale
  identity adoption when a worktree has a pre-existing identity from a different
  agent.
- **ClaudePID/TmuxSession preservation** — `quickstart` and `init` handlers load
  existing identity instead of creating fresh structs, preserving `claude_pid`
  and `tmux_session` fields set by the daemon enrichment block.
- **Plugin SessionStart hook** — hook now echoes instruction to run
  `/thrum:prime` in-conversation instead of executing `thrum prime` directly,
  which consumed restart snapshots in system-reminder context where the agent
  couldn't act on them.
- **JSONL CWD path encoding** — `encodeCwd` now replaces both `/` and `.` with
  `-`, matching Claude Code's encoding behavior. Paths containing `.workspaces`
  now resolve correctly for session JSONL lookup.
- **Nudge dedup removed** — rapid-fire messages now each trigger a separate
  nudge instead of being coalesced.
- **Restart save identity resolution** — fixed restart snapshot extraction when
  `ClaudePID` is 0 by falling back to daemon RPC.

## [0.7.1] - 2026-04-07

### Added

- **Session restart** — JSONL conversation extraction with truncation to
  exchange boundaries, snapshot save/restore/check commands,
  `thrum tmux restart` RPC for coordinator-initiated restarts, `/thrum:restart`
  skill for self-initiated restarts, auto-restart threshold configuration, and
  automatic snapshot inclusion in `thrum prime` output.
- **Tmux session management** — `thrum tmux connect` interactive session picker,
  `thrum tmux start` one-command agent launch (create + launch + prime +
  attach).
- **Plugin TMUX_SESSIONS.md resource** — new resource documenting tmux-managed
  sessions as the recommended message delivery approach, including full agent
  worktree setup sequence.
- **Auto-restart check script** — `auto-restart-check.sh` for context threshold
  monitoring (not wired to hook — requires external trigger).

### Changed

- **Tmux-first plugin** — SKILL.md rewritten with tmux sessions as the
  recommended message delivery approach, listener pattern as fallback.
  LISTENER_PATTERN.md gets tmux-first note. CLI_REFERENCE.md updated with tmux
  commands.
- **`thrum tmux launch` runtime detection** — reads configured runtime from
  `.thrum/config.json` instead of hardcoding `claude`.
- **Prime output** — non-tmux multi-agent agents now see a tip suggesting
  `thrum tmux start`. Tmux-managed agents see "no listener needed" message.
- **Stop hook** — skips listener PID check for tmux-managed agents.
- **Post-compact hook** — skips listener warning for tmux-managed agents.
- **Coordinator preamble** — CRITICAL section on Thrum dispatch (never spawn
  sub-agents to worktrees), Sub-Agent Dispatcher anti-pattern added.
- **auggie-mcp cleanup** — replaced all auggie-mcp codebase-retrieval references
  with standard Claude Code tools across 22 files.

### Fixed

- **Agent delete dialog** — Web UI now passes full agent ID to delete dialog
  instead of display name, preventing wrong-agent deletion when IDs contain
  colons.
- **HandleRestart safety** — resolves worktree path before killing the session
  (no rollback-free kill). `Restore` handles `os.Rename` error with fallback.
- **Double-snapshot prevention** — `HandleRestart` skips JSONL extraction when
  snapshot already exists.
- **Tmux bugs** — missing Enter in HandleSend, worktree-blind session discovery
  in HandleStatus, worktree-blind nudge lookup in resolveNudgeTarget,
  HandleCheckPane stub replaced with logging.
- **`.consumed` cleanup** — stale `.consumed` snapshot files added to
  `thrum purge` scope.
- **PID fallback** — `thrum restart save` falls back to daemon RPC when
  `ClaudePID` is 0 in identity file.

## [0.7.0] - 2026-04-06

### Added

- **Unified cross-repo communication** — Four-layer transport architecture
  (Network → Transport Bridge → Routing → Application) connecting agents across
  repos and machines via WebSocket peering. Includes `TransportBridge`
  interface, shared `WSClient`, common relay logic, `PeerTransport` for remote
  daemons, `PeerBridge` lifecycle, `PeerManager` with auto-connect and
  exponential backoff, `peer configure` CLI for proxy agent management, 16-char
  numeric pairing code, and optional token auth on WebSocket upgrade.
- **PID identity resolution** — Process tree walk identifies agents by their
  Claude PID, eliminating identity conflicts in multi-agent sessions. Includes
  adoption logic for stale/human-owned identities, schema v16 (`claude_pid`
  column), `[live]`/`[stale]` indicators in `thrum team`, and quickstart gating
  on PID liveness.
- **Telegram group bridge** — Human-to-agent messaging via Telegram groups with
  `@mention` routing, proxy agent registration, conditional IsBot gate with
  trusted bot allowlist, and web UI groups management panel.
- **Three-tier context model** — `project_state.md` skeleton on init,
  `thrum prime` as single complete session briefing with inline preamble and
  project state, `update-project` skill for session summaries, and
  `context show --project/--session` flags.
- **Single-agent mode** — Config flag, `thrum single-agent-mode` toggle command,
  stop hook and startup gated on mode, default preamble stripped to
  mode-independent content.
- **PID file spawn coordination** — `thrum wait` writes PID file, shell scripts
  check PID instead of heartbeat, simplified listener spawn instructions.
- **E2E test tmux isolation** — All E2E tests migrated to tmux-based command
  isolation. Global setup cleans THRUM\_\* env vars before tmux server starts.
- **`scripts/tmux-exec` CLI** — Standalone bash script for isolated tmux command
  execution (create/run/exec/send/capture/destroy) with `--clean` flag.
- **Telegram UI enhancements** — Setup wizard, pairing flow, allow list
  management, groups management in settings panel.

### Changed

- **Tailscale sync migrated to WebSocket** — Sync transport moved from raw
  TCP/NDJSON to WebSocket via shared `TransportBridge`. Breaking change for
  existing Tailscale-paired peers (re-pair required).
- **Bridge components extracted to shared package** — `internal/bridge/` now
  contains `TransportBridge` interface, `WSClient`, `MessageMap`, and common
  relay logic used by both Telegram and peer transports.
- **Default preamble is mode-independent** — Messaging protocol content moved to
  multi-agent preamble only; single-agent mode gets a clean minimal preamble.
- **Plugin version bumped to 0.7.0** for marketplace pre-release testing.

### Fixed

- **Telegram group relay** — Fixed missing `group` field and wrong
  `caller_agent_id` in group inbound relay.
- **Telegram group bridge routing** — Fixed DM path matching before group/proxy
  paths, reply-to-group routing, and proxy agent registration.
- **Stop hook scoping** — Unread count now scoped to current agent identity.
- **Quickstart self-conflict** — Allow name changes within the same session
  without triggering PID conflict.
- **Base32 hash detection** — Exclude Crockford-invalid letters (I, L, O, U).
- **Peer code stdin** — Support `--peercode -` for piped input.
- **Wait heartbeat** — Update heartbeat timestamp after successful RPC call.

### Removed

- **`thrum setup claude-md` command** — Deleted in favor of manual CLAUDE.md
  management.
- **`update-context` command** — Superseded by `/thrum:update-project` skill.

## [0.6.3] - 2026-03-28

### Added

- **Message-listener cron watchdog** — A cron-based watchdog automatically
  re-arms the background message listener when it exits. Previously, agents had
  to manually re-spawn the listener after each cycle; now recovery is fully
  automatic. Eliminates the most common cause of missed messages in long-running
  sessions.
- **Extended listener budget** — Message-listener cycle count increased from 10
  cycles (~80 minutes) to 30 cycles (~4 hours). Combined with the watchdog
  pattern, a single listener setup now provides continuous coverage without
  manual intervention.

### Changed

- **Listener token usage** — The extended budget and watchdog pattern together
  reduce listener token consumption by ~65% compared to the previous frequent
  re-arm model.

## [0.6.2] - 2026-03-27

### Fixed

- **Sync-aware purge** — `thrum purge` now propagates across Tailscale-synced
  nodes. Previously, purged messages, sessions, and events would resurrect when
  a peer synced its unpurged data back. The purge handler now emits a
  `purge.executed` event that peers apply automatically, and the `SyncApplier`
  rejects any incoming events older than the latest purge cutoff.
- **Agent deletion propagation** — Deleting an agent from the web console or CLI
  now fully scrubs all related data (messages, sessions, events) on peer nodes.
  Previously, `agent.cleanup` only deleted the agent row, leaving orphaned data
  that could resurrect the agent via sync.

### Added

- `purge_metadata` table (schema v15) — stores the latest purge cutoff for
  sync-aware filtering
- `purge.executed` event type — propagates purge operations to Tailscale-synced
  peers
- `applyPurgeExecuted` projector handler — auto-purges SQLite on peers when
  `purge.executed` arrives via sync

## [0.6.1] - 2026-03-24

### Added

- **Telegram pairing flow** — Interactive onboarding for the Telegram bridge.
  `thrum telegram configure` now automatically restarts the daemon and enters a
  pairing mode that captures your Telegram user ID when you send the first
  message. No more manually looking up IDs or editing config files.
  - `thrum telegram pair` — standalone pairing for already-configured bridges
  - `--allow-from` flag for non-interactive setup when the ID is known
  - `--pair-timeout` controls the pairing window (default 60s, max 5 minutes)
  - `--skip-pair` writes config only without interactive pairing
  - `telegram.pair` RPC with bridge readiness polling and timeout cap
  - Pairing security model: short window, explicit consent, single session, no
    persistent state change, fail-closed on decline or timeout

## [0.6.0] - 2026-03-21

### Added

- **Telegram bridge** — Bidirectional messaging between Telegram and Thrum.
  Bridge runs as an isolated WebSocket RPC client inside the daemon (no internal
  imports — fail-closed security boundary). Inbound messages routed from
  Telegram users to Thrum agents; outbound replies threaded back to the
  originating Telegram chat. Access controlled via AllowFrom whitelist (empty
  blocks all).
  - `thrum telegram configure` — set bot token and allowed user IDs
  - `thrum telegram status` — check bridge connection and config
  - `thrum telegram pair` — interactive account pairing
  - Per-user rate limiting on inbound messages
  - PingHandler keeps WebSocket alive during idle periods
- **Conversation UI** — Threaded conversation timeline replaces flat inbox as
  default dashboard view. ConversationList sidebar with ConversationView chat
  layout.
- **Telegram settings panel** — Configure and monitor the Telegram bridge from
  the Web UI with live status and token management.
- **Role-aware preambles** — 9 built-in roles (coordinator, implementer,
  reviewer, planner, tester, researcher, architect, documenter, analyst) get
  role-specific preamble headers with Anti-Patterns sections.
  `thrum preamble --init` is now role-aware.
- **Behavioral anchoring in DefaultPreamble** — Rewritten with Operating
  Principles, Startup Protocol, and Anti-Patterns (Deaf Agent, Silent Agent,
  Context Hog) for stronger agent behavior.

### Fixed

- **Context RPC repo_path** — Context save/show RPCs now pass the worktree's
  repo_path, fixing preamble and context files being written to the wrong
  `.thrum/` directory in multi-worktree setups.
- **Peer join positional arg** — `thrum peer join` now accepts peercode as a
  positional argument in addition to `--peercode` flag, stdin pipe, and
  interactive prompt. Fixes "flag needs an argument" error when piping.
- **Stop hook message reminder** — Stop hook now reminds agents to mark messages
  as read before session end.
- **SettingsView test mocks** — Added missing Telegram hook mocks to UI test
  suite.
- **Resilience test timing** — Fixed flaky `TestTimeout_HandlerDeadlineEnforced`
  benchmark by adding polling for handler context cancellation flag.

## [0.5.9] - 2026-03-18

### Added

- **Tailscale sync .env auto-loading** — Daemon automatically reads `THRUM_TS_*`
  and `TAILSCALE_*` variables from `.env` (repo root or `.thrum/.env`). No more
  manual `export` before daemon start.
- **15-second sync interval for Tailscale peers** — Reduced from 5-minute
  periodic fallback to 15-second interval with 10-second recent threshold.
  Combined with push notifications, cross-machine messages arrive in under 20
  seconds.
- **Initial sync on scheduler startup** — Periodic sync scheduler now runs an
  immediate sync when starting, instead of waiting for the first tick.

### Fixed

- **Tailscale long-poll timeout** — Every RPC had a 10-second context timeout,
  killing `peer.wait_pairing` instantly. Added `RegisterLongPollHandler` with
  6-minute timeout for pairing operations.
- **Tailscale peer addressing** — Use tsnet Tailscale IP addresses instead of
  hostnames for `peer join`. tsnet creates `-1` suffix hostnames that regular
  DNS cannot resolve.
- **Background listener preamble** — `DefaultPreamble()` had a standalone
  `Wait for messages` line but no background listener spawn instructions.
  Replaced with `Background Message Listener` section containing the
  `STEP_1`/`STEP_2` spawn pattern that survives context compaction.

### Changed

- **Tailscale docs rewrite** — Updated env var names (`THRUM_TS_AUTHKEY` not
  `THRUM_TS_AUTH_KEY`), documented `.env` auto-loading, hostname requirement,
  tsnet `-1` suffix, IP-based peer join, and updated sync intervals throughout.

## [0.5.8] - 2026-03-17

### Added

- **`thrum purge` command** — Remove old messages, sessions, and events before a
  cutoff date. Supports relative durations (`2d`, `24h`), date-only
  (`2026-03-15`), and RFC 3339 timestamps. Cleans both SQLite tables and sync
  JSONL files. Agents, groups, and subscriptions are not touched.
  - `--before` flag with flexible date parsing (`internal/timeparse` package)
  - `--all` flag to purge everything
  - `--confirm` flag required to execute (preview by default)
  - `purge.execute` RPC handler with dry-run and execute modes
  - `RemoveBeforeTimestamp()` JSONL filter function
  - Integration tests covering dry-run → execute → agent survival

## [0.5.7] - 2026-03-15

### Fixed

- **Web UI agent deletion** — Register `agent.delete` and `agent.cleanup` on the
  WebSocket registry so the web UI can call them (previously returned "Method
  not found")
- **Agent delete cleanup** — `HandleDelete` now removes orphaned sessions,
  session child tables (refs, scopes), and events from SQLite; also filters
  agent lifecycle events from `events.jsonl` via new `jsonl.RemoveByField()`
  helper to prevent re-projection on daemon restart

### Added

- **a-sync worktree protection** — PreToolUse hook (`block-sync-worktree-cd.sh`)
  prevents `cd`/`pushd` into `.git/thrum-sync/a-sync/` and blocks
  branch-changing git operations (`checkout`, `switch`, `reset`, `merge`,
  `rebase`, `pull`) via `git -C` targeting the sync worktree. Checking out a
  different branch there destroys the entire `.git` directory.

## [0.5.6] - 2026-03-14

### Agent Detection & Skills Installer

New data-driven agent registry with 3-tier detection (environment variables,
config files, binary verification) replaces hardcoded runtime checks.
`thrum init --skills` installs agent-agnostic Thrum skills without full runtime
setup — useful for multi-agent environments where agents just need messaging
commands.

### Added

- **3-tier agent detection** — registry-driven detection via environment
  variables (tier 1), config files (tier 2), and binary verification (tier 3)
- **Data-driven agent registry** — built-in definitions for Claude Code, Codex,
  Aider, and other runtimes; `SupportedRuntimes` derived from registry
- **`thrum init --skills`** — lightweight skill installation with agent-aware
  path resolution; detects existing plugin before installing
- **Embedded skill content** — agent-agnostic Thrum skill shipped inside the
  binary for install without network access
- **Explicit mark-as-read (UI)** — messages require explicit interaction to mark
  as read; `thrum inbox --unread` no longer marks messages as read
- **Action directive protocol** — `thrum wait` outputs structured action
  directives instead of raw message content; stop hook uses directives too
- **Hybrid message reliability** — stop hook + listener heartbeat file ensures
  no messages are missed between listener re-arms

### Fixed

- **12 E2E test failures** resolved; `THRUM_HOME` cleared in global-setup for
  test isolation
- **UI identity mismatch** — `for_agent` identity used for `is_read` and
  mark-read; message list query invalidation added to `useMarkAsRead`
- **Listener hardening** — standardized timeout to 8m, widened `--after` window
  from -1s to -15s, fixed heartbeat step skipping on Haiku, prevented listener
  from acting on ACTION REQUIRED directives
- **Daemon shutdown** — force-close active connections on shutdown
- **Preamble** recreated when deleted; DefaultPreamble test assertion updated
- **Inbox unread count** aligned with `for_agent` filter

### Changed

- **README** rewritten to match website voice; SVG architecture diagram added
- **Branding** — removed "git-backed" from identity language; CLI positioned as
  primary, MCP as optional
- **Documentation site** improved for human readers; quickstart simplified;
  install methods consolidated

## [0.5.5] - 2026-03-09

### Improved Agent Safety & Toolkit

Default preamble now warns agents against running `thrum context save` manually
(which destroys accumulated session state). Role templates updated with
learnings from a 31-task multi-agent session: mandatory sub-agent delegation,
CAN/CANNOT scope boundaries, background listener pattern, and `thrum sent`
integration.

### Changed

- **DefaultPreamble** — "Save context" line now directs agents to use
  `/thrum:update-context` skill instead of manual `thrum context save`
- **Role templates (all 8)** — added context save warning, background message
  listener pattern, `thrum sent --unread`, SendMessage tool warning, fixed idle
  behavior to use listener instead of direct `thrum wait`
- **Coordinator templates** — added CAN/CANNOT authority lists, strategy file
  references
- **Implementer templates** — added CAN/CANNOT scope lists, mandatory sub-agent
  delegation, 5-step task protocol (strict variant)
- **Planner/Researcher templates** — added exploration-focused strategy
  references
- **project-setup skill** — now self-contained in plugin with
  `resources/implementation-agent.md` and `resources/philosophy-template.md`;
  added beads prerequisite check with correct install instructions
  (`steveyegge/beads/bd`)
- **Beads setup guide** — rewritten for Dolt backend (v0.59.0+), correct repo
  attribution (steveyegge/beads), dolt prerequisite, sync setup, common errors
- **Beads UI setup guide** — updated for Dolt backend, added worktree support
  and sandbox mode sections
- **Context docs** — added agent safety note to `thrum context save` in CLI and
  context documentation

## [0.5.4] - 2026-03-09

### Sent Command & Durable Deliveries

New `thrum sent` command lets agents review messages they sent and see which
recipients have read them. Message delivery is now durable — every `send`
records recipient snapshots in SQLite, and `mark-read` updates durable read
receipts. The send response now shows exactly who the message was delivered to,
eliminating guesswork about routing.

### Added

- **`thrum sent`** — list messages you sent with recipient read status
- **`thrum sent --unread`** — filter to messages with unread recipients
- **`thrum sent --to @agent`** — filter by recipient or audience
- **`thrum sent show MSG_ID`** — full recipient detail for one message
- **Durable message deliveries** — `message_deliveries` table tracks every
  recipient with `delivered_at` and `read_at` timestamps
- **Send confirmation** — `SendResponse` now includes `audiences` and
  `recipients` fields showing resolved delivery targets
- **`thrum sent --unread`** in DefaultPreamble, strategies, and prime output

### Fixed

- **`thrum wait`** now wakes for direct reply mentions and group messages where
  the agent is a member
- **Wait filters** aligned with inbox delivery rules for consistent behavior
- **Startup script** properly quotes values in `CLAUDE_ENV_FILE` heredoc

## [0.5.3] - 2026-03-06

### Scheduled Backups

The daemon can now run automatic backups on a configurable interval. Configure
via CLI (`thrum backup schedule 24h`) or `.thrum/config.json`. Backups include
all thrum data plus third-party plugins (e.g., Beads) in a single archive with
GFS rotation.

### Pinned Agent Identity for Worktrees

Agents working in worktrees no longer silently drift to the daemon's default
identity. Three new environment variables (`THRUM_HOME`, `THRUM_AGENT_ID`,
`THRUM_NAME`) pin CLI commands to a specific repository and agent, even when the
shell cds into a different worktree. The startup script and daemon both respect
these pins.

### Added

- **Scheduled automatic backups** — `thrum backup schedule [interval|off]` with
  `--dir` flag; daemon runs a `BackupScheduler` goroutine at the configured
  interval
- **Embedded strategy files** — three strategy reference files (sub-agent,
  registration, resume-after-context-loss) embedded in the binary and written to
  `.thrum/strategies/` during `thrum init`
- **Strategy read-directives** in `DefaultPreamble` — agents are pointed to
  `.thrum/strategies/` for operational patterns
- **`CLAUDE_ENV_FILE` integration** — startup script persists `THRUM_HOME`,
  `THRUM_AGENT_ID`, and other env vars into Claude Code's session environment
  for SessionStart hooks
- **Strategies documentation** — new website category with three strategy pages

### Changed

- **Startup script** (`scripts/thrum-startup.sh` and template) now exports
  `THRUM_HOME`, `THRUM_AGENT_ID`, and binds all thrum commands to the home repo
  via `--repo "$THRUM_HOME"`
- **`runDaemon()`** creates identities directory in the local checkout instead
  of the shared redirect target, matching the `IdentitiesDir()` contract
- **`DefaultPreamble`** prioritizes `@name` over `@role` for send instructions,
  preventing accidental group fanout
- **`resolveLocalAgentID()`** checks `THRUM_AGENT_ID` env var before config file
  lookup; errors now surface with a helpful registration hint

### Fixed

- **Variable shadowing in `prime.go`** — `whoami` inside an `if` block was
  shadowed by `:=`, causing `ctx.Session` to never populate; `thrum prime`
  always showed "Session: none"
- **Identity drift in `status`, `overview`, `prime`, and subscriptions** — these
  commands now pass `caller_agent_id` to the daemon instead of relying on
  daemon-side resolution
- **`DefaultSocketPath`** applies `EffectiveRepoPath` before redirect resolution
  so worktree agents connect to the correct daemon
- **`thrum init --force`** now refreshes the preamble, pre-populates identity
  fields, and fixes role conflict on re-init
- **Preamble `--after` flag** corrected from `-30s` to `-1s` (prevents stale
  message replay)

## [0.5.0] - 2026-02-23

### Big Update to the UI

The web dashboard has been rebuilt from scratch as a Slack-style 3-panel
interface. Full documentation:
**[Web UI Guide](https://thrum.team/docs.html#web-ui.html)**

### Added

- **Web UI overhaul** — Slack-style interface with sidebar navigation, Live
  Feed, My Inbox, Group Channels, Agent Inbox, Who Has?, and Settings views
- **Live Feed** with real-time activity stream and three filter modes (All,
  Messages Only, Errors)
- **Group Channels** with member management, create/delete dialogs, and
  channel-scoped messaging
- **Agent Inbox** with context panel showing intent, branch, session info, and
  impersonation view
- **Who Has?** file coordination tool — search which agent is editing a file
- **Settings view** with daemon health, theme toggle (Dark/Light/System),
  keyboard shortcuts, and notification preferences
- **Keyboard shortcuts** — `1`–`5` for views, `Cmd+K` for search, `Esc` to
  dismiss
- **ComposeBar** with `@mention` autocomplete for agents and groups
- **Unread badges** on sidebar groups and agent entries
- **Message deep-linking** from Live Feed to inbox conversations
- **Pagination** in InboxView and GroupChannelView
- **Agent delete dialog** with archive option and type-to-confirm
- **Group delete dialog** with archive option
- **Role-based preamble templates** — auto-applied on agent registration via
  `.thrum/role_templates/`
- **Project setup skill** — converts plan files into beads epics, tasks, and
  worktrees
- **Web UI documentation page** with 7 annotated screenshots

### Added (RPC)

- `message.deleteByAgent` — clean up messages when removing an agent
- `message.deleteByScope` — scoped message deletion
- `message.archive` — export-then-delete for message cleanup
- `group.delete` with `delete_messages` parameter

### Changed

- Dashboard rebuilt as single-page app with hash-based routing
- Sidebar restructured: Live Feed → My Inbox → Groups → Agents → Tools
- Message list uses conversation grouping instead of flat chronological display

### Fixed

- Auth guards on all protected views
- Polling interval consistency across components
- Pagination race conditions in inbox and channel views
- Agent name tooltips for truncated names
- Dead code and unused hooks removed
- Identity fallback for "Unknown" inbox heading
- Port file cleanup on daemon shutdown
- Group member validation on add
- Startup identity persistence

## [0.4.5] - 2026-02-21

### Added

- **Agent context management**: Per-agent context storage with CLI detection.
  `thrum context save/show` persists session state across compaction.
- **`thrum init` full setup**: Single command does prompt, daemon start,
  register, session creation, and intent setting.
- **Identity v3 enrichment**: Identity files now carry branch, intent, and
  session_id. `quickstart` populates these fields automatically.
- **AgentSummary canonical output model**: Unified JSON/human output for agent
  status across `whoami`, `status`, and `overview` commands.
- **safedb package**: Compile-time context enforcement for all SQLite operations
  with connection limits and WAL sync mode.
- **safecmd package**: Context-aware git commands with 5s/10s timeouts replacing
  raw `exec.Command` calls.
- **Resilience test suite**: 32 tests covering RPC, CLI, concurrency, crash
  recovery, and multi-daemon scenarios.
- **`setup claude-md` subcommands**: Generate CLAUDE.md files from Go templates.
- **Pre-compact hook**: Identity-scoped backups via `${CLAUDE_PLUGIN_ROOT}`
  script bundled in plugin.

### Changed

- **Name-only routing**: Messages route by agent name and group membership only.
  Role strings are no longer used for direct inbox matching. Role-based
  addressing (`@implementer`) now works through auto-created role groups that
  are visible in `thrum group list` and manageable via `thrum group member`.
- **Agent name ≠ role**: Registration rejects agents whose name matches their
  role (e.g., `name=implementer role=implementer`). Use distinct names like
  `impl_api` or `impl_db`.
- **`thrum wait` always filters by agent identity**: The `--all` flag has been
  removed. Wait now returns only messages addressed to the calling agent (direct
  mentions, group messages, broadcasts).
- **Recipient validation**: Sending to an unknown agent, role, or group now
  returns a hard error listing the unresolvable addresses. The message is not
  stored — fix the address and resend.
- `status` and `overview` use `FormatAgentSummary` for consistent agent output.
- `team` output shows worktree and host as separate fields.
- `agent list` shows branch and intent for offline agents in context view.
- Go 1.26 required (fixes govulncheck panic on json/v2 variadic types).

### Fixed

- MCP `check_messages` now sees name-directed messages, broadcasts, and group
  messages (previously only role-based mentions were matched).
- MCP send and broadcast include `CallerAgentID` so messages are attributed to
  the correct sender in multi-worktree setups.
- MCP mark-read records read-state under the correct agent identity.
- MCP `check_messages` excludes the agent's own sent messages.
- Replies now include the original sender in the audience so they route back
  correctly.
- Reply to group messages uses `@groupname` instead of the malformed
  `@group:groupname` format.
- MCP waiter subscribes to `@everyone` group scope so broadcasts trigger
  WebSocket push notifications.
- `list_agents` shows the agent ID when display name is empty.
- Daemon deadlock prevention with SQLite and socket timeouts.
- SQLite WAL accumulation with connection limit and sync mode.
- Server per-request timeout reduced from 30s to 10s.
- RPC dial timeout added (5s), RPC call timeout reduced to 10s.
- WebSocket handshake timeout added (10s) for MCP waiter.
- Sync notify goroutines capped to 10 with semaphore.
- Context propagation through pairing wait path.
- All git commands migrated to safecmd with enforced timeouts.
- All DB call sites migrated to context-aware safedb.
- Lock scope reduced in 5 high-risk RPC handlers.

## [0.4.4] - 2026-02-18

### Added

- `thrum init --stealth`: writes exclusions to `.git/info/exclude` instead of
  `.gitignore`, leaving zero footprint in tracked files.
- `--limit N` alias for `--page-size N` on `thrum inbox`.
- `--everyone` flag on `thrum send` (alias for `--to @everyone`).
- Plugin ships `agents/message-listener.md` for auto-discovery by Claude Code.
- `make deploy-remote REMOTE=host` for scp + codesign to remote macOS machines.

### Changed

- `thrum init` defaults to `local_only: true` — remote git sync requires
  explicit opt-in via `local_only: false` in config.
- `thrum prime` listener instruction upgraded from soft tip to
  `⚠ ACTION REQUIRED:` directive.

### Fixed

- `--broadcast` is now a proper alias for `--to @everyone` (not deprecated).
- Plugin install docs corrected to two-step marketplace flow.
- `thrum setup claude-md` added to README Essential Commands table.
- Defensive test for duplicate thrum section headers in CLAUDE.md.
- Clarifying comment on separator edge case in `replaceThrumSection()`.

## [0.4.3] - 2026-02-17

### Changed

- Init is local-only by default — remote git sync must be explicitly enabled via
  `local_only: false` in `.thrum/config.json`

### Fixed

- Internal git commits in the a-sync worktree now skip pre-commit hooks
  (`--no-verify`) to avoid failures from project-level hooks
- Daemon, CLI client, and MCP server can no longer hang indefinitely. All I/O
  paths now enforce timeouts: 5s CLI dial, 10s RPC calls, 10s server
  per-request, 10s WebSocket handshake, 5s/10s git commands, and context-scoped
  SQLite queries. Lock scopes reduced in high-risk handlers so no mutex is held
  during file I/O, git operations, or WebSocket dispatch.
- Subscription cleanup on session end — orphaned subscriptions from crashed
  clients are now deleted when `session.end` fires (thrum-620c)
- Subscription commands (`thrum subscriptions`) now resolve caller identity
  correctly by passing `caller_agent_id` through the RPC layer (thrum-efjv)
- Context propagation in subscription handlers changed from
  `context.Background()` to request context for proper cancellation
- Template rendering test expectations aligned with identity-reuse design

### Added

- Crash recovery tests: kill-during-write, DB integrity after abrupt shutdown,
  daemon restart, projection rebuild from JSONL
- Negative path tests: send to non-existent agent, malformed JSON-RPC requests
  (6 cases), connection drops mid-request, mixed valid/invalid concurrent load
- Timeout enforcement tests verifying 10s per-request handler deadline and
  concurrent request isolation
- Goroutine leak detection helper (`checkGoroutineLeaks`) added to concurrent
  resilience tests
- E2E agent-cleanup tests: agent delete removes all artifacts (identity,
  messages, events), delete non-existent agent returns error, cleanup emits
  event in events.jsonl, `--force` and `--dry-run` mutual exclusion (thrum-6xjs,
  thrum-mfiv, thrum-x29q, thrum-i2fe)
- E2E init tests updated to match sharded file layout (`events.jsonl` +
  `messages/` directory) (thrum-lwls, thrum-xlig)
- `thrum setup claude-md` command generates recommended CLAUDE.md content for
  thrum-enabled repos. Prints to stdout by default; `--apply` appends to
  existing CLAUDE.md with duplicate detection; `--force` replaces existing
  section (thrum-rimg)
- `thrum setup` restructured with `worktree` and `claude-md` subcommands
  (backwards compatible — bare `thrum setup` still works)
- `thrum prime` shows a tip to run `thrum setup claude-md --apply` when
  CLAUDE.md lacks a Thrum section

### Changed (Infrastructure)

- Resilience test infrastructure refactored: shared fixture extraction via
  `TestMain` (extracts once, copies per-test), atomic JSON-RPC request IDs to
  prevent race conditions
- Race detection (`-race`) enabled in resilience test script
- CLI roundtrip test helper `runThrum` now enforces 30s context timeout to
  prevent hangs
- Benchmark `BenchmarkConcurrentSend10` protected with `select`-based deadlock
  timeout

## [0.4.2] - 2026-02-14

### Added

- Apple Developer ID codesigning and notarization for macOS release binaries
- CI/CD signs darwin binaries during GoReleaser build and submits to Apple for
  Gatekeeper approval

## [0.4.1] - 2026-02-14

### Fixed

- Context prime identity resolution in worktrees and unread hint
- 6 bugs closed (thrum-pwaa, thrum-16lv, thrum-pgoc, thrum-5611, thrum-en2c,
  thrum-8ws1): daemon accept loop race condition, gofmt formatting,
  golangci-lint errors, macOS quarantine attribute in install script

### Changed

- Documentation audit: updated 9 files across website docs, llms.txt, and plugin
  files to reflect v0.4.1 changes

## [0.4.0] - 2026-02-13

### Added

#### Agent Groups

Named groups for organizing agents and targeting messages. Groups are flat
collections of agents and roles.

- `thrum group create|delete|add|remove|list|info|members` CLI commands
- Auto-detection of member type (`@alice` = agent, `--role` = role)
- `@everyone` built-in group auto-created on daemon startup
- Group-scoped messaging via `thrum send --to @groupname`
- 6 new MCP tools: `create_group`, `delete_group`, `add_group_member`,
  `remove_group_member`, `list_groups`, `get_group`
- `get_group` supports `expand=true` to resolve roles to agent IDs

#### Reply-to Messages

Simple message threading via parent references, replacing the thread system.

- `thrum reply MSG_ID` creates a `reply_to` reference on the new message
- Replies copy audience (mentions) from parent message
- Inbox clusters replies under parent messages with `↳` prefix
- `send_message` MCP tool accepts optional `reply_to` parameter

#### Tailscale Peer Sync

Cross-machine event synchronization over Tailscale's encrypted mesh network.

- Human-mediated pairing: `thrum peer add` generates 4-digit code,
  `thrum peer join <address>` connects
- 3-layer security: Tailscale WireGuard encryption + pairing code + per-peer
  token auth
- Event-sourced sync with sequence-based checkpoints and deduplication
- Periodic sync scheduler with configurable intervals
- 5 CLI commands: `thrum peer add|join|list|remove|status`
- Peer registry with persistence to `.thrum/var/peers.json`
- Supports both Tailscale SaaS and self-hosted Headscale control planes

#### Runtime Preset Registry

Multi-runtime support for AI coding agents with auto-detection and config
generation.

- Auto-detection for 6 platforms: Claude Code, Codex, Cursor, Gemini, Auggie,
  CLI-only
- `thrum runtime list|show|set-default` CLI commands
- `thrum init --runtime <name>` generates runtime-specific config files (MCP
  settings, hooks, instructions)
- Embedded templates for each runtime with shared startup script
- File marker detection (`.claude/settings.json`, `.codex`, `.cursorrules`,
  `.augment`) with env var fallback

#### Configuration Consolidation

`.thrum/config.json` as single source of truth for all settings.

- `thrum config show` displays effective configuration resolved from all sources
  with provenance (config.json, env, default, auto-detected). Supports `--json`.
- `thrum init` interactively prompts for runtime selection (non-interactive
  fallback for CI)
- Daemon reads `sync_interval` and `ws_port` from config.json
- `ws_port: "auto"` finds a free port dynamically
- Priority chain: CLI flags > env vars > config.json > defaults

#### Team Command

Rich per-agent status for all active agents.

- `thrum team` shows session, git branch, intent, inbox counts, and per-file
  change details with diff stats for every active agent
- Per-agent inbox shows directed messages; shared messages in footer section
- `--all` flag includes offline agents, `--json` for machine-readable output
- `THRUM_HOSTNAME` env var for friendly machine names
- Hostname tracking on agent registration (schema v11)

#### Context Prime

- `thrum context prime` gathers identity, session, agents, inbox, and git work
  context in a single command for agent initialization
- Graceful degradation when daemon, session, or git are unavailable
- Both human-readable and `--json` output

#### Enhanced Quickstart & Worktree Bootstrap

- `thrum quickstart` gains `--runtime`, `--dry-run`, `--no-init`, `--force`,
  `--preamble-file` flags
- Auto-detects runtime and generates config files during quickstart
- Auto-creates context file and default preamble on first registration
- `scripts/setup-worktree-thrum.sh` enhanced with `--identity`, `--role`,
  `--module`, `--preamble`, `--base` flags for single-command worktree bootstrap

#### Additional Commands

- `thrum whoami` — display current agent identity without daemon connection
- `thrum version` — version info with hyperlinks to repo and docs

### Changed

- **`thrum send --broadcast`** deprecated, maps to `--to @everyone` with notice
- **`broadcast_message` MCP tool** simplified to send via `@everyone` group
- **`thrum status`/`overview`** scope inbox counts to agent's actual messages
  and resolve local worktree identity correctly
- **`thrum who-has`** shows detailed file change info (+additions/-deletions,
  status, time ago)
- **Website** — added light/dark theme toggle with full light-mode CSS

### Removed

- **Thread system** — `thrum thread create|list|show` commands, `thread.create`,
  `thread.list`, `thread.get` RPC handlers, and `threads` table all removed.
  Replaced by reply-to references and groups.

### Infrastructure

- **Schema**: 5 migrations (v7→v12) — added `groups`, `group_members`, `events`,
  `sync_checkpoints` tables; added `file_changes` and `hostname` columns;
  dropped `threads` table
- **Dependencies**: Tailscale SDK v1.94.1, `golang.org/x/term` v0.38.0
- **Tests**: +39 test files (~6,000 lines added, ~1,200 removed)
- **New packages**: `internal/groups`, `internal/runtime`,
  `internal/daemon/checkpoint`, `internal/daemon/eventlog`

### Documentation

- 5 new guides: multi-agent coordination, Tailscale sync, Tailscale security,
  configuration, and design philosophy
- CLI progressive disclosure: 4-tier command organization (daily drivers,
  agent-oriented, setup/admin, aliases)
- All thread and nested-group references removed across 18+ docs
- Toolkit templates restructured into `agent-dev-workflow/` directory

## [0.3.1] - 2026-02-11

### Added

#### Context Preamble

Per-agent preamble support — a stable, user-editable header prepended when
showing context. Preambles persist across `thrum context save` operations,
acting as a persistent system prompt that survives session resets.

- `thrum context show` gains `--raw` and `--no-preamble` flags
- `thrum context load` alias for `thrum context show`
- New `thrum context preamble` subcommand with `--init` and `--file` flags
- Default preamble with thrum quick reference auto-created on first context save
- `context.preamble.show` and `context.preamble.save` RPC methods
- `thrum agent delete` now cleans up preamble files

#### Test Suite Quality Audit

Comprehensive cleanup of 140 test quality issues across Go, UI, and E2E suites.
All changes are test-only — zero production code modified.

- Replaced `time.Sleep` calls with proper synchronization (ready channels,
  socket polling)
- Fixed broken tests: signal handling, type assertions, error handling,
  incomplete stubs
- Strengthened UI tests: missing assertions, test spy conflicts, TypeScript
  errors, un-skipped disabled tests
- Replaced hardcoded sleeps with polling-based waits in E2E specs

## [0.3.0] - 2026-02-11

### Added

#### Agent Context Management

Per-agent context storage for persisting volatile project state across sessions.
Agents can save, view, clear, and sync markdown context files tied to their
identity.

- `thrum context save|show|clear|sync|update` CLI commands
- `context.save`, `context.show`, `context.clear` RPC methods
- `thrum status` shows context file size and age when context exists
- `thrum agent delete` removes agent context files
- `/update-context` Claude Code skill for guided context saving
- Context sync to a-sync branch for cross-machine sharing

### Fixed

- `thrum wait` no longer calls vestigial subscribe/unsubscribe RPCs that caused
  identity resolution failures in multi-agent worktrees. Subscription calls
  removed; `mention_role` filtering moved into the message list poll where it
  takes effect.

## [0.2.0] - 2026-02-10

### Added

#### Identity Resolution & Wait Command

Complete overhaul of agent identity resolution for multi-worktree repositories,
plus efficient blocking-based message listening.

- **Most-recent-wins auto-selection**: When multiple identity files exist for a
  worktree, automatically selects the one with the latest timestamp. Eliminates
  "cannot auto-select identity" errors.
- **Worktree identity guard**: Running from a worktree with no registered
  identities now returns a clear error instead of falling through to the main
  repo's identities.
- `thrum whoami` command displays current agent identity without daemon
  connection (lightweight alternative to `thrum status`)
- `thrum wait --all` subscribes to all messages (broadcasts + directed)
- `thrum wait --after` filters by relative time (e.g., `--after -30s`)
- Message-listener agent rewrite: replaced polling with blocking wait, reducing
  API calls from 120 to 12 for ~30 minute coverage

### Changed

- `resolveLocalAgentID` now returns errors; all 17 CLI call sites fail early
  with a "register first" message
- Inbox auto-filters by worktree identity, preventing message leakage across
  worktrees

### Fixed

- Formatting alignment in wait.go

## [0.1.0] - Earlier Development

### Added

#### Core Infrastructure

- Event-sourced messaging with JSONL append-only log and SQLite projection
- Message scopes, references (tags, mentions), threads, and edit history
- Agent registration, identity management, and session lifecycle
- Unix socket JSON-RPC server with handler registry and batch support
- Git-based message synchronization with conflict resolution
- Event subscription system with filtering and notification dispatch
- Test suite with >70% coverage
