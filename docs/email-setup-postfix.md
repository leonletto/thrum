## Email Bridge: Self-Hosted Postfix Setup

Run the Thrum email bridge against a Postfix MTA you control. This is the right
path when you need a private mesh with no dependency on a cloud mail provider.

### Prerequisites

- A Postfix MTA reachable from all daemons in your mesh (port 587 STARTTLS or
  465 implicit TLS)
- Dovecot (or another IMAP server) fronting the same mailbox that Postfix
  delivers into
- A dedicated mailbox for Thrum traffic (e.g. `thrum@mail.example.com`)
- SASL authentication configured in Postfix (`smtpd_sasl_auth_enable = yes`)
- Thrum installed and the daemon running (`thrum daemon status`)

---

## Postfix Submission Configuration (Reference)

A minimal `master.cf` submission stanza that enables STARTTLS and SASL:

```text
submission inet n - y - - smtpd
  -o syslog_name=postfix/submission
  -o smtpd_tls_security_level=encrypt
  -o smtpd_sasl_auth_enable=yes
  -o smtpd_sasl_type=dovecot
  -o smtpd_sasl_path=private/auth
  -o smtpd_recipient_restrictions=permit_sasl_authenticated,reject
  -o milter_macro_daemon_name=ORIGINATING
```

Key `main.cf` settings:

```text
smtpd_tls_cert_file = /etc/letsencrypt/live/mail.example.com/fullchain.pem
smtpd_tls_key_file  = /etc/letsencrypt/live/mail.example.com/privkey.pem
smtpd_use_tls       = yes
smtpd_tls_security_level = may
```

> Only STARTTLS on port 587 or implicit TLS on port 465 should be used.
> Unauthenticated port-25 relay is not supported by the email bridge.

---

## Daemon-Handle Naming Convention

Each Thrum daemon has a short handle used as a routing identifier inside the
mesh. For self-hosted deployments, use a name that reflects the machine or repo:

```text
daemon-a          ← daemon on machine "a", repo "main"
daemon-b-infra    ← daemon on machine "b", repo "infra"
```

The handle is set with `--daemon-handle` during `thrum email init`. It becomes
the `X-Thrum-From-Daemon` header value on outbound messages — keep it stable
after pairing, because the mesh uses it as a routing key.

---

## IMAP / SMTP Host Configuration

| Setting     | Value                                       |
| ----------- | ------------------------------------------- |
| IMAP host   | Your Dovecot host (e.g. `mail.example.com`) |
| IMAP port   | `993` (TLS) or `143` (STARTTLS)             |
| SMTP host   | Your Postfix host                           |
| SMTP port   | `587` (STARTTLS) or `465` (TLS)             |
| Auth method | `PLAIN` over STARTTLS                       |
| Username    | Full mailbox address                        |

---

## Run `thrum email init`

Self-hosted MTAs use the `custom` provider with explicit IMAP/SMTP hosts:

```bash
thrum email init --provider custom
```

The interactive wizard prompts for IMAP/SMTP hosts + ports, then the mailbox
password, and writes `.thrum/secrets/email.json` (mode `0600`).

For scripted / non-interactive setup:

```bash
thrum email init \
  --provider custom \
  --non-interactive \
  --imap-host mail.example.com --imap-port 993 \
  --smtp-host mail.example.com --smtp-port 587 \
  --password "<mailbox password>" \
  --daemon-handle daemon-a \
  --target-user leon \
  --target-email leon@personal-email.com
```

After init, run `thrum daemon restart` to load the new bridge config.

---

## Verify with `thrum email status`

```bash
thrum email status
```

Expected output:

```text
email bridge: running
  address:      thrum@mail.example.com
  provider:     smtp
  imap:         mail.example.com:993  connected
  smtp:         mail.example.com:587  ok
  last_poll:    2026-05-16T14:03:11Z
  last_error:   —
  peers:        0 active
```

---

## Troubleshoot Auth

**`535 5.7.8 Authentication credentials invalid`** Verify the SASL password is
correct. Check that `smtpd_sasl_auth_enable = yes` is set in `main.cf` for the
submission daemon, not just the main smtpd.

**`454 4.7.0 TLS not available`** Postfix TLS certificates are missing or
expired. Check `/var/log/mail.log` for OpenSSL errors and renew the certificate.

**IMAP connects but SMTP rejects** Postfix `smtpd_recipient_restrictions` may
not include `permit_sasl_authenticated`. Verify `master.cf` has the restriction
set on the submission service specifically.

**`Connection refused` on port 587** The submission service may not be enabled.
Check `master.cf` for the `submission` line and restart Postfix after changes:

```bash
postfix reload
```

---

## Deliverability

For outbound mail from your own domain to reach external providers, configure:

- **SPF:** add a `TXT` record to your DNS pointing to your MTA's IP
- **DKIM:** configure `opendkim` on your Postfix server and add the public key
  to DNS
- **DMARC:** optional but recommended for `p=quarantine` policy

For a closed private mesh (all daemons on the same domain or LAN), SPF/DKIM are
not required — authenticated submission over STARTTLS is sufficient.
