
## Tailscale Sync

> **Prerequisites:** Tailscale installed and Thrum v0.4.0+ on all machines.
> Set `THRUM_TS_ENABLED=true` before starting the daemon.

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
2. **Thrum v0.4.0+** installed on all machines (Tailscale support added in
   v0.4.0)

## Getting Started

### 1. Enable Tailscale Sync

Set the environment variable to enable Tailscale integration:

```bash
export THRUM_TS_ENABLED=true
```

### 2. Start the Daemon

```bash
thrum daemon start
```

When Tailscale sync is enabled, the daemon:

- Starts a tsnet listener on port 9100 (configurable)
- Registers sync RPC handlers (`sync.pull`, `sync.notify`, `sync.peer_info`,
  `pair.request`)
- Waits for peer pairing via CLI

### 3. Pair Two Machines

Pairing requires action on both machines simultaneously:

**On Machine A** (the one you want to share with):

```bash
thrum peer add
# Output: Waiting for connection... Pairing code: 7392
```

**On Machine B** (the one joining):

```bash
thrum peer join my-laptop:9100
# Prompts: Enter pairing code:
# You type: 7392
# Output: Paired with "my-laptop". Syncing started.
```

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
| **Sync Scheduler**  | Periodic fallback sync (5-minute interval, skips recently synced peers)  |

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

A fallback mechanism that runs every 5 minutes. It pulls from all known peers
that were not synced recently (within the last 2 minutes). This ensures
convergence even if push notifications are lost.

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

| Variable               | Default            | Description                           |
| ---------------------- | ------------------ | ------------------------------------- |
| `THRUM_TS_ENABLED`     | `false`            | Enable Tailscale sync                 |
| `THRUM_TS_HOSTNAME`    | (auto)             | Hostname for the tsnet listener       |
| `THRUM_TS_PORT`        | `9100`             | Port for the sync RPC listener        |
| `THRUM_TS_AUTH_KEY`    | (none)             | Tailscale auth key for headless setup |
| `THRUM_TS_CONTROL_URL` | (default)          | Custom control server URL             |
| `THRUM_TS_STATE_DIR`   | `.thrum/var/tsnet` | tsnet state directory                 |

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
periodic_sync: starting with interval=5m0s, recent_threshold=2m0s
```

## Troubleshooting

### Cannot reach peer

1. Verify both machines are on the same Tailscale network
2. Check that both daemons are running (`thrum daemon start`)
3. Verify the address format is `hostname:port` (default port: 9100)
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

- **Use auth keys** (`THRUM_TS_AUTH_KEY`) for headless CI/CD runners
- **Keep the default port** (9100) unless you have a conflict
- **Use Tailscale ACLs** to restrict which machines can communicate

### Performance

- **Push notifications** handle most sync latency -- events typically propagate
  within seconds
- **Periodic sync** (5 min) acts as a safety net, not the primary mechanism
- **Batch size** of 1000 events per pull keeps memory bounded during large syncs
- **Checkpointing** ensures no redundant transfers after restarts

## Next Steps

- [Tailscale Security](tailscale-security.md) — the full security model:
  encryption layers, pairing codes, token authentication, and threat analysis
- [Sync Protocol](sync.md) — how Git-based sync works under the hood, for when
  Tailscale isn't available or you want async delivery
- [Multi-Agent Support](multi-agent.md) — coordinate agents across machines
  once sync is set up
- [Configuration](configuration.md) — configure sync interval, local-only mode,
  and other daemon settings
