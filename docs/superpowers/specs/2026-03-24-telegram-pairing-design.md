# Telegram Pairing Flow

**Date:** 2026-03-24
**Status:** Approved
**Branch:** thrum-dev

## Problem

When configuring the Telegram bridge on a new repo, the user doesn't know their
Telegram user ID or chat ID. The current flow requires manually copying
`allow_from` and `chat_id` from another working config. Without these fields,
the bridge blocks all incoming messages (`Access: block all`).

## Solution

Add an interactive pairing flow to `thrum telegram configure` that temporarily
opens the bridge to capture the first incoming Telegram message, presents the
sender for confirmation, and locks down the whitelist automatically.

## User-Facing Flow

### New setup (configure + pair in one command)

```text
thrum telegram configure --token 123:AAH... --target @coordinator_main --user leon-letto
Telegram bridge configured.
  Token:  123456789...
  Target: @coordinator_main
  User:   leon-letto

Starting daemon with new config...
Daemon restarted

Pairing -- send any message to your bot from Telegram (timeout: 60s)...

Message from: Leon Letto (ID: 123456789)
  Allow this user? [y/n]: y

Paired! Allowed users: [123456789]
  Bridge is live -- no further restart needed.
```

### Pair an already-configured bridge

```text
thrum telegram pair
Pairing -- send any message to your bot from Telegram (timeout: 60s)...

Message from: Leon Letto (ID: 123456789)
  Allow this user? [y/n]: y

Paired! Allowed users: [123456789]
```

### Skip pairing (ID already known)

```bash
thrum telegram configure --token 123:AAH... --target @coord --user leon --allow-from 123456789
```

### Declined pairing

If the user declines the `[y/n]` prompt, the probe message is consumed and
discarded. The bridge returns to `block all` state (no `allow_from` set).
To retry, run `thrum telegram pair` again.

## New CLI Flags

### `thrum telegram configure`

| Flag              | Type     | Default | Description                                       |
| ----------------- | -------- | ------- | ------------------------------------------------- |
| `--allow-from`    | int64    | 0       | Telegram user ID to whitelist (skips pairing)     |
| `--chat-id`       | int64    | 0       | Telegram chat ID for outbound (defaults to allow-from for personal chats) |
| `--pair-timeout`  | duration | 60s     | How long to wait for a pairing message            |
| `--skip-pair`     | bool     | false   | Write config only, don't pair                     |

The existing `--yes` flag on `configure` also applies to the pairing
confirmation step. When `--yes` is set, both the token-replacement prompt and
the pair confirmation are auto-accepted.

### `thrum telegram pair` (new subcommand)

| Flag             | Type     | Default | Description                                       |
| ---------------- | -------- | ------- | ------------------------------------------------- |
| `--pair-timeout` | duration | 60s     | How long to wait for a pairing message            |
| `--yes`          | bool     | false   | Auto-accept the first sender without prompting    |

**Note on `--yes`:** This removes the explicit-consent safeguard. Intended for
automated testing and trusted environments only. In untrusted contexts, omit
`--yes` so that the user visually confirms the sender identity.

## RPC Design

### New method: `telegram.pair`

**Request:**

```json
{
  "method": "telegram.pair",
  "params": {
    "timeout_seconds": 60
  }
}
```

**Handler behavior:**

1. Validate `timeout_seconds` is between 1 and 300 (max 5 minutes). Return
   error if out of range.
2. Wait for bridge readiness: poll `bridge.Running()` for up to 5 seconds
   (100ms intervals). Return error `"bridge not running"` if it doesn't become
   ready.
3. Call `bridge.Pair(ctx, timeout)`.
4. Return sender info on success.

**Response (success):**

```json
{
  "telegram_user_id": 123456789,
  "telegram_username": "jdoe",
  "first_name": "Leon",
  "last_name": "Letto",
  "chat_id": 123456789,
  "message_text": "hello"
}
```

