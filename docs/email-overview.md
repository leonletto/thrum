## Email Bridge

**Route Thrum messages across machines over standard IMAP/SMTP — no dedicated
server, no VPN, no custom network configuration.**

The email bridge is the v0.11 substrate for cross-machine agent coordination.
Each daemon connects to a shared mailbox. Messages between daemons are delivered
as email, picked up via IMAP IDLE, and injected into the local agent graph as
ordinary Thrum messages. From an agent's perspective, messages from a remote
daemon look identical to local messages.

---

## When to Use the Email Bridge

| Scenario                                     | Recommendation                                                        |
| -------------------------------------------- | --------------------------------------------------------------------- |
| Two machines, one operator, private mesh     | Email bridge with a dedicated `@gmail.com` or `@fastmail.com` address |
| Team of operators, each on separate machines | Email bridge with one shared mailbox or per-operator addresses        |
| Low-latency real-time sync on a LAN          | [Tailscale Sync](tailscale-sync.md) — lower latency than IMAP poll    |
| Mobile notifications only                    | [Telegram Bridge](telegram-bridge.md) — better for human-facing push  |

The email bridge polls on a configurable interval (default: 30 seconds via IMAP
IDLE). It's not real-time, but it's reliable, asynchronous, and works across any
network.

---

## Setup Guides

Choose the provider that matches your mailbox:

- [Gmail Setup](email-setup-gmail.md)
- [Fastmail Setup](email-setup-fastmail.md)
- [iCloud Mail Setup](email-setup-icloud.md)
- [Self-Hosted Postfix Setup](email-setup-postfix.md)

---

## Architecture

```text
Daemon A                          Daemon B
  │                                 │
  ├── SMTP → [shared mailbox] ←─────┤ SMTP
  │                                 │
  └── IMAP IDLE ←──────────────────── IMAP IDLE
```

- **Outbound:** the bridge's queue worker picks up enqueued messages and submits
  them via SMTP to the recipient daemon's registered address
- **Inbound:** the IMAP IDLE goroutine wakes on new mail, parses the
  `X-Thrum-From-Daemon` header to identify the sender, and routes the payload to
  the local daemon's message store

Both goroutines run inside the daemon process. No external sidecar required.

---

## Next Steps

After setting up a provider:

1. **Pair your daemons:** [Mesh Pairing](email-mesh-pairing.md)
2. **Understand the security model:** [Trust Model](email-trust-model.md)
3. **Diagnose problems:** [Troubleshooting](email-troubleshooting.md)
