## Email Mesh Pairing

After each daemon has its email bridge configured and running, pair them so they
can exchange Thrum messages across machines or repositories.

---

## Pre-conditions

Before pairing:

- Both daemons must have a running email bridge (`thrum email status` shows
  `running` on each side)
- Each daemon must have a distinct handle set via `--daemon-handle` during
  `thrum email init` (and matching peer entries on both sides)
- The two daemons must be able to reach each other's mailbox (i.e. they are
  using the same shared mailbox or can deliver mail to each other's address)

---

## Initial Pair

The pairing handshake is two-sided. Each daemon receives an inbound pair-request
envelope from the other peer's first contact, which lands in the bridge's
pending-stranger-pair list keyed by the sender's `daemon_handle`. The operator
then confirms the pending entry with `thrum email pair --to <handle>`.

**On daemon-A** (operator A confirms daemon-B's pending request):

```bash
thrum email pair --to daemon-b
```

The handle (`daemon-b`) is the `--daemon-handle` value daemon-B was configured
with at `thrum email init`. Output:

```text
Paired with daemon-b
```

**On daemon-B** (operator B confirms daemon-A's pending request):

```bash
thrum email pair --to daemon-a
```

Once both sides have confirmed, both daemons record the peer with `trust=full`.
The exchange completes within one IMAP poll cycle (typically under 60 seconds).

> Pre-pairing exchange of handles is out-of-band (Signal, in person, shared
> config). The `pair` command only confirms a pending stranger request — it does
> not initiate one.

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
learns about daemon-C automatically through gossip. You don't need to pair every
combination of daemons manually — each new pairing propagates to all existing
peers.

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

To re-admit a revoked daemon, run `thrum email pair` again from both sides. The
previous revocation is overwritten by the new pair entry.

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

To check for pending (unconfirmed) pairs, run `thrum email status` — the output
includes a count of pending stranger-pair entries waiting for operator
confirmation:

```bash
thrum email status
```
