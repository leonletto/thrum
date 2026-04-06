## Telegram Groups

The [DM bridge](telegram-bridge.md) connects one person to their agents. That's
great when you're the only one working with a repo. But if you're on a team — a
few engineers, maybe a PM — and you all want to interact with the same agent,
DMs don't work. Everyone would need their own bot, their own pairing, their own
private conversation. Nobody sees what anyone else asked or what the agent said
back.

Telegram group support fixes this. You create a Telegram group, add your repo's
bot, and invite your team. Anyone in the group can send instructions to the
agent. The agent's responses appear in the group where everyone can see them.
You get a shared conversation with your agent that the whole team can
participate in.

### How It Works

```text
Telegram Group
┌─────────────────────────────────┐
│  Alice: @thrum_bot check the    │
│         deploy status           │
│                                 │
│  thrum_bot: Deploy is green.    │
│    Last push 12m ago by Bob.    │
│                                 │
│  Bob: @thrum_bot run the smoke  │
│       tests on staging          │
│                                 │
│  thrum_bot: All 47 tests pass.  │
└─────────────┬───────────────────┘
              │
              ▼
┌─────────────────────────────────┐
│  Thrum Daemon                   │
│                                 │
│  tg:dev-team (mirrored group)   │
│  ├── @coordinator_main          │
│  ├── user:alice                 │
│  └── user:bob                   │
└─────────────────────────────────┘
```

- **Inbound:** Someone sends a message in the Telegram group. If it @mentions
  the bot (or has no @mention at all), the bridge relays it into a mirrored
  Thrum group (`tg:dev-team`). The target agent receives it.
- **Outbound:** The agent sends a message to the mirrored Thrum group. The
  bridge posts it back to the Telegram group where everyone can see it.
- **Threading:** Telegram replies map to Thrum `reply_to`. If someone replies to
  the bot's message in the group, the agent sees it as a reply to the original
  thread.
- **Identity:** Each person's messages are tagged with their Telegram username,
  so the agent knows who's asking.

### Prerequisites

1. **A working DM bridge.** Follow the [Telegram Bridge](telegram-bridge.md)
   guide first. You'll have a bot token, a paired Telegram account, and a
   working DM relay. The group feature builds on top of this.
