## Telegram Bridge

**One connection, one unified inbox for your whole team of agents.**

Configure the Telegram bridge once and Telegram becomes the inbox for every
agent in your repo. Any agent that needs your input can ping you directly — the
coordinator asking for a merge approval, an implementer hitting a design
decision, a tester reporting a failure. You reply from Telegram and your reply
routes back to whichever agent messaged you, not through a middle layer. Close
the laptop, put your phone in your pocket, and your team still has a way to
reach you.

This isn't a 1:1 channel to one "target" agent. It's a shared phone line for the
entire team. You just configure a default agent for fresh messages you start
from Telegram — everything else routes by context.

### What you can do with it

- **Get pinged by any agent, any time.** An implementer hits an ambiguous spec
  and asks for your call without going through the coordinator first.
- **Reply threading works by author.** When you reply to a message in Telegram,
  your reply goes back to the agent that sent it — even if that's five different
  agents in the same day, each in its own thread.
- **Start a fresh conversation by typing a new message.** A new message from
  Telegram (no reply-to) lands in the configured default agent's inbox, so
  you've still got a "tell the coordinator" path when you need one.
- **Hold concurrent conversations** with multiple agents without them colliding
  — each Telegram thread maps to one Thrum thread with one author.
- **Stay off the terminal.** Review, decide, approve, nudge, cancel — all from
  your phone.

### How It Works

The bridge runs as a goroutine inside the Thrum daemon. It connects to the
daemon's own WebSocket server as a client (the same way the browser UI does) and
polls Telegram for inbound messages.

```text
Telegram ←→ Bridge Goroutine ←→ Daemon WebSocket ←→ Agents
              (inside daemon)    (JSON-RPC 2.0)
```

**Outbound (Thrum → Telegram):** any agent that sends a message with your bridge
user as a recipient gets forwarded to Telegram. Your coordinator can ping you,
an implementer can ping you, a tester can ping you — they all land in the same
chat.

**Inbound (Telegram → Thrum)** routes by context:

| What you send from Telegram                   | Where it lands                           |
| --------------------------------------------- | ---------------------------------------- |
| Fresh message (no Telegram reply-to)          | The configured `--target` agent          |
| Reply to a message an agent sent you          | The agent that sent the original message |
| Reply to one of your own messages (edge case) | Falls back to the configured `--target`  |

**Threading:** Telegram replies map to Thrum `reply_to` on the matching thread,
so the agent sees your reply as a continuation of the original conversation, not
a new thread.

The bridge is isolated — it connects via the public WebSocket RPC interface and
never imports internal daemon packages.

### Setup

#### 1. Create a Telegram Bot

