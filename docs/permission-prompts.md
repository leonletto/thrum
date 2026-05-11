## What This Does

Your agent hits a permission prompt in the middle of a long run. You're not
watching the pane. The agent sits there silently — blocked — until you notice.
Depending on when you check, that could be minutes or hours of wasted time.

Thrum detects this. The daemon watches every enrolled tmux session, notices when
the pane content stops changing in a way that looks like a stuck prompt, and
sends you an actionable message: here's what the agent is waiting on, here's the
session, here are the commands to approve or deny. You reply `y` or `n` from
wherever you are — CLI, web UI, or Telegram on your phone — and Thrum replays
the correct keystroke directly into the pane.

---

## How Detection Works

### Why we replaced the alert-silence hook

The first version of this feature used tmux's `alert-silence` hook as the
trigger. That turned out to be the wrong approach. tmux documents this in issue
1384: alerts are processed per-session-per-client, and detached sessions
(sessions with no currently attached client) typically don't fire the hook at
all. Thrum agents run detached by design. So `alert-silence` was unreliable
exactly when you most needed it.

We replaced it with a daemon-side poller that doesn't depend on tmux hooks.

### The silence-hash poller

The daemon runs a `SessionPoller` (`internal/daemon/permission/poller.go`) that
polls every enrolled session every 10 seconds. Each poll captures the pane
content, strips runtime-specific volatile lines (more on that below), and hashes
the result with SHA-256. Two consecutive polls with the same hash — stable for
~20 seconds — trigger `HandleCheckPane`, which runs the runtime-specific pattern
library against the raw pane content and, on a match, calls `OnDetection` to
start the nudge flow.

Pattern matching and the stability hash both run against the **bottom 15 lines**
of the capture — not the whole 30-line window. An active prompt sits at the
bottom of the terminal by definition; a prompt still visible in the upper
scrollback has either been resolved or is being pushed out by newer output, and
either way isn't actionable. Scoping detection to the bottom of the pane stops
the scheduler from re-firing `firstDetect` every ~60s on resolved prompts that
haven't yet scrolled off the capture window.

Detection latency is about 20 seconds from when the prompt appears to when the
first nudge goes out.

Sessions are enrolled automatically when the daemon launches or restarts a
session. `ReconcilePoller` runs at daemon boot and re-enrolls every active
session, so sessions that were tracked before a daemon restart resume polling
without re-registration.

### Restart safety

The `permission_nudges` SQLite table (schema v21) persists in-flight nudges
across daemon restarts. `ReloadOnBoot` reads all non-expired rows at startup and
resumes the reminder cadence at the right step. If there are any, you'll see
this in the daemon log:

```text
permission found N pending nudge(s) still in flight
```

The Telegram message-ID mapping is also persisted — in `telegram_msg_map`
(schema v24) — so a supervisor's reply to a nudge that was sent before the
daemon restarted still routes correctly. Before this, the mapping lived in an
in-memory LRU cache and a restart between sending the nudge and receiving the
reply silently broke the routing.

---

## Volatile-Line Catalog

Before hashing, the poller strips runtime-specific lines that change every few
seconds for cosmetic reasons — spinners, timers, statuslines. Without stripping,
the hash never stabilizes on an unchanged pane, so the poller never fires.

| Runtime  | Stripped lines                                        | Why                                                 |
| -------- | ----------------------------------------------------- | --------------------------------------------------- |
| `codex`  | `• Working (Ns • esc to interrupt)` progress line     | Per-second activity timer                           |
| `claude` | Cogitation spinner lines (`✻ Cogitated for Ns`, etc.) | Animated spinner                                    |
| `claude` | ccstatusline `Ctx: ... \| Block: ...` markers         | Context size and block countdown drift every 30–60s |

The ccstatusline strip was added after observing three spurious nudges in ~80
seconds for a single unchanged Claude Code prompt. The Ctx counter and Block
countdown were resetting the hash on every drift.

Unknown runtimes pass through unstripped. That's the conservative default:
better to have the poller not fire (false-not-stable) than to fire on a pane
that's still actively running (false-stable).

To add stripping for a new runtime, add patterns to
`internal/daemon/permission/poller.go` in the `volatileLinePatterns` map. The
key is the canonical runtime name from the agent identity file.

---

## Supported Runtimes

The pattern library covers six runtimes. Each has one or more patterns with an
`approve_key` and an optional `deny_key`:

