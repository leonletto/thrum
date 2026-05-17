---
title: "Email Bridge: Fastmail Setup"
description:
  "Configure the Thrum email bridge with a Fastmail account — app password,
  IMAP/SMTP settings, init invocation, and auth troubleshooting"
category: "email"
order: 2
tags: ["email", "fastmail", "bridge", "imap", "smtp", "setup"]
last_updated: "2026-05-16"
---

## Email Bridge: Fastmail Setup

Connect Thrum's email bridge to a Fastmail account so daemons can exchange
messages over IMAP/SMTP.

### Prerequisites

- A Fastmail account (individual or team)
- Thrum installed and the daemon running (`thrum daemon status`)
- A dedicated Fastmail address or masked email for Thrum traffic

---

## Step 1 — Generate an App Password

Fastmail uses per-app passwords for third-party IMAP/SMTP access. Your main
account password does not work for IMAP.

1. Log in at [app.fastmail.com](https://app.fastmail.com)
2. Go to **Settings → Privacy & Security → Passwords & Security → App Passwords**
3. Click **New App Password**, name it `thrum-daemon`, and select the
   **Mail (IMAP/POP/SMTP)** access level
4. Copy the generated password — it is shown only once

> Fastmail docs: [App Passwords](https://www.fastmail.help/hc/en-us/articles/360058752854)

---

## Step 2 — IMAP / SMTP Host Configuration

| Setting      | Value                        |
| ------------ | ---------------------------- |
| IMAP host    | `imap.fastmail.com`          |
| IMAP port    | `993` (TLS)                  |
| SMTP host    | `smtp.fastmail.com`          |
| SMTP port    | `587` (STARTTLS)             |
| Auth method  | `PLAIN` over STARTTLS        |
| Username     | Full Fastmail address (e.g. `thrum-mesh@fastmail.com`) |

---

## Step 3 — Run `thrum email init`

```bash
thrum email init \
  --provider fastmail \
  --address thrum-mesh@fastmail.com \
  --imap-host imap.fastmail.com \
  --imap-port 993 \
  --smtp-host smtp.fastmail.com \
  --smtp-port 587 \
  --display-name "Thrum Daemon A"
```

The command prompts for the app password and writes
`.thrum/secrets/email.json` (mode `0600`). The daemon restarts the bridge
automatically.

---

## Step 4 — Verify with `thrum email status`

```bash
thrum email status
```

Expected output when the bridge is healthy:

```text
email bridge: running
  address:      thrum-mesh@fastmail.com
  provider:     fastmail
  imap:         imap.fastmail.com:993  connected
  smtp:         smtp.fastmail.com:587  ok
  last_poll:    2026-05-16T14:03:11Z
  last_error:   —
  peers:        0 active
```

If `last_error` shows anything, see [Troubleshoot Auth](#troubleshoot-auth) below.

---

## Troubleshoot Auth

**`535 Authentication credentials invalid`**
The app password was pasted incorrectly or the slot was deleted. Go to
**Settings → App Passwords**, delete the `thrum-daemon` entry, create a new
one, and re-run `thrum email init --update-credentials`.

**`IMAP LOGIN failed`**
Fastmail requires the username to be the **full email address**, not just the
local part. Verify `--address` is set to `user@fastmail.com`, not `user`.

**App password stops working**
Fastmail invalidates app passwords when you change your main account password.
Generate a fresh app password and update:

```bash
thrum email init --address thrum-mesh@fastmail.com --update-credentials
thrum daemon restart
```

---

## Deliverability

Fastmail's SMTP servers handle SPF and DKIM automatically for addresses on your
domain. For `@fastmail.com` addresses, authentication records are pre-configured
— no additional DNS steps are required.

To verify the display name appears correctly:

```bash
thrum email test-send --to your-personal@email.com --subject "Thrum display name check"
```

Open the received message and confirm the **From** header shows your configured
`--display-name`.
