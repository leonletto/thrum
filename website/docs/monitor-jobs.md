---
title: "Monitor Jobs"
description:
  "Watch any process output for pattern matches and deliver results as synthetic
  Thrum messages — no polling, no shell, no manual piping"
category: "orchestration"
order: 3
tags: ["monitor", "jobs", "process", "watch", "regex", "debounce"]
last_updated: "2026-04-11"
---

## What This Is

Monitor jobs let you attach a regex filter to any subprocess and turn matching
output lines into Thrum messages. The daemon runs the process, reads its stdout
and stderr, and sends a message to the agent of your choice whenever a line
matches.

You'd use this for things like:

- Watching a dev server for error lines while an agent is coding
- Alerting an agent when a build finishes or fails
- Routing test failure output to the responsible implementer
- Forwarding log patterns from a long-running background service

The daemon owns the process lifecycle. You start a monitor, it runs until you
stop it, and it survives daemon restarts from its saved spec.

---

## Quick Start

Say you want to alert `@impl_api` whenever your dev server logs an error:

```bash
thrum monitor start \
  --name dev-errors \
  --match "(?i)(error|exception|panic)" \
  --to @impl_api \
  --debounce 60s \
  -- node server.js --watch
```

The daemon spawns `node server.js --watch` directly (no shell). Every line from
stdout or stderr is checked against the regex. When a line matches, the daemon
sends a message to `@impl_api` from sender `@monitor:dev-errors`.

Check it's running:

```bash
thrum monitor list
```

```text
ID    NAME         STATUS   UPTIME    PID     MATCH
m_01  dev-errors   running  4m32s     91234   (?i)(error|exception|panic)
```

Stop it when you're done (use the name or the ID):

```bash
thrum monitor stop dev-errors
```

---

## Commands

### `thrum monitor start`

Start a new monitor job. (`add` is a retained alias.)

```bash
thrum monitor start --name <name> --match <regex> --to @<agent> [options] -- <argv...>
```

Everything after `--` is the command to run. The first token is the executable,
the rest are its arguments. No shell expansion happens — what you write is what
gets passed to `exec`.

**Required flags:**

| Flag              | Description                                                                                             |
| ----------------- | ------------------------------------------------------------------------------------------------------- |
| `--name <name>`   | Short identifier for this monitor. Appears in `@monitor:<name>` sender address.                         |
| `--match <regex>` | Go RE2 regex. Lines matching this get delivered. Case-sensitive by default; use `(?i)` for insensitive. |
| `--to @<agent>`   | Recipient agent name (without `@` is also accepted).                                                    |

**Optional flags:**

| Flag                    | Default | Description                                                                                                                                                                                                          |
| ----------------------- | ------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--debounce <duration>` | `60s`   | Debounce window. First match in a window delivers immediately. Subsequent matches in the same window are suppressed. When the window closes, a trailing summary is sent if any matches were suppressed. Minimum 30s. |
| `--env KEY=VALUE`       | —       | Extra environment variable for the child process. Repeat for multiple.                                                                                                                                               |

**Notes:**

- The daemon auto-populates `CWD` from your current directory at the time you
  run the command.
- The command runs with a minimal environment: `HOME`, `USER`, `LANG`, `TZ`,
  plus any `--env` vars you add. It does NOT inherit your full shell
  environment.
- Max 100 monitors can run concurrently.

**Example:**

```bash
thrum monitor start \
  --name build-watch \
  --match "^(FAIL|PASS|error)" \
  --to @coordinator_main \
  --debounce 120s \
  --env GOPATH=/home/leon/go \
  -- go test ./... -v