| Runtime    | Pattern                                          | Approve key                      | Deny key (runtime default) |
| ---------- | ------------------------------------------------ | -------------------------------- | -------------------------- |
| `claude`   | `Do you want to proceed?` tool confirmation      | `1` (Yes, once)                  | per-shape — see below      |
| `codex`    | `Would you like to run the following command?`   | `1` (Yes, proceed)               | `3` (No)                   |
| `cursor`   | `Not in allowlist:`                              | `y` (Run once)                   | `Escape`                   |
| `opencode` | `△ Permission required`                          | `Enter` (Allow once, default)    | `End,Enter` (Reject)       |
| `kiro-cli` | `shell requires approval`                        | `Enter` (Yes, single permission) | `Escape`                   |
| `auggie`   | `Always index this workspace` indexing consent   | `3` (Session-only)               | `Escape`                   |
| `auggie`   | `\| Tool Approval Required \|` per-tool approval | `A` (Allow)                      | `D` (Deny)                 |

**Per-shape deny key for claude.** The `claude.tool_confirmation` pattern
matches three observable prompt shapes under one regex anchor, and each shape
has a different "No" key. When the scheduler fires a nudge for a claude prompt,
`DisambiguateClaudeDeny` inspects the captured pane (ANSI-stripped) and picks
the right deny key:

| Shape                                                 | Example                                               | Deny key stored on the nudge |
| ----------------------------------------------------- | ----------------------------------------------------- | ---------------------------- |
| Variant A — 3-option Write/Exec                       | `1. Yes` / `2. Yes, and don't ask again` / `3. No, …` | `3`                          |
| Variant B-Bash — 2-option Bash picker                 | `1. Yes` / `2. No, …`                                 | `2`                          |
| Variant B-Read — 2-option Read picker (or unknown 2-) | `1. Yes` / `2. Yes, and don't ask again for session`  | `Escape`                     |
| No 1/2/3 lines detected at all                        | —                                                     | `Escape`                     |

The Variant B-Read case falls back to `Escape` on purpose: option 2 in that
shape is a session-scoped "don't ask again" — sending `"2"` as a deny would
grant a forever-allow. Without positive evidence that option 2 says "No", the
safe default is to cancel the dialog. The disambiguated key is written onto
`NudgeRow.DenyKey` at first detection so every reminder in the thread carries
the same keystroke without re-inspecting the pane.

The CI guard `TestApproveKeyNeverForeverAllow` enforces a safety invariant: no
runtime's `approve_key` can ever map to a "don't ask again", "add to allowlist",
or "auto-run everything" option. A supervisor's approval must always grant
single-invocation permission only. For auggie specifically, `approve_key = "3"`
because the default-highlighted option on the indexing consent prompt is
`[1] "Always index this workspace"` — a forever-allow that writes to
`~/.augment/settings.json`. Sending `Enter` there would be a safety bug.

---

## Configuration

Two new keys in `.thrum/config.json` control the feature:

```json
{
  "project_name": "thrum",
  "permission_supervisors": ["coordinator", "@leon-letto"]
}
```

**`project_name`** — used to name the `@supervisor_<project>` pseudo-agent that
authors nudges. Defaults to `filepath.Base` of the repo root if unset.

**`permission_supervisors`** — list of recipients for nudges. Accepts:

- A role name (`"coordinator"`) — broadcasts to all active agents with that role
- An agent name (`"@implementer_api"`) — delivers to a specific agent
- A user name (`"@leon-letto"`) — auto-forwards to Telegram if the bridge is
  configured for that user

Defaults to `["coordinator"]` when the key is absent.

The config is re-read on every nudge, so you can change the supervisor list
without a daemon restart.

At daemon boot, Thrum registers `@supervisor_<project>` as a reserved
pseudo-agent. It's the canonical author of all permission nudges. Unlike
`@system`, it accepts replies — that's the entire point. You can see it with:

```bash
thrum team --system
```

Reserved agents show with the `⊙` glyph in compact output. Regular agents use
`●` (active) or `○` (offline).

---

## What a Nudge Looks Like

The first time a prompt is detected, you receive a message like this:

