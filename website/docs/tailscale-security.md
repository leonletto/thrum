---
title: "Tailscale Sync Security"
description:
  "Security model for Thrum's Tailscale peer sync — pairing, encryption, access
  control, and threat mitigations"
category: "guides"
---

## Tailscale Sync Security

> See also: [Tailscale Sync](tailscale-sync.md) for setup, architecture, and CLI
> commands.

Security model for the Tailscale-based sync protocol.

## Overview

The sync protocol uses three layers of defense:

1. **Tailscale Encryption** -- WireGuard tunnels encrypt all traffic
2. **Pairing Code** -- Human-mediated 4-digit code establishes trust
3. **Token Authentication** -- 32-byte token authenticates every request

This replaces the previous overengineered security stack (Ed25519 signing,
validation pipeline, WhoIs authorization, rate limiting, quarantine system) with
a simpler model that provides equivalent practical security.

## Layer 1: Tailscale Encryption

All sync traffic flows over Tailscale's WireGuard mesh network. This provides:

- **End-to-end encryption** between peers
- **Identity verification** via Tailscale's control plane
- **NAT traversal** with no port forwarding or firewall configuration
- **Network-level access control** via Tailscale ACLs

Tailscale handles the hard parts of secure networking. Thrum doesn't need to
implement its own transport encryption.

### Recommended ACL Configuration

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

## Layer 2: Pairing Code

Trust between two machines is established through a human-mediated pairing flow.

### How It Works

1. Machine A generates a random 4-digit code and displays it to the user
2. The user communicates the code to Machine B's operator (verbally, chat, etc.)
3. Machine B sends the code to Machine A over Tailscale
4. Machine A verifies the code and establishes the peer relationship

### Security Properties

- **Human-in-the-loop**: A person must deliberately share the code, preventing
  automated or accidental pairing
- **Time-limited**: Pairing sessions expire after 5 minutes
- **Attempt-limited**: Only 3 attempts per session before lockout
- **One-time use**: Each code can only be used once

### Threat Model

The pairing code provides protection against:

- **Unauthorized peers** -- Only someone with the code can pair
- **Replay attacks** -- Codes expire and are single-use
- **Brute force** -- 3-attempt limit makes guessing impractical within the
  5-minute window

The code does NOT protect against an attacker who can both observe the code
being shared AND intercept the Tailscale connection. In practice, Tailscale's
network security makes this extremely unlikely.

## Layer 3: Token Authentication

After pairing, a 32-byte hex token (256 bits of entropy) is shared between the
two peers. Every sync request includes this token.

### Token Lifecycle

1. **Generation**: A random 32-byte token is generated during pairing
2. **Distribution**: The token is sent to the joining peer in the pairing
   response
3. **Storage**: Both peers store the token in their `peers.json` file
4. **Validation**: Every sync request is validated against the token before
   processing

### Validation Flow

```text
Incoming sync request
   │
   ├─ Is method "pair.request"?  ──► Yes: Skip auth (pairing flow)
   │
   ├─ Extract token from params
   │
   ├─ Look up token in peer registry
   │   ├─ Not found  ──► Reject (unauthorized)
   │   └─ Found      ──► Allow + update last_sync
   │
   └─ Dispatch to handler
```

### Security Properties

- **256 bits of entropy** -- Computationally infeasible to guess
- **Per-peer tokens** -- Each peer relationship has its own token
- **Central validation** -- All RPCs are authenticated in the sync server before
  handler dispatch
- **Exempt only pair.request** -- The pairing RPC is the only unauthenticated
  method (protected by the pairing code instead)

## Configuration

No security-specific configuration is needed. The security model is built into
the pairing and sync flow.

| Aspect           | Configuration                        |
| ---------------- | ------------------------------------ |
| Encryption       | Automatic (Tailscale)                |
| Pairing timeout  | 5 minutes (hardcoded)                |
| Pairing attempts | 3 per session (hardcoded)            |
| Token length     | 32 bytes / 256 bits (hardcoded)      |
| Network ACLs     | Configure in Tailscale admin console |

## Comparison with Previous Model

| Previous (Removed)            | Current (Simplified)                                 |
| ----------------------------- | ---------------------------------------------------- |
| Ed25519 event signing         | Not needed -- Tailscale provides transport integrity |
| 3-stage validation pipeline   | Not needed -- token auth is sufficient               |
| WhoIs authorization           | Not needed -- pairing code + Tailscale ACLs          |
| Per-peer rate limiting        | Not needed -- Tailscale rate limits at network level |
| Quarantine system             | Not needed -- invalid tokens are simply rejected     |
| TOFU key pinning              | Replaced by explicit human-mediated pairing          |
| ~1,074 lines of security code | ~40 lines of token validation                        |

## Troubleshooting

### Peer rejected with "unauthorized"

The peer's token doesn't match. This can happen if:

1. The peer was removed and re-paired (old token is invalid)
2. The `peers.json` file was manually edited or corrupted
3. The peer registry was reset on one side

**Fix**: Remove the peer on both machines and re-pair:

```bash
# On both machines:
thrum peer remove <name>

# Then pair again:
# Machine A: thrum peer add
# Machine B: thrum peer join <address>
```

### Pairing fails with "no active pairing session"

The pairing session on Machine A expired or was never started.

**Fix**: Run `thrum peer add` on Machine A first, then `thrum peer join` on
Machine B within 5 minutes.

### Pairing fails with "too many failed attempts"

Three incorrect codes were entered.

**Fix**: Run `thrum peer add` again on Machine A to start a fresh session with a
new code.
```
