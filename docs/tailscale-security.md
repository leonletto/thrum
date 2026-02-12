# Tailscale Sync Security

Security features for the Tailscale-based sync protocol.

## Overview

The sync protocol uses multiple layers of defense:

1. **Ed25519 Event Signing** — every event is cryptographically signed at creation
2. **Validation Pipeline** — three-stage validation for incoming sync events
3. **Tailscale WhoIs Authorization** — peer identity verification via Tailscale
4. **Rate Limiting** — per-peer token bucket rate limiting
5. **Quarantine System** — invalid events are isolated and tracked

## Event Signing

Events are signed with Ed25519 keys before being written to the event log.

### Key Management

On first daemon start, an Ed25519 key pair is generated and stored at
`.thrum/var/identity.key` (PEM format, 0600 permissions). The public key
fingerprint is logged on startup for verification.

### Canonical Signing Format

The signature covers a canonical payload: `event_id|type|timestamp|origin_daemon`.
This ensures the core identity of each event is tamper-proof while allowing
non-critical fields to be modified.

### Backward Compatibility

Events without signatures are accepted by default. Set `THRUM_SECURITY_REQUIRE_SIGNATURES=true`
to reject unsigned events (recommended after all daemons have been upgraded).

## Validation Pipeline

Incoming sync events pass through three validation stages:

### Stage 1: Schema Validation
- Required fields present (`event_id`, `type`, `timestamp`, `origin_daemon`)
- Valid event type
- Event size within `MaxEventSize` (default: 1MB)

### Stage 2: Signature Verification
- Ed25519 signature check against peer's public key
- Invalid signatures are rejected
- Missing signatures accepted unless `require_signatures` is enabled

### Stage 3: Business Logic
- Timestamp sanity: not more than 24h in the future
- Message content size limit (default: 100KB)
- Agent ID format validation

Events that fail any stage are quarantined instead of applied.

## Authorization

### Tailscale WhoIs

When enabled, every sync connection is verified via Tailscale's `WhoIs` API to
confirm the peer's identity. Three authorization checks are available:

| Check | Config | Description |
|-------|--------|-------------|
| Allowed Peers | `THRUM_SECURITY_ALLOWED_PEERS` | Comma-separated hostnames |
| Required Tags | ACL tags | At least one tag must match (e.g., `tag:thrum-daemon`) |
| Allowed Domain | `THRUM_SECURITY_ALLOWED_DOMAIN` | Login name suffix (e.g., `@company.com`) |

All configured checks must pass. Unconfigured checks are skipped.

### ACL Setup

#### Tailscale ACLs

```json
{
  "tagOwners": {
    "tag:thrum-daemon": ["group:devops"]
  },
  "acls": [
    {
      "action": "accept",
      "src": ["tag:thrum-daemon"],
      "dst": ["tag:thrum-daemon:9100"]
    }
  ]
}
```

#### Headscale ACLs

```yaml
groups:
  - name: thrum-daemons
    members: ["user1", "user2"]

acls:
  - action: accept
    src: ["group:thrum-daemons"]
    dst: ["group:thrum-daemons:9100"]
```

## Rate Limiting

Per-peer token bucket rate limiting protects against abuse.

| Parameter | Default | Environment Variable |
|-----------|---------|---------------------|
| Requests/sec | 10 | `THRUM_SECURITY_MAX_RPS` |
| Burst size | 20 | `THRUM_SECURITY_BURST_SIZE` |
| Queue depth | 1000 | `THRUM_SECURITY_MAX_QUEUE_DEPTH` |

- **429**: Rate limit exceeded (per-peer)
- **503**: Sync queue full (global overload)

## Quarantine

Invalid events are stored in a quarantine table with:
- Event ID and full JSON
- Peer that sent it
- Failure reason
- Timestamp

An alert is logged when more than 10 invalid events are received from the same
peer within one hour.

## Configuration

All security settings are configured via environment variables:

```bash
# Event validation
THRUM_SECURITY_MAX_EVENT_SIZE=1048576      # 1 MB (default)
THRUM_SECURITY_MAX_BATCH_SIZE=1000         # events per sync batch
THRUM_SECURITY_MAX_MESSAGE_SIZE=102400     # 100 KB (default)
THRUM_SECURITY_REQUIRE_SIGNATURES=false    # reject unsigned events

# Rate limiting
THRUM_SECURITY_RATE_LIMIT_ENABLED=true     # enable rate limiting
THRUM_SECURITY_MAX_RPS=10                  # requests/sec per peer
THRUM_SECURITY_BURST_SIZE=20               # burst allowance
THRUM_SECURITY_MAX_QUEUE_DEPTH=1000        # max sync queue depth

# Authorization
THRUM_SECURITY_REQUIRE_AUTH=false           # require WhoIs auth
THRUM_SECURITY_ALLOWED_DOMAIN=@company.com # domain filter
```

### Example Configurations

#### Development (permissive)

```bash
THRUM_SECURITY_REQUIRE_SIGNATURES=false
THRUM_SECURITY_RATE_LIMIT_ENABLED=false
THRUM_SECURITY_REQUIRE_AUTH=false
```

#### Production (hardened)

```bash
THRUM_SECURITY_REQUIRE_SIGNATURES=true
THRUM_SECURITY_RATE_LIMIT_ENABLED=true
THRUM_SECURITY_MAX_RPS=5
THRUM_SECURITY_BURST_SIZE=10
THRUM_SECURITY_REQUIRE_AUTH=true
THRUM_SECURITY_ALLOWED_DOMAIN=@yourcompany.com
```

## Troubleshooting

### Events rejected with "invalid signature"

1. Verify both daemons are using the same version of thrum
2. Check that the peer's public key is registered: look for `sync.peer_info` exchange in logs
3. If upgrading, set `THRUM_SECURITY_REQUIRE_SIGNATURES=false` temporarily

### Rate limit errors (429)

Increase `THRUM_SECURITY_MAX_RPS` or `THRUM_SECURITY_BURST_SIZE`. Check if a peer
is generating excessive sync traffic.

### Quarantined events

Check quarantined events for patterns:
- Same peer repeatedly failing → possible misconfiguration or malicious peer
- Schema violations → version mismatch between daemons
- Signature failures → key rotation needed or man-in-the-middle