```text
⚠ Permission prompt — @implementer_api (thrum-main)

Repo:    thrum
Runtime: claude
Pattern: claude.tool_confirmation
First detected: 2026-04-16 14:32:11 (0s ago)
Reminder #1 of 6

Pane tail (last 15 lines):
  ...
  ╭──────────────────────────────────────────╮
  │ Do you want to proceed?                  │
  │                                          │
  │ 1. Yes                                   │
  │ 2. Yes, and don't ask again for ...      │
  │ 3. No, and tell Claude what to ...       │
  ╰──────────────────────────────────────────╯

─────────────────────────
To approve:  thrum tmux send thrum-main "1"
To deny:     thrum tmux send thrum-main "3"

Or reply to this message with `y` / `n` — works from CLI, web UI, and Telegram.
```

Reminder messages have the same body but with `Reminder #N of 6` incremented.

**Reminder cadence** — all offsets are measured from when the prompt was first
detected:

| Reminder | Sent at (from first detection) |
| -------- | ------------------------------ |
| 1        | immediately                    |
| 2        | +5m                            |
| 3        | +15m                           |
| 4        | +45m                           |
| 5        | +2h                            |
| 6        | +4h                            |

After 6 reminders with no supervisor response, the scheduler marks the agent
`stuck` (see below) and stops sending.

---

## Stuck State

When the 6-nudge cadence runs out, the agent's identity file gets
`agent_status: stuck`. This is visible in `thrum team` output and the web UI's
agent list.

Stuck clears automatically. When the poller next sees the pane return to a
healthy state — any state that isn't a recognized permission prompt —
`OnRecovery` runs, deletes the nudge row, and resets `agent_status`.

If you manually resolve the prompt in the pane (by attaching to the session and
pressing a key yourself), the next poll cycle will see the changed pane content
and trigger recovery automatically. You don't need to do anything else.

---

## Replying to a Nudge

### From the CLI

```bash
# Reply to the nudge message directly
thrum reply <msg_id> y

# Or send the keystroke directly into the pane
thrum tmux send <session> "1"
```

Both work. The `thrum reply` path goes through `TryResolve` and fires the
correct runtime-specific keystroke. The `thrum tmux send` path sends a literal
keystroke to the pane and bypasses the nudge system — use it if the pattern
matching got something wrong and you want to type a different key.

### From the web UI

Open the inbox, find the nudge message, and use the reply button. Typing `y` or
`n` in the reply field routes through the same `TryResolve` path.

### From Telegram

Two ways to reply:

**Threaded reply** — tap Reply on the nudge message in your Telegram DM with the
bot, type a token, send it. The bot's `InboundRelay` reads the `ReplyToMsgID`,
looks up the corresponding Thrum message ID, and routes the reply through
`TryResolve`. This is the most explicit path — you're replying to the specific
nudge.

**Fresh DM** — if you dismissed the notification or can't find the thread, send
a new DM to the bot with just the approval token. The bot looks up your
most-recent non-expired pending nudge and routes the reply there. This only
works if the body is an exact match for one of the accepted tokens.

Token sets by path:

| Path                                 | Approve tokens             | Deny tokens            |
| ------------------------------------ | -------------------------- | ---------------------- |
| CLI, web UI, threaded Telegram reply | `y`, `yes`, `approve`, `a` | `n`, `no`, `deny`, `d` |
| Telegram fresh DM                    | `y`, `yes`, `allow`        | `n`, `no`, `deny`      |

The fresh-DM set is intentionally narrower. `approve` and `a` aren't in it —
they're unlikely to be a fresh-DM typo and excluding them keeps the trigger
tight. If you need to send `approve`, use a threaded reply.

Prose messages (`"yeah sure"`, `"y please"`) don't trigger either path — both
require an exact match after lowercasing and trimming whitespace.

You can reply to any message in the reminder thread, not just the first nudge.
The `TryResolve` thread-ID fallback walks reminder messages back to their root
nudge row, so replying to Reminder #4 works the same as replying to Reminder #1.

---

## Surviving Daemon Restart

Two tables keep in-flight approvals alive across a daemon restart:

**`permission_nudges`** (schema v21) — stores the pending nudge state: session,
tmux target, agent name, pattern key, approve/deny keys, detected time, nudge
count, last pane hash, and an 8-hour expiry. `ReloadOnBoot` reads all
non-expired rows at startup and resumes the reminder cadence automatically.

**`telegram_msg_map`** (schema v24) — maps Telegram message IDs to Thrum message
IDs. Without this, a supervisor's reply to a nudge sent before a daemon restart
would arrive as an unrouted DM with no `reply_to` reference, and `TryResolve`
would never fire. The table persists the mapping with SQLite write-through and
falls back to it on a cache miss.

