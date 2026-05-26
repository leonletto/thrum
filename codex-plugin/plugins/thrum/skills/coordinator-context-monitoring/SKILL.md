---
name: coordinator-context-monitoring
description: "Use when managing live implementer/brainstormer agents during a long coordination session, at epic merge gates, after a busy dispatch hour, or whenever you suspect an agent is approaching context limits. Prevents 97%-context silent blow-ups by running a sweep + pre-emptive restart before the agent degrades. Safe to wire into a recurring cron that INVOKES this skill — the skill applies tier-ladder judgment, only firing autonomous restarts at the >85% tier. What's forbidden is a cron/script that fires restarts unconditionally without going through this skill's tier ladder."
# source: claude-plugin/skills/coordinator-context-monitoring/SKILL.md
# generated-by: scripts/sync-skills.sh
---


## Coordinator: Context Monitoring and Pre-emptive Restart

### When to invoke

Trigger this pattern at each of:

- Receipt of an `ALERT: flagged=…` message from the `context-monitoring`
  thrum monitor (the canonical scheduled sweep, fires every ~20 min)
- Epic merge gates (after merging a sub-epic — E6.4, E6.5, etc.)
- After dispatching 3+ tasks in quick succession
- After any agent has been running for 60+ minutes without a restart
- When you observe slow or degraded responses from an implementer
- Manually whenever the session feels "intense" (lots of cycles in a short
  window)

The skill applies tier-ladder judgment, so the >85% autonomous-restart tier
fires conditionally on actual ctx %, not on every sweep. What's forbidden is
a script that bypasses this skill and fires `thrum tmux restart --force`
unconditionally (that violates `feedback_restart_discipline` — burn the
runway, don't restart on schedule).

### How the scheduled sweep works (v0.10.6+ — thrum monitor)

The sweep runs as a daemon-managed `thrum monitor` job named
`context-monitoring`, registered with a 5-field cron schedule
(e.g. `"7,27,47 * * * *"` — every 20 min at :07/:27/:47). The job invokes
`scripts/error-and-context-agent-sweep.sh --no-nudge --out
/tmp/agent-sweep.txt`. The script emits a single consolidated `ALERT:` line
to stdout when ANY agent crosses a threshold (ctx >= 50% OR api-error OR
capture-fail); when the fleet is clean, the script is silent so no message
fires. The monitor's `--match '^ALERT:'` filter routes the ALERT line as a
message to `@coordinator_main`, which triggers this skill.

Format of the ALERT line:

```text
ALERT: flagged=N stuck=S tier3=T tier2=U — agent_a(92%,api-err,STUCK); agent_b(88%); …
```

- `flagged` — total agents needing attention
- `stuck` — api-errored on TWO consecutive sweeps (state file tracks this
  across runs)
- `tier3` — count with ctx >= 85% (force-restart candidates)
- `tier2` — count with 70-84% ctx (tmux-send nudge candidates)
- Per-agent segment: `name(ctx%,reason-if-any,classifier)` joined by `;`

The full per-agent report stays at `/tmp/agent-sweep.txt` (overwritten each
sweep) for on-demand drill-down — read it AFTER receiving an ALERT to see
which specific panes are at risk.

The previous keepalive-cron pattern (CronCreate `5fdb627b`) is deprecated
in favor of this scheduled monitor. The bookkeeping responsibility moves
out of the coordinator's per-session re-add chore and into the daemon's
durable monitors table (survives daemon restart, no per-session re-init
needed for this monitor — though OTHER CronCreate jobs may still require it
per `feedback_cron_reinit_each_session`).

### Step 1 — Run the sweep

```bash
bash scripts/error-and-context-agent-sweep.sh --out /tmp/agent-sweep.txt
grep -E 'ctx_used:|^===== ' /tmp/agent-sweep.txt
```

The script emits one `ctx_used: X.X%` line per live agent. It captures the
Claude Code status bar footer, normalizing UTF-8 non-breaking spaces
(`\xc2\xa0`) before matching `Ctx Used: X.X%`. Runtimes without that footer
(Codex, Cursor) fall back to `(n/a)`.

### Step 2 — Threshold logic

| ctx_used  | Action                                                                         |
| --------- | ------------------------------------------------------------------------------ |
| < 50%     | No action — agent has runway                                                   |
| 50% – 70% | Directed inbox restart request (polite, agent writes snapshot)                 |
| 70% – 85% | Tmux-send nudge directly into their pane (bypasses inbox; more forceful)       |
| > 85%     | Force-restart immediately without waiting for response                         |
| `(n/a)`   | Pane capture failed OR runtime has no Ctx footer — check tmux session manually |

**Reliability ladder rationale:** the inbox → tmux-send → force-restart
progression goes from polite to forceful. Inbox messages can fail (delivery
bugs, agent too degraded to check inbox at high ctx, or the documented self-echo
regression). Tmux-send types literally into the agent's input field — bypasses
inbox entirely. Force-restart bypasses the agent altogether.

The thresholds reflect the pattern session-2-ago coordinator established: agents
degrade _silently_ before they blow, and 50% is the "still coherent enough to
write a good snapshot" window.

### Step 3 — Directed inbox restart request for 50% – 70% agents

```bash
thrum send 'Your context is at ~X%. Please run $thrum-restart now — write your snapshot and I will re-dispatch after.' --to @<agent_name>
```

The agent writes their restart snapshot to `.thrum/restart/<agent>.md` and goes
idle. Coordinator waits for the "snapshot written" acknowledgement before
re-dispatching the next task.

If the agent acks without writing the snapshot first (anti-pattern), gently
remind them to write the snapshot before going idle so the next session restores
cleanly.

If the agent doesn't respond within ~5 minutes OR you see them keep working,
escalate to Step 4 (tmux-send nudge).

### Step 4 — Tmux-send nudge for 70% – 85% agents

The inbox path may not be reaching them at this context level. Bypass the inbox
by typing the restart command directly into their input field:

```bash
thrum tmux send <tmux_session_name> '$thrum-restart'
```

(Find the tmux session name in sweep output — it's typically the worktree
basename, e.g. `b-b1-impl`, NOT the agent_id.)

This causes the runtime to immediately execute `$thrum-restart` as if the user
typed it. The agent writes their snapshot + restarts.

If the tmux-send doesn't trigger a restart within ~5 minutes (agent may be too
degraded to process input), escalate to Step 5 (force-restart).