1. Open Telegram and message [@BotFather](https://t.me/BotFather)
2. Send `/newbot` and follow the prompts
3. Copy the bot token (looks like `123456789:AAH...`)

#### 2. Configure and Pair

Run `configure` with your bot token. The command writes the config, restarts the
daemon, and enters a pairing flow that captures your Telegram user ID
automatically:

```bash
thrum telegram configure \
  --token "123456789:AAHyour-token-here" \
  --target "@coordinator_main" \
  --user "your-username"
```

After writing the config, it prompts you to send a message from Telegram:

```text
Pairing — send any message to your bot from Telegram (timeout: 60s)...

Message from: Jane Doe (ID: 123456789)
  Allow this user? [y/n]: y

Paired! Allowed users: [123456789]
  Bridge is live — no further restart needed.
```

| Flag             | Description                                                                                                                       |
| ---------------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `--token`        | Bot token from BotFather                                                                                                          |
| `--target`       | Default agent for fresh messages you start from Telegram (with `@`). Replies route to the author of the message being replied to. |
| `--user`         | Your Thrum username (e.g., `your-username`)                                                                                       |
| `--allow-from`   | Skip pairing — set Telegram user ID directly                                                                                      |
| `--chat-id`      | Telegram chat ID for outbound (defaults to --allow-from)                                                                          |
| `--pair-timeout` | How long to wait for pairing message (default: 60s)                                                                               |
| `--skip-pair`    | Write config only, don't pair                                                                                                     |

#### Re-pairing

If you need to pair again (e.g., after resetting the allow list), use the
standalone pair command:

```bash
thrum telegram pair
```

This connects to the running daemon and waits for a Telegram message to capture
your user ID.

#### Manual Setup (alternative)

If you already know your Telegram user ID, skip the interactive pairing:

```bash
thrum telegram configure \
  --token "123456789:AAHyour-token-here" \
  --target "@coordinator_main" \
  --user "your-username" \
  --allow-from 123456789
```

Then restart the daemon:

```bash
thrum daemon restart
```

#### 3. Test It

Send a fresh message from Telegram to your bot. It lands in the configured
default agent's inbox:

```bash
thrum inbox --unread
```

Now have any agent in the team send you a message:

```bash
thrum send "Hello from the terminal!" --to @your-username
```

It should appear in your Telegram chat. Reply to that Telegram message and your
reply goes back to the sending agent — not the default target. That's the
reply-aware routing doing its job.

You can repeat this with a different agent and each thread stays independent:
fresh message → default agent, reply → original author.

### Configuration Reference

The full configuration lives in `.thrum/config.json` under the `telegram` key:

```json
{
  "telegram": {
    "token": "123456789:AAH...",
    "target": "@coordinator_main",
    "user_id": "leon-letto",
    "chat_id": 123456789,
    "allow_from": [123456789, 412587349],
    "allow_all": false,
    "enabled": true
  }
}
```

| Field        | Type   | Description                                                                                                                                   |
| ------------ | ------ | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `token`      | string | Telegram bot token from BotFather. Required.                                                                                                  |
| `target`     | string | Default agent mention (e.g., `@coordinator_main`) for fresh messages you start from Telegram. Replies route to the original author. Required. |
| `user_id`    | string | Your Thrum username. Required.                                                                                                                |
| `chat_id`    | int    | Telegram chat ID for outbound messages. For DMs, same as your user ID.                                                                        |
| `allow_from` | int[]  | Telegram user IDs allowed to send messages. Empty = block all.                                                                                |
| `allow_all`  | bool   | If true, allow all Telegram users (overrides `allow_from`). Default: false.                                                                   |
| `enabled`    | bool   | Explicit enable/disable. Default: true when token is set.                                                                                     |

### CLI Commands

```bash
# Configure the bridge (interactive pairing)
thrum telegram configure --token <token> --target <agent> --user <username>

# Configure with known user ID (skip pairing)
thrum telegram configure --token <token> --target <agent> --user <username> --allow-from <id>

# Pair your Telegram account (bridge must be configured and daemon running)
thrum telegram pair

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

**Pairing security:**

- During pairing, the bridge temporarily accepts messages from any Telegram user
  for up to 60 seconds (configurable, max 5 minutes).
- The user must explicitly confirm the sender via a `[y/n]` prompt.
- Only one pairing session can be active at a time.
- The pairing message is consumed and never relayed to Thrum agents.
- No persistent state changes occur during pairing — a crash reverts to the
  prior access control state (block all).

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
- Check that the sending agent addressed you by username
  (`--to @your-username`). Any agent can DM you this way — not just the
  configured default target.

**Reply from Telegram went to the wrong agent:**

- Confirm you used Telegram's "reply" feature on the agent's message, not a
  fresh message in the chat.
- Fresh messages always route to the configured default (`--target`). Only
  replies use the reply-aware routing.
- If you replied to one of your own past messages, the bridge falls back to the
  default target to avoid a self-mention loop.

**Connection drops after idle:**

- The bridge handles WebSocket keep-alive automatically via ping/pong. If you
  see `client closed` errors in logs, ensure you're running the latest version.

**"Existing token will be replaced" prompt:**

- This confirmation appears when reconfiguring with a different token. Use
  `--yes` to bypass in scripts.

### Next: Telegram Groups

Once your DM bridge is working, you can set up a shared Telegram group so your
whole team can interact with the same agent. See
[Telegram Groups](telegram-groups.md) for setup.