**Response (timeout):**

```json
{
  "error": "no message received within 60s"
}
```

**Response (already pairing):**

```json
{
  "error": "pairing already in progress"
}
```

**After user confirms**, the CLI calls the existing `telegram.configure` RPC to
set `allow_from` and `chat_id`. The bridge picks up the new config via its
existing `Restart(newCfg)` method (a goroutine cycle reset within the daemon
process, not a process restart).

**CLI must use `CallWithTimeout`:** The default RPC client timeout is 10s
(`defaultCallTimeout` in `internal/cli/client.go`). Since `telegram.pair` blocks
server-side for up to 300s, the CLI must use `client.CallWithTimeout()` with
a deadline of `pairTimeout + 5s` buffer. This follows the existing pattern used
by `peer.wait_pairing`.

## Implementation

### Files to modify

| File                                 | Change                                                |
| ------------------------------------ | ----------------------------------------------------- |
| `internal/bridge/telegram/bridge.go` | Add `Pair(ctx, timeout) (PairResult, error)` method, `PairResult` struct, pair mutex |
| `internal/bridge/telegram/bot.go`    | Add `pairCh` via `atomic.Pointer`, branch in `Poll()` to intercept during pairing |
| `internal/daemon/rpc/telegram.go`    | Add `HandlePair` RPC handler with bridge readiness polling and timeout cap |
| `cmd/thrum/main.go`                  | Add `telegram pair` subcommand; modify `configure` to auto-restart + flow into pairing; add `--allow-from`, `--chat-id`, `--pair-timeout`, `--skip-pair` flags |

No new files. No config schema changes (`allow_from`, `chat_id`, `allow_all`
already exist).

### Bridge.Pair() logic

```text
Bridge.Pair(ctx, timeout):
  1. TryLock pairMu -- return error "pairing already in progress" if locked
  2. Create pairCh (chan PairResult, buffered 1)
  3. Store pairCh via bot.pairCh.Store(&pairCh)  [atomic.Pointer]
  4. Set bot.pairMode.Store(true)  [atomic.Bool -- AFTER pairCh is observable]
  5. Defer (in order):
     a. bot.pairMode.Store(false)
     b. bot.pairCh.Store(nil)
     c. pairMu.Unlock()
  6. Select:
     - <-pairCh: got result -> success
     - <-time.After(timeout): timeout -> error
     - <-ctx.Done(): cancelled -> error
```

**Memory ordering:** `pairCh` is stored via `atomic.Pointer` before `pairMode`
is set to `true`. In `Poll()`, `pairMode` is checked first; if true, `pairCh`
is loaded via `atomic.Pointer`. The store-before-flag / check-flag-before-load
pattern ensures `Poll()` always sees a valid channel when `pairMode` is true.

### Bot.Poll() changes

When `pairMode` is true:

- Skip the `IsAllowed` check
- Load `pairCh` via `atomic.Pointer` and send the `PairResult`
- The message is consumed (not forwarded to inbound relay)
- Only the first message triggers; `pairMode` reverts via `Bridge.Pair()` defer

### CLI configure flow

```text
runTelegramConfigure:
  1. Collect token/target/user (existing logic)
  2. Write config (existing logic)
  3. If --allow-from provided:
       Set chat_id = --chat-id if provided, else chat_id = allow_from
       Write allow_from + chat_id to config
       Print "restart daemon to apply", done
  4. Else if --skip-pair:
       Print "restart daemon to apply", done
  5. Else:
       Restart daemon (call cli.RestartDaemon -- returns after readiness)
       Call telegram.pair RPC via CallWithTimeout(pairTimeout + 5s)
       Display sender info
       If --yes or user confirms [y/n]:
         Call telegram.configure RPC with allow_from + chat_id
         Print "Paired! Bridge is live."
       Else:
         Print "Pairing skipped. Run 'thrum telegram pair' to retry."
```

