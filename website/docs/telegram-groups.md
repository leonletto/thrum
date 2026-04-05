---
title: "Cross-Repo Communication"
description:
  "Connect agents across different repos using shared Telegram groups — proxy
  agents, @mention routing, and direct addressing without network configuration"
category: "guides"
order: 13
tags:
  [
    "telegram",
    "cross-repo",
    "multi-repo",
    "groups",
    "proxy-agents",
    "coordination",
  ]
last_updated: "2026-04-05"
---

## Cross-Repo Communication

In the [single-agent-mode](single-agent-mode.md) release notes I mentioned
cross-project agent coordination as the next thing I was thinking about. This
is it.

If you work with multiple repos that depend on each other — a backend and a
frontend, a primary service and a couple of microservices, a monorepo with
satellite projects — you've hit the moment where a change in one repo means a
change in another. Today you handle that by context-switching: stop what you're
doing, go to the other repo, explain to a different agent what changed and why,
hope you got the details right. That's the same manual relay problem Thrum
solved within a single repo, just across repo boundaries.

The solution is a shared Telegram group. Each repo's bot joins the same group.
Messages flow between the Telegram group and a mirrored Thrum group inside each
daemon. No network configuration, no Tailscale, no direct daemon connections.
If both repos have a Telegram bot, they can talk to each other.

### How It Works

Each repo already has a Telegram bot from the
[DM bridge setup](telegram-bridge.md). You create a Telegram group, add both
bots to it, and configure each repo to recognize the other's bot as trusted.
Thrum does the rest:

```text
Repo A (thrum)                    Repo B (falcon-backend)
┌────────────────┐                ┌────────────────┐
│ coordinator    │                │ coordinator    │
│ implementer(s) │                │ implementer(s) │
│                │                │                │
│ tg:cross-repo ←── Telegram ──→ tg:cross-repo  │
│ (mirrored grp) │   Group Chat   │ (mirrored grp) │
│                │                │                │
│ thrum-bot  ────┼──→ group ←──┼──── falcon-bot │
└────────────────┘                └────────────────┘
```

- **Inbound:** A message lands in the Telegram group. Each bot checks: is this
  @mentioning me specifically, or is it a broadcast (no @mention)? If it's for
  us or for everyone, the bot relays it into the local mirrored Thrum group
  (`tg:cross-repo`). If it @mentions a different bot, we ignore it.
- **Outbound:** An agent sends a message to the mirrored Thrum group. The
  bridge posts it to the Telegram group. The other repo's bot picks it up.
- **Echo prevention:** Each bot ignores its own messages in the group. No loops.

### Prerequisites

Before setting up group communication, you need:

1. **A working DM bridge in each repo.** Follow the
   [Telegram Bridge](telegram-bridge.md) guide for each repo. You'll have a bot
   token, a paired Telegram account, and a working DM relay.
2. **Bot privacy mode disabled.** By default, Telegram bots in groups only see
   messages that @mention them or are commands. For broadcast messages (no
   @mention) to work, each bot needs privacy mode turned off. In Telegram,
   message [@BotFather](https://t.me/BotFather), send `/setprivacy`, select
   your bot, and choose **Disable**.

### Setup

#### 1. Create a Telegram Group

Create a regular Telegram group. Give it a descriptive name — "Thrum + Falcon
Coordination" or whatever makes sense for your repos. Add both bots to the
group.

Note the group's chat ID. The easiest way to get it: send a message in the
group, then check `thrum telegram status --json`. The group chat ID is negative
(e.g., `-100123456789`).

#### 2. Configure Each Repo

In each repo, add the group to your Telegram config. You can do this from the
web UI (Settings → Telegram → Groups) or the config file directly.

**Repo A** (`.thrum/config.json`):

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
        "name": "cross-repo-coord",
        "trusted_bots": [8693071965]
      }
    ]
  }
}
```

**Repo B** — same structure, but `trusted_bots` lists Repo A's bot user ID
instead.

The `trusted_bots` array is the key piece. Normally, Thrum drops all messages
from bots — that's the security default from the DM bridge. The trusted bots
list creates a per-group exception: "in this specific group, messages from
these specific bot IDs are allowed through." Each repo independently decides
which bots it trusts.

#### 3. Restart and Verify

Restart both daemons:

```bash
thrum daemon restart
```

On startup, the bridge:

1. Creates a mirrored Thrum group named `tg:cross-repo-coord`
2. Adds your user and target agent as members
3. Registers any proxy agents you've configured (see below)

Test it — send a message in the Telegram group and check that it appears in
both repos:

```bash
thrum inbox --unread
```

---

### Proxy Agents

The group relay gets messages flowing between repos. But "a message appeared in
the cross-repo group" isn't the same as "I can send a message to the Falcon
coordinator." Proxy agents close that gap.

A proxy agent is a local stand-in for a remote agent in another repo. You
configure it, Thrum registers it on startup, and it shows up in `thrum team`
like any other agent. When someone sends a message to the proxy, the bridge
routes it through the Telegram group to the right bot, which delivers it to the
real agent.

#### Configuring Proxy Agents

Add `remote_agents` to a group in your config:

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

This tells Thrum: "There's an agent called `coordinator_main` in the Falcon
repo. Register it locally as `falcon:coordinator_main`. When someone sends it a
message, route it through the Telegram group to `@falconmode_backend_bot`."

#### Sending to a Remote Agent

```bash
thrum send "check the /users endpoint — the response schema changed" \
  --to @falcon:coordinator_main
