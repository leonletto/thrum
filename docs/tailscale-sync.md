
## Tailscale Sync

> **Prerequisites:** Tailscale installed and Thrum v0.5.8+ on all machines.

## Overview

Thrum's Tailscale sync enables real-time event synchronization between daemon
instances running on different machines connected via a
[Tailscale](https://tailscale.com) network. Agents on separate laptops, VMs, or
CI runners can coordinate as if they were on the same machine -- messages, agent
events, and session updates propagate automatically.

**Key capabilities:**

- **Cross-machine sync** -- Events flow between daemons over Tailscale's
  encrypted WireGuard tunnels
- **Push + pull** -- Immediate push notifications on new events, with periodic
  pull as a fallback
- **Human-mediated pairing** -- Simple 4-digit code to pair two machines (no
  auto-discovery, no complex key management)
- **Token authentication** -- Each peer pair shares a unique 32-byte token for
  ongoing auth
- **Zero config networking** -- No port forwarding, no firewall rules. Tailscale
  handles connectivity

## Prerequisites

1. **Tailscale installed** on all machines running Thrum daemons
2. **Thrum v0.5.8+** installed on all machines

## Getting Started

### 1. Configure Tailscale Sync

Create a `.env` file in your repo root with the required variables:

```bash
# .env (add to .gitignore — contains auth keys)
THRUM_TS_ENABLED=true
THRUM_TS_HOSTNAME=my-laptop
THRUM_TS_AUTHKEY=tskey-auth-xxxxx
```

The daemon auto-loads `.env` from the repo root (or `.thrum/.env`). Variables
with `THRUM_` or `TAILSCALE_` prefixes are loaded; existing environment
variables take precedence.

**Important:** `THRUM_TS_HOSTNAME` and `THRUM_TS_AUTHKEY` are both required. The
hostname identifies your machine on the Tailscale network. Get an auth key from
the [Tailscale admin console](https://login.tailscale.com/admin/settings/keys).

### 2. Start the Daemon

```bash
thrum daemon restart
```

When Tailscale sync is enabled, the daemon:

- Starts a tsnet listener on port 9100 (configurable)
- Registers sync RPC handlers (`sync.pull`, `sync.notify`, `sync.peer_info`,
  `pair.request`)
- Runs periodic sync every 15 seconds (with immediate sync on startup)
- Waits for peer pairing via CLI

**Note:** tsnet creates a separate Tailscale identity with a `-1` suffix (e.g.,
`my-laptop-1`). This is normal — it's a userspace Tailscale instance running
alongside your system Tailscale.

### 3. Pair Two Machines

Pairing requires action on both machines simultaneously. `thrum peer add` blocks
for up to 5 minutes waiting for the other machine to connect — coordinate timing
with the other operator.

**On Machine A** (the one you want to share with):

```bash
thrum peer add
# Output: Waiting for connection... Pairing code: 7392
# (blocks up to 5 minutes)
```

**On Machine B** (the one joining):

```bash
thrum peer join <tailscale-ip>:9100
# Prompts: Enter pairing code:
# You type: 7392
# Output: Paired with "my-laptop". Syncing started.
```

**Important:** Use the tsnet Tailscale IP address (e.g., `100.x.y.z:9100`), not
the hostname. Regular DNS cannot resolve tsnet hostnames (the `-1` suffix
variants). Find the IP with `tailscale status` — look for the entry with the
`-1` suffix matching the other machine's `THRUM_TS_HOSTNAME`.

Machine A will also show success:

```text
Paired with "office-server" (100.64.2.10:9100). Syncing started.
```

Both machines now sync events automatically.

### 4. Verify Sync

```bash
# List paired peers
thrum peer list

# Detailed sync status
thrum peer status

# Check health endpoint
thrum status
```

## Architecture

```text
Machine A                           Machine B
┌─────────────────────┐             ┌─────────────────────┐
│  Thrum Daemon       │             │  Thrum Daemon       │
│  ├─ Event Log       │             │  ├─ Event Log       │
│  ├─ tsnet Listener  │◄──────────►│  ├─ tsnet Listener  │
│  ├─ Sync Manager    │  Tailscale  │  ├─ Sync Manager    │
│  └─ Peer Registry   │  (WireGuard)│  └─ Peer Registry   │
└─────────────────────┘             └─────────────────────┘
         │                                    │
    ┌────┴────┐                          ┌────┴────┐
    │ Agents  │                          │ Agents  │
    │ CLI/MCP │                          │ CLI/MCP │
    └─────────┘                          └─────────┘
```

### Component Overview

| Component           | Purpose                                                                  |
| ------------------- | ------------------------------------------------------------------------ |
| **Event Log**       | Sequenced event store with origin tracking and dedup                     |
| **tsnet Listener**  | Tailscale-native TCP listener (no port forwarding needed)                |
| **Sync Manager**    | Orchestrates pull sync, push notifications, and the scheduler            |
| **Sync Client**     | Pulls events from peers in batches with checkpointing                    |
| **Sync Server**     | Exposes `sync.*` and `pair.*` RPC methods to peers (token-authenticated) |
| **Peer Registry**   | Thread-safe registry of paired peers with JSON persistence               |
| **Pairing Manager** | Handles the 4-digit code pairing flow                                    |
| **Sync Scheduler**  | Periodic sync every 15s for Tailscale peers (skips recently synced)      |

## Sync Protocol

### Event Log Foundation

Every event written to the daemon includes:

- **`origin_daemon`** -- Unique daemon ID identifying the source machine
- **`sequence`** -- Monotonically increasing per-daemon sequence number

Events are stored in a SQLite `events` table with sequence-based pagination,
enabling efficient delta sync.

### Pull Sync

The primary sync mechanism. Daemon A asks Daemon B: "Give me all events after
sequence N."

```text
Daemon A                              Daemon B
   │                                      │
   │ sync.pull(after_seq=42, token=...)   │
   ├─────────────────────────────────────►│
   │                                      │
   │  {events: [...], next_seq: 1042,     │
   │   more_available: true}              │
   │◄─────────────────────────────────────┤
   │                                      │
   │ sync.pull(after_seq=1042, token=...) │
   ├─────────────────────────────────────►│
   │                                      │
   │  {events: [...], next_seq: 1500,     │
   │   more_available: false}             │
   │◄─────────────────────────────────────┤
```

Batched pull with the `limit+1` trick to determine `more_available`. Checkpoints
are persisted per-peer so sync resumes from where it left off. All requests
include the peer's auth token.

### Push Notifications

When a daemon writes a new event, it broadcasts a `sync.notify` to all known
peers:

```text
Daemon A writes event
   │
   ├──► sync.notify(daemon_id, latest_seq, token) ──► Daemon B
   ├──► sync.notify(daemon_id, latest_seq, token) ──► Daemon C
   │
   Daemons B and C pull new events from A
```

Push notifications are fire-and-forget -- failures are logged but do not block
the writer.

### Periodic Sync Scheduler

For Tailscale peers, the scheduler runs every 15 seconds with a 10-second
recent-sync threshold. It pulls from all known peers that were not synced
recently. Combined with push notifications, this provides near-real-time sync
(typically under 20 seconds end-to-end). An initial sync runs immediately on
daemon startup.

For Git-only sync (no Tailscale), the scheduler uses the default 5-minute
interval with a 2-minute threshold.

### Deduplication

Events are deduplicated by `event_id` (ULID-based, globally unique). The
`HasEvent()` function provides O(1) dedup via the SQLite primary key index.
Duplicate events from overlapping syncs are silently skipped.

## Pairing Flow

Pairing establishes mutual trust between two machines with a human in the loop.

```text
Machine A (thrum peer add)           Machine B (thrum peer join)
   │                                      │
   │  1. Generate 4-digit code + token    │
   │  2. Display code to user             │
   │                                      │
   │        (human shares code)           │
   │                                      │
   │  pair.request(code, id, name, addr)  │
   │◄─────────────────────────────────────┤ 3. User enters code
   │                                      │
   │  4. Verify code                      │
   │  5. Store peer B + token             │
   │                                      │
   │  {status: paired, token, id, name}   │
   ├─────────────────────────────────────►│ 6. Store peer A + token
   │                                      │
   │  Both peers now authenticate with    │
   │  the shared token on every request   │
```

- The pairing code is a random 4-digit number (3 attempts allowed)
- The token is a random 32-byte hex string
- Pairing sessions expire after 5 minutes
- Both peers store each other's info in `peers.json`

## Configuration

### Environment Variables

| Variable               | Default            | Description                             |
| ---------------------- | ------------------ | --------------------------------------- |
| `THRUM_TS_ENABLED`     | `false`            | Enable Tailscale sync                   |
| `THRUM_TS_HOSTNAME`    | (required)         | Hostname for the tsnet listener         |
| `THRUM_TS_PORT`        | `9100`             | Port for the sync RPC listener          |
| `THRUM_TS_AUTHKEY`     | (required)         | Tailscale auth key (from admin console) |
| `THRUM_TS_CONTROL_URL` | (default)          | Custom control server URL (Headscale)   |
| `THRUM_TS_STATE_DIR`   | `.thrum/var/tsnet` | tsnet state directory                   |

These can be set via environment variables or in a `.env` file at the repo root.
The `.env` file is auto-loaded by the daemon — only `THRUM_*` and `TAILSCALE_*`
prefixed variables are read. **Add `.env` to `.gitignore`** since it contains
your auth key.

## CLI Commands

### `thrum peer`

Manage sync peers:

```bash
# Start pairing on this machine (displays 4-digit code)
thrum peer add

# Join a remote peer (prompts for pairing code)
thrum peer join <address:port>

# List all paired peers
thrum peer list

# Remove a peer
thrum peer remove <name>

# Detailed sync status for all peers
thrum peer status
```

### `thrum status`

When Tailscale sync is enabled, `thrum status` includes sync information:

```text
Tailscale Sync: enabled
  Peers: 2 connected
  Last sync: 30s ago
  Hostname: my-laptop
```

## Security Model

Tailscale sync uses a simple three-layer security model:

### 1. Tailscale Encryption (Network Layer)

All traffic between daemons flows over Tailscale's WireGuard tunnels. This
provides end-to-end encryption and identity verification at the network level.
No data travels over the public internet unencrypted.

### 2. Pairing Code (Trust Establishment)

A human-mediated 4-digit code establishes initial trust between two machines.
The pairing code must be shared out-of-band (verbally, chat, etc.), ensuring
both sides consent to the peering relationship.

- 4-digit random code (10,000 possibilities)
- 3 attempts allowed before the session is locked
- 5-minute timeout on pairing sessions

### 3. Token Authentication (Ongoing Auth)

After pairing, each request includes a 32-byte hex token. The receiving daemon
validates the token against its peer registry before processing any sync
request. The `pair.request` method is the only RPC exempt from token
authentication (it's how new peers establish their tokens).

- Token validation is centralized in the sync server
- Invalid or missing tokens are rejected immediately
- Peer's `last_sync` is updated on each successful authenticated request

## Peer Management

### Peer Registry

The peer registry is stored as JSON at `.thrum/var/peers.json` and persists
across daemon restarts. It tracks:

- Daemon ID and name
- Network address (Tailscale IP + port)
- Auth token
- Paired-at timestamp and last sync time

## Monitoring

### Health Endpoint

The daemon's `health` RPC method includes Tailscale sync status when enabled:

```json
{
  "tailscale_sync": {
    "enabled": true,
    "hostname": "my-laptop",
    "peer_count": 2,
    "peers": [
      {
        "daemon_id": "d_abc123",
        "name": "office-server",
        "last_sync": "30s ago"
      }
    ]
  }
}
```

### Logs

Tailscale sync logs are prefixed for easy filtering:

```text
[pairing] Session started, code=7392, timeout=5m0s
[pairing] Paired with office-server (d_abc123) at 100.64.2.10:9100
sync.notify: synced from d_abc123 — applied=5 skipped=0
periodic_sync: starting with interval=15s, recent_threshold=10s
```

## Troubleshooting

### Cannot reach peer

1. Verify both machines are on the same Tailscale network
2. Check that both daemons are running (`thrum daemon start`)
3. Use the Tailscale IP (not hostname) for `peer join`: `tailscale status` to
   find it
4. Test connectivity: `tailscale ping <hostname>`

### Pairing code rejected

- Ensure you're entering the code displayed on the other machine
- Codes expire after 5 minutes -- run `thrum peer add` again if expired
- After 3 failed attempts, the session locks -- restart with `thrum peer add`

### Sync not working after pairing

1. Check `thrum peer status` for connection details
2. Verify both daemons have Tailscale enabled (`THRUM_TS_ENABLED=true`)
3. Check daemon logs for sync errors

## Best Practices

### Network Setup

- **Use auth keys** (`THRUM_TS_AUTHKEY`) for headless CI/CD runners
- **Keep the default port** (9100) unless you have a conflict
- **Use Tailscale ACLs** to restrict which machines can communicate

### Performance

- **Push notifications** trigger immediate pulls when events are written
- **Periodic sync** (15s for Tailscale peers) ensures convergence even if push
  notifications are lost
- **Typical end-to-end latency** is under 20 seconds across machines
- **Batch size** of 1000 events per pull keeps memory bounded during large syncs
- **Checkpointing** ensures no redundant transfers after restarts

## Next Steps

- [Tailscale Security](tailscale-security.md) — the full security model:
  encryption layers, pairing codes, token authentication, and threat analysis
- [Sync Protocol](sync.md) — how Git-based sync works under the hood, for when
  Tailscale isn't available or you want async delivery
- [Multi-Agent Support](multi-agent.md) — coordinate agents across machines once
  sync is set up
- [Configuration](configuration.md) — configure sync interval, local-only mode,
  and other daemon settings
