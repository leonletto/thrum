
# Tailscale Sync

> See also: [Tailscale Security](tailscale-security.md) for the full security
> model, [Multi-Agent Support](multi-agent.md) for team coordination patterns,
> [Sync Protocol](sync.md) for Git-based synchronization.

## Overview

Thrum's Tailscale sync enables real-time event synchronization between daemon
instances running on different machines connected via a
[Tailscale](https://tailscale.com) network. Agents on separate laptops, VMs, or
CI runners can coordinate as if they were on the same machine -- messages,
agent events, and session updates propagate automatically.

**Key capabilities:**

- **Cross-machine sync** -- Events flow between daemons over Tailscale's
  encrypted WireGuard tunnels
- **Push + pull** -- Immediate push notifications on new events, with periodic
  pull as a fallback
- **Peer discovery** -- Automatic discovery of other Thrum daemons on your
  tailnet via the Tailscale API
- **Cryptographic security** -- Ed25519 event signing, WhoIs-based peer
  authorization, per-peer rate limiting, and a quarantine system for invalid
  events
- **Zero config networking** -- No port forwarding, no firewall rules. Tailscale
  handles connectivity

## Prerequisites

1. **Tailscale installed** on all machines running Thrum daemons
2. **ACL tags configured** -- Thrum daemons should be tagged with `tag:thrum-daemon`
   in your Tailscale ACL policy
3. **Thrum v0.3.2+** installed on all machines

## Getting Started

### 1. Enable Tailscale Sync

Set the environment variable to enable Tailscale integration:

```bash
export THRUM_TAILSCALE_ENABLED=true
```

### 2. Start the Daemon

```bash
thrum daemon start
```

When Tailscale sync is enabled, the daemon:

- Starts a tsnet listener on port 4200 (configurable)
- Generates Ed25519 identity keys (stored at `.thrum/var/identity.key`)
- Begins peer discovery on the tailnet
- Registers sync RPC handlers (`sync.pull`, `sync.notify`, `sync.peer_info`)

### 3. Add Peers

Peers are discovered automatically if tagged with `tag:thrum-daemon`, or you can
add them manually:

```bash
# List discovered peers
thrum tsync peers list

# Manually add a peer
thrum tsync peers add my-laptop:4200

# Force an immediate sync
thrum tsync force
```

### 4. Verify Sync

```bash
# Check sync health and peer status
thrum status
# Shows Tailscale sync status, peer count, and last sync times
```

## Architecture

```
Machine A                           Machine B
┌─────────────────────┐             ┌─────────────────────┐
│  Thrum Daemon       │             │  Thrum Daemon       │
│  ├─ Event Log       │             │  ├─ Event Log       │
│  ├─ tsnet Listener  │◄──────────►│  ├─ tsnet Listener  │
│  ├─ Sync Manager    │  Tailscale  │  ├─ Sync Manager    │
│  ├─ Peer Registry   │  (WireGuard)│  ├─ Peer Registry   │
│  └─ Security Layer  │             │  └─ Security Layer  │
└─────────────────────┘             └─────────────────────┘
         │                                    │
    ┌────┴────┐                          ┌────┴────┐
    │ Agents  │                          │ Agents  │
    │ CLI/MCP │                          │ CLI/MCP │
    └─────────┘                          └─────────┘
```

### Component Overview

| Component | Purpose |
|-----------|---------|
| **Event Log** | Sequenced event store with origin tracking and dedup |
| **tsnet Listener** | Tailscale-native TCP listener (no port forwarding needed) |
| **Sync Manager** | Orchestrates pull sync, push notifications, and the scheduler |
| **Sync Client** | Pulls events from peers in batches with checkpointing |
| **Sync Server** | Exposes `sync.*` RPC methods to peers (security-bounded) |
| **Peer Registry** | Thread-safe registry of known peers with JSON persistence |
| **Peer Discovery** | Auto-discovers peers via the Tailscale API (`tag:thrum-daemon`) |
| **Sync Scheduler** | Periodic fallback sync (5-minute interval, skips recently synced peers) |
| **Security Layer** | Ed25519 signing, validation pipeline, WhoIs auth, rate limiting |

## Sync Protocol

### Event Log Foundation

Every event written to the daemon includes:

- **`origin_daemon`** -- Unique daemon ID identifying the source machine
- **`sequence`** -- Monotonically increasing per-daemon sequence number
- **`signature`** -- Ed25519 signature over
  `event_id|type|timestamp|origin_daemon`

Events are stored in a SQLite `events` table with sequence-based pagination,
enabling efficient delta sync.

### Pull Sync

The primary sync mechanism. Daemon A asks Daemon B: "Give me all events after
sequence N."

```
Daemon A                              Daemon B
   │                                      │
   │ sync.pull(after_seq=42, limit=1000)  │
   ├─────────────────────────────────────►│
   │                                      │
   │  {events: [...], next_seq: 1042,     │
   │   more_available: true}              │
   │◄─────────────────────────────────────┤
   │                                      │
   │ sync.pull(after_seq=1042, limit=1000)│
   ├─────────────────────────────────────►│
   │                                      │
   │  {events: [...], next_seq: 1500,     │
   │   more_available: false}             │
   │◄─────────────────────────────────────┤
```

Batched pull with the `limit+1` trick to determine `more_available`. Checkpoints
are persisted per-peer so sync resumes from where it left off.

### Push Notifications

When a daemon writes a new event, it broadcasts a `sync.notify` to all known
peers:

```
Daemon A writes event
   │
   ├──► sync.notify(daemon_id, latest_seq, event_count) ──► Daemon B
   ├──► sync.notify(daemon_id, latest_seq, event_count) ──► Daemon C
   │
   Daemons B and C pull new events from A
```

Push notifications include per-peer debouncing to avoid notification storms.
They are fire-and-forget -- failures are logged but do not block the writer.

### Periodic Sync Scheduler

A fallback mechanism that runs every 5 minutes. It pulls from all known peers
that were not synced recently (within the last 2 minutes). This ensures
convergence even if push notifications are lost.

### Deduplication

Events are deduplicated by `event_id` (ULID-based, globally unique). The
`HasEvent()` function provides O(1) dedup via the SQLite primary key index.
Duplicate events from overlapping syncs are silently skipped.

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `THRUM_TAILSCALE_ENABLED` | `false` | Enable Tailscale sync |
| `THRUM_TAILSCALE_HOSTNAME` | (auto) | Hostname for the tsnet listener |
| `THRUM_TAILSCALE_PORT` | `4200` | Port for the sync RPC listener |
| `THRUM_TAILSCALE_AUTH_KEY` | (none) | Tailscale auth key for headless setup |
| `THRUM_TAILSCALE_CONTROL_URL` | (default) | Custom control server URL |
| `THRUM_TAILSCALE_STATE_DIR` | `.thrum/var/tsnet` | tsnet state directory |

### Security Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `THRUM_SECURITY_REQUIRE_SIGNATURES` | `false` | Reject unsigned events |
| `THRUM_SECURITY_ALLOWED_PEERS` | (all) | Comma-separated list of allowed peer hostnames |
| `THRUM_SECURITY_REQUIRED_TAGS` | `tag:thrum-daemon` | Required Tailscale ACL tags |
| `THRUM_SECURITY_ALLOWED_DOMAINS` | (all) | Allowed Tailscale login domains |
| `THRUM_SECURITY_RATE_LIMIT_ENABLED` | `false` | Enable per-peer rate limiting |
| `THRUM_SECURITY_MAX_REQUESTS_PER_SEC` | `10` | Max sync requests per second per peer |
| `THRUM_SECURITY_BURST_SIZE` | `20` | Rate limiter burst size |

## CLI Commands

### `thrum tsync`

Manage Tailscale sync operations:

```bash
# Force an immediate sync with all peers
thrum tsync force

# List known peers and their sync status
thrum tsync peers list

# Manually add a peer by address
thrum tsync peers add <hostname:port>
```

### `thrum status`

When Tailscale sync is enabled, `thrum status` includes sync information:

```
Tailscale Sync: enabled
  Peers: 3 connected
  Last sync: 30s ago
  Hostname: my-laptop
```

## Security Model

Tailscale sync employs defense in depth with four layers:

### 1. Ed25519 Event Signing

Every daemon generates an Ed25519 key pair on first start. All events are signed
with a canonical payload: `event_id|type|timestamp|origin_daemon`. Signatures
are base64-encoded and stored in the event's `signature` field.

```bash
# Key location
.thrum/var/identity.key   # PEM-encoded Ed25519 private key (0600 perms)

# Fingerprint logged on startup
identity: generated new keys at .thrum/var/identity.key
  (fingerprint: SHA256:e1pqx9idRwTP4Uvda7vwKnrnM5Kie+HWCGozTmXkpyU=)
```

### 2. Validation Pipeline

Incoming sync events pass through a three-stage validation pipeline:

| Stage | Checks | Action on Failure |
|-------|--------|-------------------|
| **Schema** | Required fields present, valid types | Reject + quarantine |
| **Signature** | Ed25519 signature valid (if present) | Reject + quarantine |
| **Business Logic** | Timestamp within 24h, origin_daemon set | Reject + quarantine |

### 3. WhoIs Authorization

When a peer connects, the daemon uses the Tailscale WhoIs API to verify the
peer's identity. Authorization checks (in order):

1. **Allowed peers** -- Is the hostname in the allowed list?
2. **Required tags** -- Does the peer have `tag:thrum-daemon`?
3. **Allowed domains** -- Is the login from an allowed domain?

If any configured check fails, the connection is rejected with a detailed log.

### 4. Rate Limiting and Quarantine

- **Per-peer rate limiting** -- Token bucket algorithm (default 10 req/s, burst
  20). Returns 429 when exceeded, 503 when the global queue is full
- **Quarantine system** -- Invalid events are stored in a `quarantined_events`
  SQLite table with the rejection reason. An alert fires when a peer exceeds
  10 quarantined events per hour

For detailed security documentation, see
[Tailscale Security](tailscale-security.md).

## Peer Management

### Automatic Discovery

When Tailscale sync is enabled, the daemon queries the Tailscale API for peers
tagged with `tag:thrum-daemon`. Discovered peers are added to the peer registry
automatically.

### Manual Peer Management

```bash
# Add a peer explicitly
thrum tsync peers add workstation.tailnet:4200

# List all known peers with sync status
thrum tsync peers list
# PEER             DAEMON_ID        LAST_SYNC    STATUS
# my-laptop:4200   d_abc123         30s ago      idle
# ci-runner:4200   d_def456         2m ago       idle
```

### Peer Registry

The peer registry is stored as JSON at `.thrum/var/peers.json` and persists
across daemon restarts. It tracks:

- Daemon ID and hostname
- Last sync time and status
- Tailscale IP address

## Monitoring

### Health Endpoint

The daemon's `health` RPC method includes Tailscale sync status when enabled:

```json
{
  "tailscale_sync": {
    "enabled": true,
    "hostname": "my-laptop",
    "peer_count": 3,
    "peers": [
      {
        "daemon_id": "d_abc123",
        "hostname": "workstation",
        "last_sync": "2026-02-11T15:30:00Z",
        "status": "idle"
      }
    ]
  }
}
```

### Logs

Tailscale sync logs are prefixed for easy filtering:

```
[sync_auth] ALLOWED peer workstation (login: alice@company.com, tags: [tag:thrum-daemon])
[quarantine] event evt_01HXE... from d_unknown quarantined: signature verification failed
sync.notify: synced from d_abc123 — applied=5 skipped=0
periodic_sync: starting with interval=5m0s, recent_threshold=2m0s
```

## Best Practices

### Network Setup

- **Tag all Thrum daemons** with `tag:thrum-daemon` in your Tailscale ACL
  policy for automatic discovery
- **Use auth keys** (`THRUM_TAILSCALE_AUTH_KEY`) for headless CI/CD runners
- **Keep the default port** (4200) unless you have a conflict

### Security

- **Enable signature verification** (`THRUM_SECURITY_REQUIRE_SIGNATURES=true`)
  once all peers are running v0.3.2+
- **Restrict allowed domains** in multi-tenant Tailscale networks
- **Enable rate limiting** for internet-exposed or high-traffic deployments
- **Monitor quarantine alerts** -- 10+ quarantined events/hour from a peer
  indicates a problem

### Performance

- **Push notifications** handle most sync latency -- events typically propagate
  within seconds
- **Periodic sync** (5 min) acts as a safety net, not the primary mechanism
- **Batch size** of 1000 events per pull keeps memory bounded during large syncs
- **Checkpointing** ensures no redundant transfers after restarts

## See Also

- [Tailscale Security](tailscale-security.md) -- Full security model
  documentation
- [Multi-Agent Support](multi-agent.md) -- Groups, runtime presets, and team
  coordination
- [Agent Coordination](agent-coordination.md) -- Workflow patterns and Beads
  integration
- [Sync Protocol](sync.md) -- Git-based synchronization details
- [CLI Reference](cli.md) -- Complete command documentation