### CLI pair subcommand

```text
runTelegramPair:
  1. Connect to daemon via getClient() -- error if daemon not running
  2. Read config, check token exists -- error if not configured
  3. Call telegram.pair RPC via CallWithTimeout(pairTimeout + 5s)
  4. Display sender info
  5. If --yes or user confirms [y/n]:
       Call telegram.configure RPC with allow_from + chat_id
       Print "Paired!"
  6. Else:
       Print "Pairing skipped. Run 'thrum telegram pair' to retry."
```

## Security Model

### Threat model for pairing

**Window exposure:** During pairing, any Telegram user who knows the bot's
username can send a message and be presented for approval. This is acceptable
because:

- The window is short (default 60s, max 5 minutes enforced server-side)
- The user must explicitly confirm the sender (`[y/n]` prompt)
- Only one pairing session at a time (mutex)
- The probe message is never relayed to Thrum

**No persistent state change:** `pairMode` is an in-memory `atomic.Bool`, never
written to disk. A crash during pairing reverts to the prior state (block all).

**Post-pairing:** The `allow_from` whitelist is the permanent gate. All messages
from non-whitelisted users are silently dropped before content extraction or
logging.

**Bot token exposure:** The token is masked in all CLI output (`MaskedToken()`
shows first 10 chars). Config file is written with `0600` permissions.

**Outbound hardcoding:** The bridge only sends to the configured `chat_id` --
never to arbitrary Telegram chats, even if a Thrum message contains a different
chat ID.

**`--yes` flag risk:** When `--yes` is used, the first Telegram sender is
auto-accepted without visual confirmation. If a bot scan hits the Telegram bot
before the legitimate user, the scanner's ID is whitelisted. Use `--yes` only
in automated testing or trusted environments. In production, always confirm
the sender visually.

**Timeout cap:** The RPC handler enforces a maximum of 300s (5 minutes) for the
pairing window, regardless of what the CLI requests. This prevents indefinite
open windows from misconfiguration.

### Pairing-specific safeguards

| Safeguard        | Implementation                                           |
| ---------------- | -------------------------------------------------------- |
| Short window     | Default 60s timeout, max 300s enforced by RPC handler    |
| Explicit consent | `[y/n]` prompt required (unless `--yes`)                 |
| Single session   | `pairMu` mutex; concurrent calls return `"pairing already in progress"` |
| No relay         | Pairing message consumed, never forwarded to Thrum       |
| No disk state    | `pairMode` is in-memory only, reverts on timeout/crash   |
| Fail-closed      | If pairing fails or is declined, `allow_from` stays empty = block all |
| Atomic safety    | `pairCh` stored via `atomic.Pointer`, set before `pairMode` flag |

### Restart clarification

After pairing confirmation, the CLI calls the `telegram.configure` RPC which
internally calls `bridge.Restart(newCfg)`. This is a goroutine cycle reset
within the running daemon process -- not a daemon process restart. The bridge
cancels its current run loop and re-enters with the updated config. This happens
after `Pair()` has already returned, so there is no conflict between the active
pairing and the restart.

## Testing

- Unit test: `Bridge.Pair()` returns correct sender info from a mock bot
- Unit test: `Pair()` rejects concurrent calls with `"pairing already in progress"` error
- Unit test: `Pair()` times out correctly
- Unit test: `Bot.Poll()` routes to `pairCh` when `pairMode` is true
- Unit test: `Bot.Poll()` skips `IsAllowed` during pair mode
- Unit test: `HandlePair` rejects `timeout_seconds` > 300
- Unit test: `HandlePair` polls bridge readiness before pairing
- Integration test: `configure` with `--allow-from` writes config correctly
- Integration test: `configure` with `--allow-from` sets `chat_id` to same value when `--chat-id` omitted
- Integration test: `pair` subcommand calls RPC and updates config on confirm
- Integration test: `pair` subcommand prints retry message on decline