Together, these two tables mean a daemon restart mid-flow is invisible to the
supervisor. They reply `y`, the keystroke fires, the agent unblocks.

---

## Cross-Repo Workflow via Telegram

Here's what the full end-to-end looks like when the supervisor is on their
phone:

1. Agent in repo-A hits a permission prompt.
2. Daemon-A's poller detects stable pane content matching a pattern.
3. Daemon-A sends a nudge to `@supervisor_thrum`, which is configured with
   `@leon-letto` as a recipient.
4. The `wsBroadcaster` fires a `notification.message` WebSocket event.
5. `OutboundRelay` picks it up and forwards it to the Telegram bridge.
6. The supervisor's phone receives a DM from the bot.
7. Supervisor taps Reply, types `y`, sends.
8. `InboundRelay` reads `ReplyToMsgID`, looks up the Thrum message ID in
   `telegram_msg_map`, fires a `message.send` RPC with `reply_to`.
9. `AfterMessageCreate` fires → `TryResolve` → atomic `DELETE ... RETURNING`
   claims the nudge row → `sendKeystroke` fires `"1"` into the pane.
10. Agent proceeds.

This works automatically when the Telegram bridge user is listed in
`permission_supervisors`. No additional routing config is needed.

---

## Observability

**Daemon log traces** — the `firstDetect` path logs at `INFO` level:

```text
[permission] firstDetect  session=thrum-main runtime=claude pattern=tool_confirmation supers_count=2 agent_name=implementer_api
[permission] nudge sent   to=coordinator msg_id=perm_abc123 session=thrum-main
```

On restart with in-flight nudges:

```text
permission found 2 pending nudge(s) still in flight
```

**`[nudge]` dispatch tag** — the nudge fan-out emits structured `slog.Info`
events at four points, tagged `[nudge]`, for routing visibility:

```text
[nudge] dispatch         msg_id=... sender=... recipients=[...] origin_daemon=... session_id=...
[nudge] spool.write      msg_id=... recipient=... target=...
[nudge] spool.skip       msg_id=... recipient=... reason=non_local
[nudge] DispatchTmux     msg_id=... recipient=... target=thrum-main:0.0 session=thrum-main self_echo=false
[nudge] telegram.forward msg_id=... bridge_user=... self_echo_candidate=false
```

`self_echo` is the coordinator's phantom-nudge guardrail — if the dispatch
target resolves to the sender's own pane, the daemon refuses the write and logs
the skip. These events are at `INFO` level and gated behind the daemon's
configured log level.

**Identity guard G4** — permission writes (marking an agent stuck, clearing
stuck) are blocked if the agent process is not running. This prevents the
scheduler from labeling a dead agent's identity file during race conditions
between agent crash and reminder cadence.

**`thrum team --system`** — shows the `@supervisor_<project>` pseudo-agent with
the `⊙` glyph. If it's missing, the daemon didn't register it at boot, which
means permission nudges won't have a sender identity and will fail silently.

---

## Safety Invariant

`TestApproveKeyNeverForeverAllow` is a CI test in
`internal/daemon/permission/patterns_test.go` that iterates every pattern in the
library and asserts that the `ApproveKey` is not any of the known forever-allow
tokens (`2`, `Tab`, `Enter` for auggie's indexing prompt, etc.).

This prevents a class of bug where approving a permission prompt from Thrum
would actually grant the agent permanent or session-wide permission for an
action class — broader than the human consented to. If you're adding a new
runtime pattern and the CI guard rejects your `ApproveKey`, the fix is to change
the key to the single-invocation option, not to relax the test.

---

## Trust-Gate Detection

On first launch, codex and claude display a trust dialog before the normal
session starts. Thrum's permission-prompt detector recognizes these first-launch
trust dialogs as a distinct class. When one is detected, keystroke injection —
banner delivery, prime nudge, watchdog nudge — is skipped so the user can answer
the trust prompt manually without interference. Normal permission-prompt
detection and the supervisor notify flow are unchanged; this only affects the
injection paths that fire during session startup.

---

## See Also

- [tmux Sessions](tmux-sessions.md) — pane lifecycle, session enrollment
- [Multi-Runtime](multi-runtime.md) — supported runtimes and their capabilities
- [Telegram Bridge](telegram-bridge.md) — supervisor reply via Telegram
- [Configuration](configuration.md) — `permission_supervisors` and
  `project_name` schema