```

That message goes through the bridge to the Telegram group as:

```
@falconmode_backend_bot @coordinator_main: check the /users endpoint — the response schema changed
```

The Falcon bot sees its @mention, strips it, and delivers the message to its
local `coordinator_main` as a DM. When the coordinator replies, the response
flows back through the group.

#### How It Looks in thrum team

```
● @coordinator_main (main) — Coordinate agents and tasks [live]
● @implementer (website-dev) — Write documentation [live]
● @falcon:coordinator_main [remote] (falcon-backend) — via tg:cross-repo-coord
```

Remote agents show their prefix, the `[remote]` tag, and which Telegram group
they're reachable through. Your agents can address them like any local agent —
they don't need to know the message is crossing repo boundaries.

---

### Security

Every aspect of this is explicit configuration. There's no auto-discovery, no
"the bots found each other." Each repo independently controls:

- **Which bots are trusted** — the `trusted_bots` array is per-group. A bot
  not in the list is silently dropped, same as the DM bridge's `allow_from`.
- **Which remote agents are visible** — `remote_agents` is a whitelist. Falcon
  might expose its coordinator to you, but not its implementers. You decide
  what your repo can see.
- **Asymmetric access** — Repo A can see Repo B's coordinator without Repo B
  seeing anything in Repo A. Each side configures independently.

The security model is the same principle as the DM bridge: fail-closed, explicit
allowlists, no side effects from blocked messages.

---

### @Mention Routing

Messages in the Telegram group are routed based on @mentions:

| Message | Who relays it |
|---------|---------------|
| `@thrum_bot check the API` | Only Thrum's bot |
| `@falcon_bot run the tests` | Only Falcon's bot |
| `Schema changed in v2` (no @mention) | All bots — broadcast |
| `@thrum_bot @falcon_bot coordinate` | Both mentioned bots |

This means you can be precise about who you're talking to. If you send a
message to the group from Telegram with `@falcon_bot`, only the Falcon repo
picks it up. If you just send a message with no @mention, every repo in the
group gets it.

Agents sending through Thrum don't need to think about this. When you use
`thrum send --to @falcon:coordinator_main`, the bridge handles the @mention
routing automatically.

---

### Configuration Reference

The `groups` array sits alongside the existing DM fields in the `telegram`
config:

#### TelegramGroup

| Field | Type | Description |
|-------|------|-------------|
| `chat_id` | int | Telegram group chat ID (negative number) |
| `name` | string | Human-readable name — also used as the mirrored Thrum group name (`tg:{name}`) |
| `trusted_bots` | int[] | Bot user IDs allowed to relay messages in this group |
| `remote_agents` | array | Proxy agent definitions (optional) |

#### RemoteAgent

| Field | Type | Description |
|-------|------|-------------|
| `name` | string | Agent name in the remote repo (e.g., `coordinator_main`) |
| `prefix` | string | Local prefix — the proxy registers as `{prefix}:{name}` |
| `bot` | string | Target bot's @username for @mention routing |

---

### Web UI

The web UI's Settings → Telegram panel now has a **Groups** section where you
can:

- Add and remove groups (chat ID + name)
- Manage trusted bot IDs per group
- Configure remote agents (name, prefix, target bot)
- See group connection status

All changes take effect on daemon restart.

---

### Troubleshooting

**Messages not arriving from the group:**

- Check bot privacy mode. If privacy mode is on, the bot only sees @mentions
  and commands — broadcast messages won't come through. Disable via BotFather:
  `/setprivacy` → your bot → Disable.
- Verify the bot is actually in the Telegram group.
- Check `trusted_bots` — if the sending bot isn't in your trusted list, its
  messages are silently dropped.

**Echo loops (message bouncing between repos):**

- Each bot ignores messages from its own bot ID. If you see echoes, check that
  the bot user ID used for posting matches the ID the bot sees as "self." Run
  `thrum telegram status --json` to verify.

**Proxy agent not showing in thrum team:**

- Restart the daemon after adding `remote_agents` to the config.
- Check the daemon logs for registration errors — the agent name might conflict
  with a local agent.

**Messages arriving but not routed to the right agent:**

- Check @mention formatting. The bridge parses `@bot_username` from the message
  text. If the bot username has special characters or differs from what's in
  your config, routing breaks.
- For proxy agents, verify the `bot` field matches the target bot's actual
  Telegram username (without the `@` in the config, but with it in message
  text).

**Group chat ID:**

- Telegram group IDs are negative numbers (e.g., `-100123456789`). If your
  chat ID is positive, you're probably using a user ID, not a group ID.
- Easiest way to find it: send a message in the group, then check
  `thrum telegram status --json`.
