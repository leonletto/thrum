## Email Bridge: Trust Model

Understanding what v0.11 guarantees — and what it doesn't — before deploying the
email bridge in a multi-operator mesh.

---

## v0.11 Trust Root: Mailbox Access

In v0.11, **mailbox access is the trust root.** If a daemon can authenticate to
the shared mailbox (correct address + app password) and write a valid
`X-Thrum-From-Daemon` header, the mesh treats it as a legitimate participant.

This is the right tradeoff for personal meshes where all daemons are
operator-controlled: it eliminates key distribution complexity, works with every
standard mail provider, and adds no daemon-side cryptographic infrastructure.

What "trusted" means in practice:

- A daemon in the mesh can send messages that are delivered to other daemons
- A daemon in the mesh can read messages addressed to it
- Gossip peers learn about each other through the pairing exchange

What "trusted" does **not** mean:

- It does not mean the sender's identity is cryptographically verified
- It does not mean messages cannot be tampered with in transit

---

## What Is Not Signed in v0.11

Envelopes in v0.11 are **unsigned**. The `X-Thrum-From-Daemon` header is a
routing identifier inserted by the sending daemon — it is not a cryptographic
claim. Any party with access to the outbound SMTP relay can set this header to
an arbitrary value.

Concretely: if an attacker gains SMTP access (not just IMAP read access) to a
mesh mailbox, they can forge the `X-Thrum-From-Daemon` header and impersonate
any daemon in the mesh.

This is a known limitation of the v0.11 transport layer. It's acceptable for
personal operator-controlled meshes where SMTP access is equivalent to full
account compromise.

---

## What Is Deferred to v0.11.x (E18)

The following hardening is planned for v0.11.x under the E18 milestone and is
not included in the initial v0.11 release:

- **Ed25519 keypairs** — each daemon generates a keypair at first start; the
  public key is exchanged during pairing
- **Signed envelopes** — outbound messages are signed with the daemon's private
  key; recipients verify before processing
- **Replay-nonce defense** — a per-message nonce included in the signature
  prevents replayed captured messages from being accepted

Until E18 lands, operate under the assumption that mailbox-level access equals
mesh membership. The rotation procedure below covers the key mitigation path.

---

## Rotation Procedure (Suspected Compromise)

If you suspect a mailbox or app password has been compromised:

1. **Rotate the mailbox password at the provider.** Log in to Gmail, Fastmail,
   iCloud, or your self-hosted admin panel and change the account password
   immediately.

2. **Revoke all app passwords for the compromised account.** At the provider's
   security settings, delete every app password associated with the account (not
   just the Thrum one).

3. **Generate a new app password** and note it.

4. **Update each daemon's credentials.** Two equivalent paths:

   _Option A — re-run the wizard (overwrites `.thrum/secrets/email.json`):_

   ```bash
   thrum email init --provider <gmail|fastmail|icloud|custom> --non-interactive \
     --password "<new app password>" \
     --daemon-handle <existing handle> \
     --target-user <existing user> \
     --target-email <existing contact email>
   ```

   _Option B — edit the secrets file in place (mode must stay 0600):_

   ```bash
   $EDITOR .thrum/secrets/email.json   # replace imap_password / smtp_password
   ```

   Run on every daemon that uses the compromised mailbox credentials.

5. **Restart each daemon:**

   ```bash
   thrum daemon restart
   ```

6. **Verify the mesh re-converges.** After all daemons restart, run
   `thrum email list` on each side. Existing pairing entries are preserved — no
   re-pair is needed. The daemons re-establish contact through the new
   credentials automatically.

---

## What an Attacker with Mailbox Access Can Do

With read+write access to the Thrum mesh mailbox, an attacker can:

- **Spoof messages** — inject messages with a forged `X-Thrum-From-Daemon`
  header to impersonate any mesh daemon
- **Drop messages** — delete inbound messages before the IMAP bridge picks them
  up, causing message loss that is invisible to the sender
- **Read all traffic** — the bridge mailbox carries all inter-daemon messages in
  plaintext (subject to TLS in transit, but readable at rest in the mailbox)

**Mitigations available in v0.11:**

- **Audit log per gossip event** — every peer.pair, peer.revoke, and gossip
  propagation event is written to the daemon's structured audit log. Operators
  can review the log for unexpected peer additions.
- **Config history diff** — the `email.peers[]` array in the daemon config is
  written on each pairing change. Operators can diff config history to detect
  unexpected peer entries:

  ```bash
  git diff HEAD~5 .thrum/config.json | grep -A5 '"peers"'
  ```

- **Prompt rotation** — reduce the blast radius of a compromise by rotating app
  passwords on a schedule (e.g. every 90 days), even without a suspected
  incident.
