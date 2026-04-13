## What a Peer Is

A peer is a link between two Thrum daemons. Each daemon is bound to one repo.
Peers let agents in different repos — or on different machines — message each
other directly, as if they were all registered with the same daemon.

You register `coordinator_main` in your main repo. Your colleague runs
`reviewer_api` in the API repo on a different machine. Without peers, those two
agents can't talk. With a peer, you send:

```bash
thrum send "Auth module ready for review" --to @api:reviewer_api
```

The daemon routes it through the peer connection. The reviewer gets it in their
inbox. No relay, no manual handoff.

Peers are bidirectional and persistent. Pair once; the connection re-establishes
on daemon restarts automatically.

---

## The Two Transports

Peers use two network transports. The peer model is the same for both — you pair
once, messages route transparently. What differs is how the daemons find each
other.

### Local (same machine, different repos)

Both daemons run on the same machine. You're working across two repos in the
same worktree setup: `main-repo` and `mock-salesforce`, for example. Each has
its own daemon.

For local peers, the daemon reads the port file from the other repo's filesystem
(`.thrum/var/ws.port`). There's no network exposure — traffic stays on
`127.0.0.1`. Firewall rules and NAT don't apply.

Use this when you're building or testing a multi-repo system on one laptop.

### Tailscale (different machines)

