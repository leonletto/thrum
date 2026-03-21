---
title: "Telegram Bridge"
description:
  "Bidirectional Telegram integration — relay messages between Telegram and
  Thrum agents via a bridge goroutine inside the daemon"
category: "guides"
order: 12
tags:
  ["telegram", "bridge", "messaging", "mobile", "notifications", "integration"]
last_updated: "2026-03-20"
---

## Telegram Bridge

The Telegram bridge connects your Telegram account to Thrum's messaging system.
Messages you send from Telegram appear as Thrum messages from your user
identity, and messages sent to your Thrum inbox are forwarded to Telegram.

This lets you communicate with your agents from your phone — send instructions,
receive status updates, and reply to messages without being at your terminal.

### How It Works

The bridge runs as a goroutine inside the Thrum daemon. It connects to the
daemon's own WebSocket server as a client (the same way the browser UI does) and
polls Telegram for inbound messages.

```text
Telegram ←→ Bridge Goroutine ←→ Daemon WebSocket ←→ Agents
              (inside daemon)    (JSON-RPC 2.0)
```

- **Inbound:** Telegram message → bridge registers as your user identity → sends
  via `message.send` RPC to the target agent
- **Outbound:** Agent sends message to your inbox → bridge receives via
  WebSocket notification → forwards to your Telegram chat
- **Threading:** Telegram replies map to Thrum `reply_to`; new messages create
  new Thrum threads

The bridge is isolated — it connects via the public WebSocket RPC interface and
never imports internal daemon packages.

### Setup

#### 1. Create a Telegram Bot

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow the prompts
3. Copy the bot token (looks like `123456789:AAH...`)

#### 2. Find Your Telegram User ID

Message [@userinfobot](https://t.me/userinfobot) on Telegram. It replies with
your numeric user ID (e.g., `7747509251`).

#### 3. Configure the Bridge

```bash
thrum telegram configure \
  --token "123456789:AAHyour-token-here" \
  --target "@coordinator_main" \
  --user "your-username"
```

| Flag       | Description                                           |
| ---------- | ----------------------------------------------------- |
| `--token`  | Bot token from BotFather                              |
| `--target` | Agent that receives your Telegram messages (with `@`) |
| `--user`   | Your Thrum username (e.g., `leon-letto`)              |

#### 4. Add Your Telegram User ID to the Allow List

Edit `.thrum/config.json` and add your Telegram user ID to `allow_from`, plus
set `chat_id` to the same value (for DMs, chat ID equals user ID):

```json
{
  "telegram": {
    "token": "123456789:AAH...",
    "target": "@coordinator_main",
    "user_id": "leon-letto",
    "chat_id": 7747509251,
    "allow_from": [7747509251]
  }
}
```

#### 5. Restart the Daemon

```bash
thrum daemon restart
```

The startup banner will show:

```text
Telegram:    bridge enabled (target: @coordinator_main)
```

#### 6. Test It

Send a message from Telegram to your bot. It should appear in the target agent's
inbox:

```bash
thrum inbox --unread
```

Send a message back:

```bash
thrum send "Hello from the terminal!" --to @your-username
```

It should appear in your Telegram chat.

### Configuration Reference

The full configuration lives in `.thrum/config.json` under the `telegram` key:

```json
{
  "telegram": {
    "token": "123456789:AAH...",
    "target": "@coordinator_main",
    "user_id": "leon-letto",
    "chat_id": 7747509251,
    "allow_from": [7747509251, 412587349],
    "allow_all": false,
    "enabled": true
  }
}
```

| Field        | Type   | Description                                                                 |
| ------------ | ------ | --------------------------------------------------------------------------- |
| `token`      | string | Telegram bot token from BotFather. Required.                                |
| `target`     | string | Target agent mention (e.g., `@coordinator_main`). Required.                 |
| `user_id`    | string | Your Thrum username. Required.                                              |
| `chat_id`    | int    | Telegram chat ID for outbound messages. For DMs, same as your user ID.      |
| `allow_from` | int[]  | Telegram user IDs allowed to send messages. Empty = block all.              |
| `allow_all`  | bool   | If true, allow all Telegram users (overrides `allow_from`). Default: false. |
| `enabled`    | bool   | Explicit enable/disable. Default: true when token is set.                   |

### CLI Commands

```bash
# Configure the bridge
thrum telegram configure --token <token> --target <agent> --user <username>

# Check bridge status
thrum telegram status

# Status as JSON
thrum telegram status --json
```

### Security

The bridge follows a defense-in-depth security model:

**Access control:**

- **Fail-closed:** Empty `allow_from` with `allow_all: false` blocks all inbound
  messages. You must explicitly add Telegram user IDs.
- **Gate ordering:** The access check runs before any message processing — a
  blocked sender produces zero observable side effects (no error reply, no
  typing indicator).
- **Bot blocking:** Messages from other Telegram bots (`from.is_bot`) are always
  dropped, even if the bot's ID is in `allow_from`.
- **Rate limiting:** Allowed users have per-user rate limits to prevent abuse.

**Token hygiene:**

- The bot token is never logged, printed in error messages, or included in
  message metadata.
- The bridge struct does not store the token as a field — it passes through to
  the Telegram API at startup and is not retained.
- CLI and web UI display only the first 10 characters (masked).

**Isolation:**

- The bridge connects via the daemon's WebSocket RPC — it never imports internal
  daemon packages.
- The WebSocket client validates the URL is a loopback address before
  connecting.
- Outbound messages are restricted to the configured `chat_id` only.

**Data flow:**

- Only message content is sent to Telegram — no internal metadata (agent IDs,
  session IDs, structured data).
- Telegram metadata (chat ID, message ID, username) is stored in the Thrum
  message's `structured` field, never in the content text.

### Web UI

The Telegram bridge can also be configured from the web UI settings panel.
Navigate to Settings → Telegram to set the token, target agent, and view bridge
status.

The web UI's inbox has been redesigned as a conversation-style chat timeline
(similar to Slack or Telegram). Select an agent from the conversation list to
see the full bidirectional message history.

### Troubleshooting

**Bridge not starting:**

```bash
thrum telegram status
# Check: is the token set? Is enabled = yes?
# Check daemon logs for "telegram bridge:" messages
```

**Messages not arriving from Telegram:**

- Verify your Telegram user ID is in `allow_from`
- Check that no other process is polling the same bot (only one poller per bot
  token)
- Make sure `allow_all` is not false with an empty `allow_from` (fail-closed)

**Messages not forwarding to Telegram:**

- Verify `chat_id` is set (for DMs, same as your user ID)
- Check that the target agent is sending to your Thrum username
  (`--to @your-username`)

**Connection drops after idle:**

- The bridge handles WebSocket keep-alive automatically via ping/pong. If you
  see `client closed` errors in logs, ensure you're running the latest version.

**"Existing token will be replaced" prompt:**

- This confirmation appears when reconfiguring with a different token. Use
  `--yes` to bypass in scripts.
