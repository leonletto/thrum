---
title: "Email Mesh Pairing"
description:
  "Pair two Thrum daemons over the email bridge, verify trust, and understand
  gossip propagation and revocation semantics"
category: "email"
order: 5
tags: ["email", "mesh", "pairing", "peers", "gossip", "trust", "revocation"]
last_updated: "2026-05-16"
---

## Email Mesh Pairing

After each daemon has its email bridge configured and running, pair them so
they can exchange Thrum messages across machines or repositories.

---

## Pre-conditions

Before pairing:

- Both daemons must have a running email bridge (`thrum email status` shows
  `running` on each side)
- Each daemon must have a distinct handle set via `--display-name` during
  `thrum email init`
- The two daemons must be able to reach each other's mailbox (i.e. they are
  using the same shared mailbox or can deliver mail to each other's address)

---

## Initial Pair

The pairing handshake is two-sided — each operator initiates from their own
daemon.

**On daemon-A** (operator A runs):

```bash
thrum email pair --to daemon-b@mail.example.com
```

This sends a pairing invitation to daemon-B's mailbox. The output is:

```text
pairing request sent to daemon-b@mail.example.com
waiting for confirmation... (expires in 24h)
```

**On daemon-B** (operator B receives a nudge from their coordinator agent,
then confirms):

```bash
thrum email pair --to daemon-a@mail.example.com
```

Once daemon-B's confirmation arrives in daemon-A's mailbox, both sides record
the peer. The pairing exchange completes within one IMAP poll cycle (typically
under 60 seconds).

---

## Verify with `thrum email list`

Run on either daemon:

```bash
thrum email list
```

Expected output after a successful pair:

```text
HANDLE          ADDRESS                       TRUST   STATE
daemon-a        daemon-a@mail.example.com     full    active
daemon-b        daemon-b@mail.example.com     full    active
```

Both ends must show `trust=full` and `state=active` before messages will route.

---

## Gossip Propagation

When daemon-C pairs with daemon-B (which already knows daemon-A), daemon-A
learns about daemon-C automatically through gossip. You don't need to pair
every combination of daemons manually — each new pairing propagates to all
existing peers.

The `mesh.allow_transitive_vouching` config knob controls this behavior:

```json
{
  "email": {
    "mesh": {
      "allow_transitive_vouching": true
    }
  }
}
```

Set to `false` to require explicit pairing between every pair of daemons —
useful when you want strict control over which daemons know about each other.
When disabled, gossip still propagates reachability, but trust is not extended
transitively; daemons must be paired directly before messages can flow.

**Default:** `true` (gossip propagation enabled).

---

## Revocation

To remove a peer from the mesh:

```bash
thrum email revoke daemon-b
```

Revocation is gossiped to all current peers within one poll cycle. The revoked
daemon's `state` changes to `revoked` in `thrum email list` on all daemons that
have the peer entry. Subsequent messages from the revoked daemon's address are
silently dropped.

**What happens to in-flight messages from the revoked daemon:**

- Messages already delivered to the mailbox before the revocation are processed
  normally (the revocation is not retroactive for delivered mail)
- Messages arriving after the revocation is propagated are dropped at the bridge
  layer — no error is returned to the sender (drop-silent semantics)

To re-admit a revoked daemon, run `thrum email pair` again from both sides.
The previous revocation is overwritten by the new pair entry.

---

## Pending Pair Expiry

Pairing requests that are not confirmed within 24 hours expire automatically.
The TTL is configurable:

```json
{
  "email": {
    "pair_pending_ttl_hours": 24
  }
}
```

After expiry, the invitation is discarded. Both operators must re-run
`thrum email pair` to retry.

To check for pending (unconfirmed) pairs:

```bash
thrum email list --pending
```
