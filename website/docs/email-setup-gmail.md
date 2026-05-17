---
title: "Email Bridge: Gmail Setup"
description:
  "Configure the Thrum email bridge with a Gmail account — app password,
  IMAP/SMTP settings, init invocation, and auth troubleshooting"
category: "email"
order: 1
tags: ["email", "gmail", "bridge", "imap", "smtp", "setup"]
last_updated: "2026-05-16"
---

## Email Bridge: Gmail Setup

Connect Thrum's email bridge to a Gmail (or Google Workspace) account so
daemons can exchange messages over IMAP/SMTP.

### Prerequisites

- A Gmail or Google Workspace account with 2-Step Verification enabled
- Thrum installed and the daemon running (`thrum daemon status`)
- The account used **only** for Thrum traffic — a dedicated address is strongly
  recommended (e.g. `thrum-mesh@gmail.com`)

---

## Step 1 — Generate an App Password

Google requires an app-specific password when 2FA is active; your normal account
password is rejected for IMAP/SMTP auth.

1. Open [myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords)
2. Under **Select app** choose **Mail**; under **Select device** choose **Other**
   and type `thrum-daemon`
3. Click **Generate** — copy the 16-character password (shown once)

> Google's support article: [Sign in with App Passwords](https://support.google.com/accounts/answer/185833)

---

## Step 2 — IMAP / SMTP Host Configuration

| Setting      | Value                   |
| ------------ | ----------------------- |
| IMAP host    | `imap.gmail.com`        |
| IMAP port    | `993` (TLS)             |
| SMTP host    | `smtp.gmail.com`        |
| SMTP port    | `587` (STARTTLS)        |
| Auth method  | `PLAIN` over STARTTLS   |
| Username     | Full address (e.g. `thrum-mesh@gmail.com`) |

---

## Step 3 — Run `thrum email init`

```bash
thrum email init \
  --provider gmail \
  --address thrum-mesh@gmail.com \
  --imap-host imap.gmail.com \
  --imap-port 993 \
  --smtp-host smtp.gmail.com \
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
  address:      thrum-mesh@gmail.com
  provider:     gmail
  imap:         imap.gmail.com:993  connected
  smtp:         smtp.gmail.com:587  ok
  last_poll:    2026-05-16T14:03:11Z
  last_error:   —
  peers:        0 active
```

If `last_error` shows anything, see [Troubleshooting](#troubleshoot-auth) below.

---

## Troubleshoot Auth

**`535 5.7.8 Username and Password not accepted`**
The app password was typed incorrectly, or the app-password slot was revoked.
Re-generate a new one at [myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords)
and re-run `thrum email init`.

**`534 5.7.9 Application-specific password required`**
2-Step Verification is not enabled on the account. Enable it at
[myaccount.google.com/security](https://myaccount.google.com/security), then
generate the app password.

**App password stops working after a period**
Google can expire app passwords when account security settings change (e.g.
after a password reset). Generate a fresh app password and update secrets:

```bash
thrum email init --address thrum-mesh@gmail.com --update-credentials
thrum daemon restart
```

---

## Deliverability

Gmail routes outbound mail through its own authenticated submission servers.
SPF and DKIM are handled by Google — no additional DNS records are needed for
a `gmail.com` sender.

To verify the display name appears correctly, send a test message to your
personal address:

```bash
thrum email test-send --to your-personal@gmail.com --subject "Thrum display name check"
```

Open the received message and confirm the **From** header shows your configured
`--display-name` value, not just the raw address.
