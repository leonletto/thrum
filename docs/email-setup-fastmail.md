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
2. Go to **Settings → Privacy & Security → Passwords & Security → App
   Passwords**
3. Click **New App Password**, name it `thrum-daemon`, and select the **Mail
   (IMAP/POP/SMTP)** access level
4. Copy the generated password — it is shown only once

> Fastmail docs:
> [App Passwords](https://www.fastmail.help/hc/en-us/articles/360058752854)

---

## Step 2 — IMAP / SMTP Host Configuration

| Setting     | Value                                                  |
| ----------- | ------------------------------------------------------ |
| IMAP host   | `imap.fastmail.com`                                    |
| IMAP port   | `993` (TLS)                                            |
| SMTP host   | `smtp.fastmail.com`                                    |
| SMTP port   | `587` (STARTTLS)                                       |
| Auth method | `PLAIN` over STARTTLS                                  |
| Username    | Full Fastmail address (e.g. `thrum-mesh@fastmail.com`) |

---

## Step 3 — Run `thrum email init`

The interactive wizard prompts for the app password and writes
`.thrum/secrets/email.json` (mode `0600`):

```bash
thrum email init --provider fastmail
```

For scripted / non-interactive setup:

```bash
thrum email init \
  --provider fastmail \
  --non-interactive \
  --password "<app password>" \
  --daemon-handle thrum-daemon-a \
  --target-user leon \
  --target-email leon@personal-email.com
```

`--daemon-handle` is the mesh-visible identity peers use to address you;
`--target-user` is the Thrum username this mailbox bridges to; `--target-email`
is the operator contact address that doubles as the RFC 5322 `From:` header
(override separately with `--from-address`).

After init, run `thrum daemon restart` to load the new bridge config.

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

If `last_error` shows anything, see [Troubleshoot Auth](#troubleshoot-auth)
below.

---

## Troubleshoot Auth

**`535 Authentication credentials invalid`** The app password was pasted
incorrectly or the slot was deleted. Go to **Settings → App Passwords**, delete
the `thrum-daemon` entry, create a new one, and re-run
`thrum email init --provider fastmail` (the wizard overwrites
`.thrum/secrets/email.json`).

**`IMAP LOGIN failed`** Fastmail requires the username to be the **full email
address**, not just the local part. The username is captured during the init
wizard's prompts (or falls through from `--target-email`); verify the stored
value in `.thrum/config.json` under `email.username` is `user@fastmail.com`, not
`user`.

**App password stops working** Fastmail invalidates app passwords when you
change your main account password. Generate a fresh app password and either
re-run the wizard or edit the secrets file directly:

```bash
# Option A: re-run the wizard with the new password
thrum email init --provider fastmail --non-interactive \
  --password "<new app password>" \
  --daemon-handle thrum-daemon-a \
  --target-user leon \
  --target-email leon@personal-email.com
thrum daemon restart

# Option B: edit .thrum/secrets/email.json in place (mode must stay 0600)
$EDITOR .thrum/secrets/email.json
thrum daemon restart
```

---

## Deliverability

Fastmail's SMTP servers handle SPF and DKIM automatically for addresses on your
domain. For `@fastmail.com` addresses, authentication records are pre-configured
— no additional DNS steps are required.

To verify the display name appears correctly:

```bash
thrum email send \
  --to your-personal@email.com \
  --subject "Thrum display name check" \
  --body "test"
```

Open the received message and confirm the **From** header shows the daemon
handle from your configured `from_display_name_format` (default
`{agent} @ {handle}`), not just the raw address.
