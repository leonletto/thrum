---
title: "Email Bridge: iCloud Mail Setup"
description:
  "Configure the Thrum email bridge with an iCloud Mail account — app-specific
  password, IMAP/SMTP settings, init invocation, and auth troubleshooting"
category: "email"
order: 3
tags: ["email", "icloud", "apple", "bridge", "imap", "smtp", "setup"]
last_updated: "2026-05-16"
---

## Email Bridge: iCloud Mail Setup

Connect Thrum's email bridge to an iCloud Mail account so daemons can exchange
messages over IMAP/SMTP.

### Prerequisites

- An Apple ID with iCloud Mail enabled
- Two-Factor Authentication active on the Apple ID
- Thrum installed and the daemon running (`thrum daemon status`)
- A dedicated `@icloud.com` or custom-domain address for Thrum traffic

---

## Step 1 — Generate an App-Specific Password

Apple requires an app-specific password for third-party IMAP/SMTP clients when
2FA is enabled on your Apple ID.

1. Open [appleid.apple.com](https://appleid.apple.com) and sign in
2. Under **Sign-In and Security**, select **App-Specific Passwords**
3. Click **+** and name the password `thrum-daemon`
4. Copy the generated password in `xxxx-xxxx-xxxx-xxxx` format — shown only once

> Apple support article: [Sign in with app-specific passwords](https://support.apple.com/HT204397)

---

## Step 2 — IMAP / SMTP Host Configuration

| Setting      | Value                         |
| ------------ | ----------------------------- |
| IMAP host    | `imap.mail.me.com`            |
| IMAP port    | `993` (TLS)                   |
| SMTP host    | `smtp.mail.me.com`            |
| SMTP port    | `587` (STARTTLS)              |
| Auth method  | `PLAIN` over STARTTLS         |
| Username     | Full Apple ID address (e.g. `thrum-mesh@icloud.com`) |

> **Note:** Apple also supports port `465` (implicit TLS) for SMTP. Use `587`
> with STARTTLS unless your network blocks outbound port 587.

---

## Step 3 — Run `thrum email init`

```bash
thrum email init \
  --provider icloud \
  --address thrum-mesh@icloud.com \
  --imap-host imap.mail.me.com \
  --imap-port 993 \
  --smtp-host smtp.mail.me.com \
  --smtp-port 587 \
  --display-name "Thrum Daemon A"
```

The command prompts for the app-specific password (in `xxxx-xxxx-xxxx-xxxx`
format) and writes `.thrum/secrets/email.json` (mode `0600`). The daemon
restarts the bridge automatically.

---

## Step 4 — Verify with `thrum email status`

```bash
thrum email status
```

Expected output when the bridge is healthy:

```text
email bridge: running
  address:      thrum-mesh@icloud.com
  provider:     icloud
  imap:         imap.mail.me.com:993  connected
  smtp:         smtp.mail.me.com:587  ok
  last_poll:    2026-05-16T14:03:11Z
  last_error:   —
  peers:        0 active
```

---

## Troubleshoot Auth

**`[AUTHENTICATIONFAILED] Authentication failed`**
The app-specific password was entered incorrectly. iCloud passwords include
hyphens; paste the full `xxxx-xxxx-xxxx-xxxx` string without spaces. Revoke
the existing slot at [appleid.apple.com](https://appleid.apple.com) and
generate a new one.

**`[UNAVAILABLE] Service temporarily unavailable`**
iCloud IMAP has brief outages. Check
[Apple System Status](https://www.apple.com/support/systemstatus/) and retry
after a few minutes.

**IMAP works but SMTP fails**
Verify SMTP username is the full Apple ID address. iCloud rejects short-form
usernames on the SMTP AUTH step. Also confirm port 587 is reachable — some
ISPs block it. Try port 465 (implicit TLS) if 587 is blocked.

**App-specific password expired**
Apple revokes all app-specific passwords when you change your Apple ID password.
Generate a fresh one and update:

```bash
thrum email init --address thrum-mesh@icloud.com --update-credentials
thrum daemon restart
```

---

## Deliverability

iCloud Mail handles SPF and DKIM automatically for `@icloud.com` and `@me.com`
senders. Custom-domain iCloud+ addresses follow the same policy; no additional
DNS records are needed.

To verify the display name appears correctly:

```bash
thrum email test-send --to your-personal@email.com --subject "Thrum display name check"
```

Open the received message and confirm the **From** header shows your configured
`--display-name`.
