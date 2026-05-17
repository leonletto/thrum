---
title: "Email Bridge: Troubleshooting"
description:
  "Decision-tree for common email bridge failures — bridge won't start, IDLE
  dropping, outbound queue stuck, missing peers, and size-limit errors"
category: "email"
order: 7
tags: ["email", "troubleshooting", "bridge", "imap", "smtp", "queue", "debug"]
last_updated: "2026-05-16"
---

## Email Bridge: Troubleshooting

A symptom-first decision tree for diagnosing email bridge failures. Start with
the symptom that matches, follow the steps in order, and stop when the problem
is resolved.

---

## Bridge Won't Start

**Symptom:** `thrum email status` shows `bridge: stopped` or the daemon log
contains `email bridge failed to start`.

### Check 1 — Secrets file exists

```bash
ls -la .thrum/secrets/email.json
```

If the file is missing, run `thrum email init` to create it.

### Check 2 — Secrets file permissions

The file must be mode `0600`. If it isn't, the bridge refuses to load it:

```bash
chmod 0600 .thrum/secrets/email.json
thrum daemon restart
```

Daemon log signature for this error: `ErrEmailSecretsMode`.

### Check 3 — Secrets file is valid JSON

```bash
cat .thrum/secrets/email.json | python3 -m json.tool
```

A parse error means the file was written incorrectly (e.g. truncated during
`thrum email init`). Re-run init:

```bash
thrum email init --address your-address@example.com --update-credentials
```

### Check 4 — IMAP host reachable

```bash
openssl s_client -connect imap.gmail.com:993 -quiet 2>&1 | head -5
```

If the connection times out or TLS handshake fails, the network path to the IMAP
host is blocked. Check firewall rules and DNS resolution.

---

## IDLE Silently Dropping (Mail Not Arriving)

**Symptom:** Messages sent from daemon-B never arrive on daemon-A, even though
`thrum email status` shows `running` and no `last_error`.

### Check 1 — IDLE re-issuance in daemon logs

The bridge re-issues an IMAP IDLE command every 25 minutes to prevent server-
side timeout. Look for this log line:

```bash
thrum daemon logs | grep "imap: idle re-armed"
```

If the line never appears, the IDLE loop may have exited without restarting.
Restart the daemon to reset the IDLE goroutine:

```bash
thrum daemon restart
```

### Check 2 — Last poll timestamp

```bash
thrum email status
```

Check `last_poll`. If it is more than a few minutes old and the daemon is
running, the IMAP connection may have stalled. A daemon restart clears it.

### Check 3 — Network connectivity to IMAP host

Intermittent connectivity drops can cause the IDLE connection to go silent
without an explicit error. Check system network logs and verify the IMAP host
is reachable from the daemon's machine.

### Check 4 — Mailbox is the correct folder

By default the bridge watches the `INBOX`. If mail is being delivered to a
different folder (e.g. by a server-side filter), messages are missed. Verify
no server-side filters redirect Thrum messages out of `INBOX`.

---

## Outbound Stuck in Queue

**Symptom:** `thrum send` appears to succeed, but the recipient daemon never
receives the message. `thrum email status` shows a non-empty queue.

### Check 1 — Inspect paused peers

```bash
thrum email status
```

Look for a `paused_peers` list. A peer is paused after repeated SMTP delivery
failures to its address.

To unblock a paused peer:

```bash
thrum email unblock <peer_handle>
```

### Check 2 — Queue worker logs for SMTP errors

```bash
thrum daemon logs | grep "smtp:"
```

Common SMTP errors and their causes:

| Error                            | Cause                                         |
| -------------------------------- | --------------------------------------------- |
| `530 5.7.0 Authentication required` | SMTP auth not configured or wrong credentials |
| `550 5.1.1 User unknown`         | Recipient address does not exist              |
| `421 Too many connections`       | Rate-limited by the SMTP relay                |
| `Connection refused`             | SMTP host or port unreachable                 |

### Check 3 — Message size limit

If the queued message exceeds `email.max_outbound_bytes`, it is held in the
queue but never sent. See the [Send Returns over_size_limit](#send-returns-over_size_limit)
section below.

---

## Peer Not in `thrum email list`

**Symptom:** After running `thrum email pair`, the peer does not appear in
`thrum email list` on either side.

### Check 1 — Pairing request delivered?

The pairing invitation is sent by email. Verify it arrived in the target
daemon's mailbox. If not, the SMTP delivery failed — check `thrum daemon logs`
for SMTP errors at the time of the `pair` command.

### Check 2 — Confirmation received?

Both sides must run `thrum email pair --to <address>` for the pair to complete.
If only one side has confirmed, the entry stays in `--pending` state:

```bash
thrum email list --pending
```

The other operator must run their `thrum email pair` command.

### Check 3 — Pending pair expired

Pairing requests expire after `pair_pending_ttl_hours` (default: 24 hours). If
the confirmation was not received in time, both sides must re-run `thrum email pair`.

### Check 4 — Audit log for pair events

```bash
thrum daemon logs | grep "peer.pair"
```

The `peer.pair` audit line is written when a pair exchange completes. If it's
absent on one side, that daemon's IMAP bridge hasn't processed the confirmation
message yet. Wait one poll cycle (up to 60 seconds) or restart the daemon.

---

## Send Returns `over_size_limit`

**Symptom:** `thrum send` fails with `error: over_size_limit`.

The message body exceeds the configured `email.max_outbound_bytes` limit
(default: `102400` = 100 KB).

### Check current limit

```bash
thrum config show | grep max_outbound
```

### Raise the limit

Edit `.thrum/config.json`:

```json
{
  "email": {
    "max_outbound_bytes": 524288
  }
}
```

Then restart the daemon:

```bash
thrum daemon restart
```

> Keep the limit below your SMTP relay's maximum message size. Gmail's limit is
> 25 MB; Fastmail's is 50 MB. Most Thrum coordination messages are well under 1 MB.

---

## Still Stuck?

1. Collect a full log snapshot: `thrum daemon logs > /tmp/thrum-email-debug.txt`
2. Run `thrum email status --json` for machine-readable bridge state
3. Check the daemon version: `thrum version`

File an issue at [github.com/leonletto/thrum](https://github.com/leonletto/thrum/issues)
with the log snapshot and `thrum email status --json` output.