The two daemons are on separate machines connected via
[Tailscale](https://tailscale.com). Traffic flows over Tailscale's encrypted
WireGuard tunnels on `100.x.x.x` addresses.

Use this when your agents are spread across laptops, servers, or CI runners.
Tailscale handles the networking; you just provide a Tailscale auth key and
pair.

See [Tailscale Sync](tailscale-sync.md) for Tailscale setup and
[Tailscale Security](tailscale-security.md) for the full security model.

### Comparison

| Transport | Network layer | Use when                                             |
| --------- | ------------- | ---------------------------------------------------- |
| Local     | `127.0.0.1`   | Both repos are on the same machine                   |
| Tailscale | `100.x.x.x`   | Daemons are on separate Tailscale-connected machines |
| Network   | Direct TCP/WS | Same LAN, no Tailscale (trusted networks only)       |

---

## The Pairing Flow

Pairing happens once. After that, peers reconnect automatically.

**On the first machine** (the listener):

```bash
thrum peer add
```

The daemon generates a 16-digit numeric pairing code and blocks waiting for the
other side. It prints the full peercode:

```text
Waiting for connection...

Share this with the other machine:
  thrum peer join --peercode alice:100.64.1.5:44123:4829175036284719

Paired with "bob" (100.64.1.9:44123). Syncing started.
```

**On the second machine** (the joiner):

```bash
thrum peer join --peercode alice:100.64.1.5:44123:4829175036284719
```

For a local peer on the same machine, add `--repo-path`:

```bash
thrum peer join --peercode alice:127.0.0.1:44123:4829175036284719 \
  --repo-path /path/to/other/repo
```

The `--repo-path` flag tells the daemon where to find the other repo's port
file. It's only needed for local peers — the joining side has filesystem access
and can read the port directly.

### Pairing Code Details

The peercode format is `name:ip:port:code`. Example:

```text
mock-salesforce:127.0.0.1:9342:4829175036284719
```

The 16-digit numeric code is short-lived — it expires after 5 minutes. It's a
handshake secret, not a long-term credential. During pairing, both sides
exchange a 32-byte auth token that gets stored in `peers.json` and used for all
future connections.

If the pairing code expires, run `thrum peer add` again to generate a new one.

---

## Listener vs Dialer

The "adder" side (the one that ran `thrum peer add`) is always the **listener**.
The "joiner" side is always the **dialer**.

The dialer initiates every connection — on first pair and on every reconnect
after a restart. The listener waits for incoming connections and accepts them.

This matters for firewalls and NAT:

- The listener needs an open port (or needs to be on Tailscale where port
  forwarding isn't required).
- The dialer just needs outbound connectivity to the listener's address.
- If the listener restarts, the dialer detects the broken connection and
  reconnects with backoff.
- If the dialer restarts, it re-dials on startup.

The roles are fixed from the first pairing. They're stored in `peers.json` as
`role: "listener"` or `role: "dialer"`.

Once connected, the WebSocket connection is bidirectional — both sides send RPC
requests and receive notifications over the same connection.

---

## Address Change Handling

Laptops change networks. DHCP assigns new IPs. Daemons restart on different
ports.

When a daemon detects its own address has changed, it notifies each connected
peer automatically:

1. It connects to each peer at the peer's last-known address.
2. It sends `peer.address_changed` with the new IP, new port, and the shared
   auth token.
3. The peer verifies the token, validates the new address, and updates its
   registry.
4. The connection continues on the new address. No re-pairing needed.

Address changes are constrained by transport type to prevent redirect attacks:

| Transport | Allowed changes                                  |
| --------- | ------------------------------------------------ |
| Local     | Port only — IP must stay on `127.0.0.1`          |
| Tailscale | Port only — IP must stay in `100.64.0.0/10`      |
| Network   | Same `/24` subnet as the original paired address |

If both peers change address at the same time — both laptops moved to a new
network — automatic reconnection isn't possible. In that case, re-pair with
`thrum peer add` and `thrum peer join`. It takes 30 seconds. This scenario is
rare.

If a peer is temporarily unreachable when your address changes, the notification
is queued and retried with backoff. When the peer comes back online, it'll catch
up.

See [RPC API](rpc-api.md#peer.address_changed) for the `peer.address_changed`
method signature.

---

## Messaging Across Peers

### Proxy Agents

Remote agents don't appear in your daemon automatically. You register them
explicitly as proxy agents using `thrum peer configure`:

```bash
thrum peer configure mock-salesforce add-agent coordinator_main
```

This registers `mock-sf:coordinator_main` in your local daemon. You can then
send to it by name:

```bash
thrum send "Request complete" --to @mock-sf:coordinator_main
```

The daemon sees `mock-sf:` as the proxy prefix, routes the message through the
peer connection, strips the prefix, and delivers it to `coordinator_main` on the
remote daemon.

Proxy agents appear in `thrum team` like any other agent:

```text
NAME                     ROLE           STATUS
coordinator_main         coordinator    active
mock-sf:coordinator_main coordinator    active (via peer)
```

### Name Prefixing

Cross-repo agents use the format `prefix:name`. The prefix is the `proxy_prefix`
stored in `peers.json` — set during pairing, typically a short slug of the peer
name (`mock-sf`, `api`, `infra`).

The full routing path:

```text
@mock-sf:coordinator_main
  → local daemon recognizes "mock-sf" as peer prefix
  → relays to mock-salesforce peer's transport bridge
  → remote daemon strips prefix, delivers to "coordinator_main"
```

Reply threading works across the connection. The `MessageMap` tracks local and
remote message IDs so replies stay in the same thread.

### `thrum send` and `thrum team`

`thrum send --to @prefix:name` works the same as local delivery from the
sender's perspective. Inbox, threading, and read state all work normally.

`thrum team` includes proxy agents from all connected peers. `thrum who-has`
resolves cross-repo agents.

See [Messaging](messaging.md) for full send/receive/reply documentation.

---

## Configuration

### config.json

```json
{
  "daemon": {
    "peer_port": "auto"
  },
  "peers": {
    "auto_connect": true,
    "pairing_code_length": 16
  }
}
```

| Key                         | Default | Description                                                |
| --------------------------- | ------- | ---------------------------------------------------------- |
| `daemon.peer_port`          | `auto`  | Port the peer listener binds to. `auto` picks a free port. |
| `peers.auto_connect`        | `true`  | Reconnect to all known peers on daemon startup             |
| `peers.pairing_code_length` | `16`    | Length of the numeric pairing code                         |

### peers.json

The peer registry is managed automatically by the pairing flow. It's stored at
`.thrum/var/peers.json`. You don't edit it directly, but you can read it:

```json
{
  "peers": [
    {
      "name": "mock-salesforce",
      "address": "127.0.0.1:9342",
      "repo_path": "/Users/leon/dev/falcondev/mock-salesforce",
      "token": "...",
      "transport": "local",
      "proxy_prefix": "mock-sf",
      "remote_agents": ["coordinator_main"],
      "paired_at": "2026-04-05T20:00:00Z"
    }
  ]
}
```

The `token` field is the long-lived auth token exchanged during pairing. Guard
this file — it grants access to the peer connection. Don't commit it to git.

See [Configuration](configuration.md) for the full config reference.

---

## Commands

All peer commands live under `thrum peer`. Full reference:
[CLI](cli.md#peer-management).

### Quick Reference

| Command                                    | Description                                              |
| ------------------------------------------ | -------------------------------------------------------- |
| `thrum peer add`                           | Start a pairing session, display peercode, wait for join |
| `thrum peer join --peercode <code>`        | Join a peer using the peercode from `peer add`           |
| `thrum peer list`                          | List all paired peers with address and last sync time    |
| `thrum peer status`                        | Detailed per-peer health, auth status, and pairing time  |
| `thrum peer remove <name>`                 | Remove a peer, stop syncing immediately                  |
| `thrum peer configure <name> add-agent`    | Register a remote agent as a proxy locally               |
| `thrum peer configure <name> remove-agent` | Unregister a proxy agent                                 |

### `thrum peer add`

Blocks for up to 5 minutes waiting for the other side. Prints the full peercode
to share:

```text
$ thrum peer add
Waiting for connection...

Share this with the other machine:
  thrum peer join --peercode alice:100.64.1.5:44123:7392031846291057

Paired with "bob" (100.64.1.9:44123). Syncing started.
```

### `thrum peer join`

Pass the peercode as a flag, a positional argument, or pipe it from stdin:

```bash
# Flag
thrum peer join --peercode alice:100.64.1.5:44123:7392031846291057

# Positional
thrum peer join alice:100.64.1.5:44123:7392031846291057

# Stdin
echo "alice:100.64.1.5:44123:7392031846291057" | thrum peer join --peercode -

# Local peer (same machine)
thrum peer join --peercode alice:127.0.0.1:9342:7392031846291057 \
  --repo-path /path/to/alice-repo
```

### `thrum peer list`

```text
$ thrum peer list
NAME                 ADDRESS                LAST SYNC          LAST SEQ
mock-salesforce      127.0.0.1:9342         5 seconds ago      482
alice                100.64.1.5:44123       2 minutes ago      1042
```

### `thrum peer status`

More detail than `list` — includes auth token status, pairing timestamp, and
sequence numbers. Use `--json` for scripting.

### `thrum peer configure`

Add or remove proxy agents for a peer. Changes take effect immediately if the
peer is connected — no daemon restart needed:

```bash
# Register a remote agent as a proxy
thrum peer configure mock-salesforce add-agent coordinator_main

# Remove it
thrum peer configure mock-salesforce remove-agent coordinator_main
```

---

## Troubleshooting

### Pairing fails or times out

The pairing code expires after 5 minutes. Run `thrum peer add` again to generate
a new one. Make sure both machines can reach each other — for Tailscale peers,
use the `100.x.x.x` IP shown by `tailscale status`, not the hostname.

### Messages not reaching the remote agent

1. Check `thrum peer status` — is the peer connected? Does it have a token?
2. Verify the proxy agent is registered: `thrum team` should show `prefix:name`
   entries.
3. If the proxy agent is missing, run
   `thrum peer configure <name> add-agent <agent>` again.
4. Check that `auto_connect: true` is set in config if the peer disconnected
   after a restart.

### Address change not detected

If you moved networks and the peer connection dropped, the daemon attempts to
notify peers of the new address using the last-known address. If both machines
moved simultaneously, this fails — re-pair with `thrum peer add` and
`thrum peer join`.

### `thrum peer status` output explained

```text
NAME             ADDRESS              HAS_TOKEN  PAIRED_AT               LAST_SYNC
mock-salesforce  127.0.0.1:9342       true       2026-04-05T20:00:00Z    5s ago
alice            100.64.1.5:44123     true       2026-03-01T12:00:00Z    2m ago
```

- `HAS_TOKEN: false` means the token is missing from `peers.json` — re-pair.
- `LAST_SYNC` stale by more than a few minutes — check daemon logs for
  connection errors.

---

## How It Differs from Sync

Peers and sync are complementary. They serve different purposes.

**Peers** are live message transport. When you send a message to a proxy agent,
it's relayed in real time over the WebSocket connection to the remote daemon.
Peers are for agent-to-agent coordination — directed messages, replies, and
broadcast (`@everyone`) messaging across repos.

**Sync** is Git-backed state replication. The `a-sync` branch replicates JSONL
event logs between machines so each daemon has a full copy of the message
history, agent registry, and session state. Sync is eventually consistent and
works even when daemons are offline — changes merge when both sides reconnect.

| Capability            | Peers               | Sync                      |
| --------------------- | ------------------- | ------------------------- |
| Message delivery      | Live, directed      | Replicated, eventually    |
| Works while offline   | No                  | Yes (merges on reconnect) |
| Requires connection   | Yes                 | No (Git push/pull)        |
| State replication     | No                  | Yes (full event log)      |
| Agent addressing      | Yes (`prefix:name`) | No                        |
| Cross-machine history | No                  | Yes                       |

You can run both together. A common setup: Tailscale sync keeps the event logs
replicated across machines, while peers handle live directed messaging between
agents on those machines.

See [Sync Protocol](sync.md) for how Git-based sync works and
[Tailscale Sync](tailscale-sync.md) for cross-machine sync via Tailscale.

---

## Next Steps

- [Messaging](messaging.md) — full reference for `thrum send`, inbox, replies,
  groups, and mention routing
- [CLI Reference](cli.md#peer-management) — complete flag reference for all
  `thrum peer` commands
- [RPC API](rpc-api.md#peer-methods-v070) — `peer.*` JSON-RPC methods for custom
  integrations
- [Configuration](configuration.md) — full config reference including the
  `peers` block
- [Architecture](architecture.md#cross-repo-peer-system-v070) — internals:
  PeerManager, PeerBridge, PeerTransport, relay logic
- [Tailscale Sync](tailscale-sync.md) — cross-machine event log replication via
  Tailscale
- [Tailscale Security](tailscale-security.md) — encryption layers, token auth,
  and threat model
- [Sync Protocol](sync.md) — Git-backed async state replication
