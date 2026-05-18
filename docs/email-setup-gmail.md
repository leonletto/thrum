## Email Bridge: Gmail Setup

Connect Thrum's email bridge to a Gmail (or Google Workspace) account so daemons
can exchange messages over IMAP/SMTP.

### Prerequisites

- A Gmail or Google Workspace account with 2-Step Verification enabled
- Thrum installed and the daemon running (`thrum daemon status`)
- The account used **only** for Thrum traffic — a dedicated address is strongly
  recommended (e.g. `thrum-mesh@gmail.com`)

---

## Step 1 — Generate an App Password

Google requires an app-specific password when 2FA is active; your normal account
password is rejected for IMAP/SMTP auth.

1. Open
   [myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords)
2. Under **Select app** choose **Mail**; under **Select device** choose
   **Other** and type `thrum-daemon`
3. Click **Generate** — copy the 16-character password (shown once)

> Google's support article:
> [Sign in with App Passwords](https://support.google.com/accounts/answer/185833)

---

## Step 2 — IMAP / SMTP Host Configuration

| Setting     | Value                                      |
| ----------- | ------------------------------------------ |
| IMAP host   | `imap.gmail.com`                           |
| IMAP port   | `993` (TLS)                                |
| SMTP host   | `smtp.gmail.com`                           |
| SMTP port   | `587` (STARTTLS)                           |
| Auth method | `PLAIN` over STARTTLS                      |
| Username    | Full address (e.g. `thrum-mesh@gmail.com`) |

---

## Step 3 — Run `thrum email init`

The interactive wizard prompts for the app password and writes
`.thrum/secrets/email.json` (mode `0600`):

```bash
thrum email init --provider gmail
```

For scripted / non-interactive setup:

```bash
thrum email init \
  --provider gmail \
  --non-interactive \
  --password "<16-char app password>" \
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

**`535 5.7.8 Username and Password not accepted`** The app password was typed
incorrectly, or the app-password slot was revoked. Re-generate a new one at
[myaccount.google.com/apppasswords](https://myaccount.google.com/apppasswords)
and re-run `thrum email init`.

**`534 5.7.9 Application-specific password required`** 2-Step Verification is
not enabled on the account. Enable it at
[myaccount.google.com/security](https://myaccount.google.com/security), then
generate the app password.

**App password stops working after a period** Google can expire app passwords
when account security settings change (e.g. after a password reset). Generate a
fresh app password and either re-run `thrum email init` (the wizard overwrites
`.thrum/secrets/email.json`) or edit the secrets file directly:

```bash
# Option A: re-run the wizard with the new password
thrum email init --provider gmail --non-interactive \
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

Gmail routes outbound mail through its own authenticated submission servers. SPF
and DKIM are handled by Google — no additional DNS records are needed for a
`gmail.com` sender.

To verify the display name appears correctly, send a test message to your
personal address:

```bash
thrum email send \
  --to your-personal@gmail.com \
  --subject "Thrum display name check" \
  --body "test"
```

Open the received message and confirm the **From** header shows the daemon
handle from your configured `from_display_name_format` (default
`{agent} @ {handle}`), not just the raw address.