```

---

### `thrum monitor list`

List monitor jobs.

```bash
thrum monitor list [--all]
```

Without `--all`, shows only running monitors. With `--all`, includes stopped
ones too.

```text
ID    NAME         STATUS   UPTIME    PID     MATCH
m_01  dev-errors   running  4m32s     91234   (?i)(error|exception|panic)
m_02  build-watch  stopped  —         —       ^(FAIL|PASS|error)
```

---

### `thrum monitor show`

Full detail on a single monitor.

```bash
thrum monitor show <id|name>
```

Shows the full command, env vars (redacted — values are `[redacted]`), match
count, recent matches, debounce config, and PID.

---

### `thrum monitor stop`

Stop a running monitor.

```bash
thrum monitor stop <id|name>
```

Sends SIGTERM. Waits 5 seconds for the process to exit. If it's still running,
sends SIGKILL. Removes the monitor from persistence — it won't respawn on daemon
restart.

---

### `thrum monitor logs`

View captured output from a monitor's process.

```bash
thrum monitor logs <id|name>
```

Shows the last N bytes of combined stdout+stderr from the child process. Useful
for diagnosing why a monitor isn't matching what you expect, or seeing what the
process was doing before it exited.

---

### `thrum monitor restart`

Restart a stopped or dead monitor.

```bash
thrum monitor restart <id|name>
```

Respawns the child process from the saved spec. The monitor must already exist
(use `add` for new ones). The daemon resets match counts and uptime.

---

## How Matches Are Delivered

When a line matches, the daemon sends a synthetic Thrum message to the specified
agent. The message comes from sender `monitor:<name>` — it shows up in inbox as
`@monitor:dev-errors` (or whatever name you chose).

The message body contains the matched line.

### Debounce Behavior

The debounce window prevents alert floods from noisy processes.

- **First match** in a window: delivered immediately.
- **Subsequent matches** in the same window: suppressed.
- **When the window closes**: if any matches were suppressed, the daemon sends a
  trailing summary containing the first suppressed line and a count:
  `"(+N more matches suppressed in the last 60s)"`.
- **Default window**: 60s. Minimum: 30s. Set with `--debounce`.

So if your server logs 200 error lines in 10 seconds, you get one immediate
alert and one summary at the end of the window. Not 200 messages.

### Synthetic Messages

Monitor messages are synthetic — they don't originate from a real registered
agent. The sender name follows the `monitor:<name>` format. You can't reply to
them or use them in threads. They're delivery-only notifications.

The recipient sees them in their inbox like any other message. The
`@monitor:<name>` prefix makes them easy to identify.

---

## Lifecycle

### Persistence

Monitor specs are saved to the daemon's state database (schema v20, `monitors`
table) when you run `thrum monitor start`. If the daemon restarts, it respawns
all saved monitors from scratch — same command, same regex, same recipient.

Stop a monitor with `thrum monitor stop` to remove it from persistence. Stopped
monitors don't respawn.

### Child Exit Notification

When the child process exits (crash, normal termination, whatever), the daemon
sends an exit notification to the recipient agent. The message includes:

- Exit code or signal
- Last 500 bytes of captured output
- A `thrum monitor restart <id>` hint

The daemon does **not** auto-respawn. You decide whether to restart.

### Daemon Restart Behavior

On daemon restart:

1. The daemon loads all monitor specs from the `monitors` table.
2. It respawns each monitor's child process.
3. Match counts and uptime reset — the process is new.

---

## Constraints

| Limit                   | Value                                                                                                    |
| ----------------------- | -------------------------------------------------------------------------------------------------------- |
| Max concurrent monitors | 100                                                                                                      |
| Max line length         | 2KB — longer lines are truncated with `[truncated]`                                                      |
| Min debounce window     | 30s                                                                                                      |
| Default debounce window | 60s                                                                                                      |
| Trailing exit output    | Last 500 bytes                                                                                           |
| Regex engine            | Go stdlib regexp (RE2)                                                                                   |
| Transport               | Unix socket only — `monitor.*` RPC methods are not available over WebSocket or peer/Telegram dispatchers |

### Environment Variables

The child process runs with a minimal env whitelist: `HOME`, `USER`, `LANG`,
`TZ`. Add your own with `--env KEY=VALUE`. Values are stored in the daemon's
state DB and **redacted** (shown as `[redacted]`) in `show` and `list` output.

### No Shell

The command runs via `exec` directly — no shell is ever involved. That means:

- No glob expansion (`*.go` stays `*.go`)
- No environment variable expansion (`$HOME` stays literal)
- No pipes (`|`) or redirects (`>`) in the command itself
- No `&&` or `;` chaining

If you need shell features, pass your command to `sh -c`:

```bash
-- sh -c "make build 2>&1 | grep -E 'error|warning'"
```

---

## Cross-References

- [Messaging](messaging.md) — how messages get routed and delivered to agents
- [CLI Reference](cli.md) — full flag documentation for `thrum monitor`
- [Daemon Architecture](daemon.md) — monitor supervisor lifecycle, startup
  sequence, and persistence details