### Step 5 — Force restart for >85% agents (autonomous)

```bash
thrum tmux restart <agent_name> --force
```

**Execute autonomously — do NOT surface to the operator first** (rule confirmed
2026-05-18 Session 73 per [[feedback-autonomous-force-restart]]: the previous
"surface first, restart on authorization" policy resulted in an agent stuck at
97% ctx because the surface message scrolled past during a busy coordination
window; the autonomous restart would have caught it sooner. Restart is
non-destructive — snapshot is preserved + worktree state survives — so the cost
of a false-positive restart is far lower than the cost of a missed catch).

Do NOT wait for the agent to respond. The `--force` flag sends the restart
signal even if the agent is mid-tool-call. After the new session starts, it
auto-primes from the restart snapshot (if one was written) or from `bd prime`

- `thrum prime`.

Surface to the operator AFTER the restart in a brief status note ("Force-
restarted @<agent_name> at <ctx>% — snapshot at .thrum/restart/<agent>.md
preserved"). The notify is information, not authorization.

After force-restart, re-send the agent's current dispatch as if it were a fresh
dispatch — their previous in-flight work may need to resume from scratch (any
WIP files in their worktree are theirs to audit salvage-vs-discard).

### Step 6 — API-error auto-nudge (handled by the sweep script)

The sweep script now auto-nudges every agent whose pane shows an `API Error`
line. The script types `continue` into the affected pane via `tmux send-keys`
(bypassing the `thrum tmux send` wrapper queue, which stalls on fully-silent
panes per `thrum-7yhs`). You do not need to fire these nudges yourself.

The sweep report's header lists every agent that was auto-nudged in the current
run, e.g.:

```text
# auto-nudged 3 agent(s) on api_errors with 'continue':
#   - impl_foo @ foo-impl:0.0
#   - impl_bar @ bar-impl:0.0
#   ...
```

Anthropic 529s and rate limits are transient (typically resolve in
seconds-to-minutes); the agent's previous tool call is queued in-session, so a
single `continue` reactivates them without losing in-flight state. Sweeps fire
every ~20 min, so a single rate-limit episode rarely spans more than one sweep —
auto-nudge converges naturally.

**When to escalate:** if the same agent appears in `auto-nudged` lines on TWO
consecutive sweeps despite the nudge, the issue isn't transient — surface to
operator as SUSPECTED-STUCK and investigate manually (status.claude.com,
network, account limits).

**When NOT to auto-nudge:** if you're about to ship a release and you'd prefer
the agent's stuck-state held to fold one more fix into the current cycle,
surface to operator BEFORE the next sweep so the auto-nudge can be held. Once
`continue` fires, the agent resumes its previous tool call immediately — there's
no recovery window.

### Pre-restart safety checks

Whether triggered by the scheduled `context-monitoring` thrum monitor or by
the coordinator manually invoking the skill, run these guards BEFORE firing
a restart:

1. **Verify the daemon is reachable**:
   `thrum team --json | jq '.members | length'` — if 0 or error, daemon is down;
   skip the sweep, surface to operator.
2. **Confirm the monitor is alive**: `thrum monitor list` should show
   `context-monitoring` in `running` status with a non-empty `SCHEDULE`
   column. If absent or dead, the scheduled ALERTs aren't firing and the
   skill must be invoked manually from a recurring cron until the monitor
   is restored. (See "Re-register the monitor" below.)
3. **Check if any agent is mid-commit** (active tool call): look for
   `Running bash` or active spinner in the sweep pane lines — if so, defer
   restart for that agent until the tool completes.
4. **Never force-restart an agent whose pane shows a Git merge conflict or
   active rebase** — that corrupts the worktree. Surface to operator instead.
5. **Cooldown**: do not restart the same agent twice within 30 minutes. If an
   agent crosses threshold again that fast, something's wrong with their
   workload — surface to operator rather than restart-loop.

#### Re-register the monitor

If `thrum monitor list` doesn't show `context-monitoring`, register it from
the main repo:

```bash
thrum monitor add \
  --name context-monitoring \
  --schedule "7,27,47 * * * *" \
  --match '^ALERT:' \
  --to @coordinator_main \
  -- bash /Users/leon/dev/opensource/thrum/scripts/error-and-context-agent-sweep.sh --no-nudge --out /tmp/agent-sweep.txt
```

Adjust the absolute path to the script for your checkout. The monitor will
fire one-shot per scheduled tick; in between ticks the child does not run,
so there's no continuous CPU cost from the sweep.

### What to do post-restart

When a restart fires (Step 4 or 5):

1. Wait for the agent to come back online (`thrum team` shows them active again,
   or their pane shows the runtime prompt).
2. Re-send their current dispatch with the full scope + plan refs + AC targets —
   treat them as a fresh implementer who needs the full briefing again.
3. Note any WIP files they may have left in their worktree from the prior
   attempt (they'll audit salvage-vs-discard before substantive code).
4. If they had a partial DONE, the coordinator's git log should still have it;
   the agent may need a pointer to commits they shipped before the blow.

### Reference

- **Sweep script**: `scripts/error-and-context-agent-sweep.sh` (captures
  `ctx_used: X.X%` from Claude JSONL transcript; falls back to pane scan for
  non-Claude runtimes). Renamed from `tmux-agent-sweep.sh` 2026-05-20 per
  thrum-e1n0 — now part of a sweep-script family (sibling:
  `waiting-on-coord-agent-sweep.sh`).
- **Pattern source**: Session 70 (`2026-05-17T14:40-19:00Z`) coordinator
  established the broadcast-at-50% + force-restart-at-85% threshold pattern.
- **Memory key**: project-local `coordinator-rule-context-check-broadcast` may
  capture project-specific tweaks; load via `bd memories coordinator-rule-`.
- **Related discipline**:
  - `feedback_restart_discipline` — burn the runway; don't preempt-restart at
    clean checkpoints
  - `feedback_byte_equality_pane_detection` — pane-snapshot byte diffs are
    unreliable; use structural anchors + settle windows
- **Why thresholds matter**: agents at 97% context silently produce degraded
  output (missed instructions, partial tool calls, slow responses) before they
  blow. The 70%/85% thresholds give the system 15-30% runway to extract a
  snapshot or force-restart cleanly.