2. **Bot privacy mode disabled.** By default, Telegram bots in groups only see
   messages that @mention them or are commands. For broadcast messages (no
   @mention) to work, you need privacy mode turned off. In Telegram, message
   [@BotFather](https://t.me/BotFather), send `/setprivacy`, select your bot,
   and choose **Disable**.

### Setup

#### 1. Create a Telegram Group

Create a regular Telegram group. Add your bot and invite your team. Give it a
name that makes sense — "Dev Coordination" or "Backend Agents" or whatever.

Note the group's chat ID. Easiest way: send a message in the group, then check
`thrum telegram status --json`. Group chat IDs are negative numbers (e.g.,
`-100123456789`).

#### 2. Configure the Group

Add the group to your Telegram config. You can do this from the web UI (Settings
→ Telegram → Groups) or the config file directly.

`.thrum/config.json`:

```json
{
  "telegram": {
    "token": "...",
    "target": "@coordinator_main",
    "user_id": "leon-letto",
    "chat_id": 7747509251,
    "enabled": true,
    "allow_from": [7747509251],
    "groups": [
      {
        "chat_id": -100123456789,
        "name": "dev-team"
      }
    ]
  }
}
```

Everyone in the group who should be able to message the agent needs their
Telegram user ID in the global `allow_from` list. Same gate as DMs — if your ID
isn't in the list, your messages are silently dropped.

#### 3. Restart and Test

```bash
thrum daemon restart
```

On startup, the bridge creates a mirrored Thrum group named `tg:dev-team` and
adds your user and target agent as members.

Test it — send a message in the Telegram group mentioning the bot:

```
@your_bot_name hello from the group
```

Check the agent's inbox:

```bash
thrum inbox --unread
```

You should see the message. Have the agent reply to the mirrored group and
verify it appears in Telegram.

### @Mention Routing

Messages in the group are routed based on @mentions:

| Message                              | What happens                                    |
| ------------------------------------ | ----------------------------------------------- |
| `@thrum_bot check the API`           | Bot relays to agent                             |
| `Schema changed in v2` (no @mention) | Bot relays to agent (broadcast)                 |
| `Hey @alice what do you think?`      | Bot ignores — addressed to a human, not the bot |

If privacy mode is disabled, the bot sees everything. It relays messages that
either @mention it or have no @mention at all. Messages that @mention a human
(not the bot) are left alone — the person gets a normal Telegram notification
and the agent doesn't need to see it.

### Security

The same security model from the DM bridge applies to groups:

- **`allow_from` is global.** A person's Telegram user ID must be in the
  `allow_from` list to send messages through any chat — DM or group. There's no
  per-group allow list.
- **Fail-closed.** Empty `allow_from` with `allow_all: false` blocks everyone.
- **Bot blocking.** Messages from other bots are dropped by default (see
  [Multi-Bot Groups](#multi-bot-groups) below for the exception).
- **Rate limiting.** Per-user rate limits apply in groups the same as DMs.

### Configuration Reference

The `groups` array sits alongside the existing DM fields in the `telegram`
config:

#### TelegramGroup

| Field           | Type   | Description                                                                                      |
| --------------- | ------ | ------------------------------------------------------------------------------------------------ |
| `chat_id`       | int    | Telegram group chat ID (negative number)                                                         |
| `name`          | string | Human-readable name — also used as the mirrored Thrum group name (`tg:{name}`)                   |
| `trusted_bots`  | int[]  | Bot user IDs allowed to relay messages in this group (see [Multi-Bot Groups](#multi-bot-groups)) |
| `remote_agents` | array  | Proxy agent definitions (see [Multi-Bot Groups](#multi-bot-groups))                              |

#### RemoteAgent

| Field    | Type   | Description                                              |
| -------- | ------ | -------------------------------------------------------- |
| `name`   | string | Agent name in the remote repo (e.g., `coordinator_main`) |
| `prefix` | string | Local prefix — the proxy registers as `{prefix}:{name}`  |
| `bot`    | string | Target bot's @username for @mention routing              |

### Web UI

The web UI's Settings → Telegram panel has a **Groups** section where you can:

- Add and remove groups (chat ID + name)
- Manage trusted bot IDs per group
- Configure remote agents (name, prefix, target bot)
- See group connection status

All changes take effect on daemon restart.

### Multi-Bot Groups

Thrum supports putting multiple bots in the same Telegram group. This is fully
implemented — trusted bot allowlists, proxy agent registration, @mention routing
between bots. The use case is cross-repo coordination: Repo A's bot and Repo B's
bot in the same group, agents messaging each other through it.

**There's a catch.** Telegram's Bot API has a server-side restriction: bots
cannot see messages from other bots in groups. This is intentional on Telegram's
part — it prevents bot loops. What this means in practice:

- Your bot can **send** messages to the group, and they appear for humans and in
  the Telegram UI
- Another bot in the same group will **not receive** those messages
- So agent-to-agent communication through a shared Telegram group doesn't work

The outbound direction works fine. If you want your agent's messages to be
visible in a group that humans are watching, that works. What doesn't work is
the return path — the other repo's bot picking up those messages and delivering
them to its agents.

I'm leaving the multi-bot infrastructure in place because:

1. The outbound routing is genuinely useful (agents can post to groups that
   humans monitor)
2. Telegram might change this restriction in the future
3. Cross-repo agent-to-agent communication will come through a different
   mechanism — likely local daemon-to-daemon communication or Tailscale sync,
   not Telegram

If you want to configure trusted bots or proxy agents anyway (for the outbound
direction), here's how:

#### Trusted Bots

Add bot user IDs to the group's `trusted_bots` array. This creates a per-group
exception to the default "drop all bot messages" rule:

```json
{
  "groups": [
    {
      "chat_id": -100123456789,
      "name": "cross-repo-coord",
      "trusted_bots": [8693071965]
    }
  ]
}
```

#### Proxy Agents

Proxy agents register a remote agent as a local stand-in. They show up in
`thrum team` and can be addressed directly:

```json
{
  "groups": [
    {
      "chat_id": -100123456789,
      "name": "cross-repo-coord",
      "trusted_bots": [8693071965],
      "remote_agents": [
        {
          "name": "coordinator_main",
          "prefix": "falcon",
          "bot": "@falconmode_backend_bot"
        }
      ]
    }
  ]
}
```

Sending to a proxy agent posts a message to the Telegram group with the target
bot's @mention:

```bash
thrum send "check the /users endpoint" --to @falcon:coordinator_main
```

The message appears in the group as:

```
@falconmode_backend_bot @coordinator_main: check the /users endpoint
```

Humans in the group can see it. The target bot cannot (Telegram limitation). The
proxy agent shows in `thrum team` as:

```
● @falcon:coordinator_main [remote] (falcon-backend) — via tg:cross-repo-coord
```

### Troubleshooting

**Messages not arriving from the group:**

- Check bot privacy mode. If privacy mode is on, the bot only sees @mentions and
  commands — broadcast messages won't come through. Disable via BotFather:
  `/setprivacy` → your bot → Disable.
- Verify the bot is actually in the Telegram group.
- Check that the sender's Telegram user ID is in `allow_from`.

**Bot responses not appearing in the group:**

- Verify `chat_id` in the group config is correct (should be negative).
- Check the daemon logs for send errors.

**Group chat ID:**

- Telegram group IDs are negative numbers (e.g., `-100123456789`). If your chat
  ID is positive, you're probably using a user ID, not a group ID.
- Easiest way to find it: send a message in the group, then check
  `thrum telegram status --json`.
