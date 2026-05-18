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

> Apple support article:
> [Sign in with app-specific passwords](https://support.apple.com/HT204397)

---

## Step 2 — IMAP / SMTP Host Configuration

| Setting     | Value                                                |
| ----------- | ---------------------------------------------------- |
| IMAP host   | `imap.mail.me.com`                                   |
| IMAP port   | `993` (TLS)                                          |
| SMTP host   | `smtp.mail.me.com`                                   |
| SMTP port   | `587` (STARTTLS)                                     |
| Auth method | `PLAIN` over STARTTLS                                |
| Username    | Full Apple ID address (e.g. `thrum-mesh@icloud.com`) |

> **Note:** Apple also supports port `465` (implicit TLS) for SMTP. Use `587`
> with STARTTLS unless your network blocks outbound port 587.

---

## Step 3 — Run `thrum email init`

The interactive wizard prompts for the app-specific password (in
`xxxx-xxxx-xxxx-xxxx` format) and writes `.thrum/secrets/email.json` (mode
`0600`):

```bash
thrum email init --provider icloud
```

For scripted / non-interactive setup:

```bash
thrum email init \
  --provider icloud \
  --non-interactive \
  --password "xxxx-xxxx-xxxx-xxxx" \
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

**`[AUTHENTICATIONFAILED] Authentication failed`** The app-specific password was
entered incorrectly. iCloud passwords include hyphens; paste the full
`xxxx-xxxx-xxxx-xxxx` string without spaces. Revoke the existing slot at
[appleid.apple.com](https://appleid.apple.com) and generate a new one.

**`[UNAVAILABLE] Service temporarily unavailable`** iCloud IMAP has brief
outages. Check
[Apple System Status](https://www.apple.com/support/systemstatus/) and retry
after a few minutes.

**IMAP works but SMTP fails** Verify SMTP username is the full Apple ID address.
iCloud rejects short-form usernames on the SMTP AUTH step. Also confirm port 587
is reachable — some ISPs block it. Try port 465 (implicit TLS) if 587 is
blocked.

**App-specific password expired** Apple revokes all app-specific passwords when
you change your Apple ID password. Generate a fresh one and either re-run the
wizard or edit the secrets file directly:

```bash
# Option A: re-run the wizard with the new password
thrum email init --provider icloud --non-interactive \
  --password "<new app-specific password>" \
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

iCloud Mail handles SPF and DKIM automatically for `@icloud.com` and `@me.com`
senders. Custom-domain iCloud+ addresses follow the same policy; no additional
DNS records are needed.

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
