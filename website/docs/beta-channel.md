---
title: "Beta Channel"
description:
  "How to opt into Thrum pre-release builds, install RCs, report bugs against
  them, and roll back to stable"
category: "guides"
order: 50
tags: ["beta", "pre-release", "rc", "install", "rollback", "soak"]
last_updated: "2026-05-11"
---

## Thrum Beta Channel

Every Thrum release goes through a soak window as `-rc.N` (release candidate)
before being promoted to stable. Beta users help catch regressions before they
hit `releases/latest`. This guide covers how to opt in, what to expect, and how
to report what you find.

> **Current pre-release: `v0.10.3-rc.1`** (tagged 2026-05-11, in soak).
> Highlights: codex plugin first-class, post-launch tmux silence watchdog,
> first-launch trust-gate detection, self-echo nudge fix. Full notes:
> [What's New](whats-new.md) and the
> [CHANGELOG `[Unreleased]` section](https://github.com/leonletto/thrum/blob/main/CHANGELOG.md).
> To install:
> `curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | VERSION=v0.10.3-rc.1 sh`.

## What this is

When a new version is ready for soak, the coordinator cuts a `release/vX.Y.Z`
branch and tags `vX.Y.Z-rc.1`. GoReleaser publishes that tag as a **GitHub
prerelease** — separate from the stable releases page. RCs sit in soak for at
least 48 hours, get exercised in 3+ real projects, and only graduate to stable
once zero P0/P1 bugs are open against them.

You can opt into running these RCs at any time. Doing so gives you the release a
few days early in exchange for being on the leading edge.

## What to expect

- **RCs may have known issues.** The whole point of soak is to surface problems;
  don't be surprised if you hit one.
- **Daemon migrations may need additional re-soak.** A bugfix that touches
  daemon, sync, identity, migration, or storage projection paths re-starts a
  full 48h soak window. Less risky areas (CLI, hooks, docs) re-soak for 24h.
- **Report what you find.** The faster a bug is filed, the faster the next rc.N
  rolls.
- **Stable users are unaffected.** If you don't opt into the beta channel,
  nothing changes for you.

## How to install an RC

The curl install path supports a `VERSION=` env var for opting into a specific
tag, including prereleases. **Place `VERSION=` on the `sh` side of the pipe, not
the `curl` side** — otherwise the variable is set on the `curl` process and the
script never sees it (it falls back to `latest`):

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | VERSION=vX.Y.Z-rc.N sh
```

To find the current RC tag, browse the
[GitHub releases page](https://github.com/leonletto/thrum/releases) and look for
entries marked **Pre-release**. The RC pattern is `vX.Y.Z-rc.N` (e.g.
`v0.11.0-rc.2`).

After installing, restart the daemon so it picks up the new binary:

```bash
thrum daemon restart
thrum version
```

## How to upgrade between RCs

When a new rc.N drops (e.g. rc.1 → rc.2 because a bugfix landed), re-run the
same command with the new VERSION:

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | VERSION=vX.Y.Z-rc.N sh
thrum daemon restart
```

You don't need to uninstall the previous RC first — the install script
overwrites the binary in place.

## How to roll back to stable

Before opting into the beta channel for the first time, take a backup of your
`.thrum/` state directory in case you need to revert:

```bash
cp -r .thrum .thrum.pre-rc-$(date +%F)
```

To roll back, install the previous stable version (use `vX.Y.Z` without the
`-rc.N` suffix) and restart the daemon:

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | VERSION=v0.10.2 sh
thrum daemon restart
```

If you've created data on an RC and want to revert, restoring `.thrum/` from
your pre-RC backup is the safe path (see _Data safety_ below).

## Data safety

Rollback across a migration is not guaranteed. We test downgrade as part of
every release's test cycle on a best-effort basis, but if you've created data on
an RC and want to revert, restoring from your pre-RC backup is the safe path.

```bash
# Restore from backup (created before opting into beta)
mv .thrum .thrum.rc-discarded
cp -r .thrum.pre-rc-2026-05-01 .thrum
thrum daemon restart
```

If a specific RC's downgrade test failed during pre-release, the failure mode
will be called out in that release's notes. Always read the release notes for
the RC you're installing.

## Reporting bugs

File RC bugs at the GitHub issue tracker with the `rc-feedback` label preset:

[https://github.com/leonletto/thrum/issues/new?labels=rc-feedback](https://github.com/leonletto/thrum/issues/new?labels=rc-feedback)

Include:

- **RC version** — output of `thrum --version`
- **Daemon version** — output of `thrum daemon status --json | jq -r .version`
- **Operating system** — `uname -a` or equivalent
- **Reproduction steps** — minimum sequence of commands that triggers the bug.
  If the bug is intermittent, note how often you've seen it
- **What you expected vs. what happened**

The `rc-feedback` label is what the coordinator filters on when triaging during
soak — labelled issues block promotion at P0/P1; unlabelled bug reports may not
be seen until after stable ships.

## Leaving the beta channel

When you're done testing, install the latest stable and restart the daemon.
You're done — no further configuration needed:

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
thrum daemon restart
thrum version
```

Stable releases come without a `-rc.N` suffix. Once you're back on stable,
you'll only receive non-prerelease updates until you opt back in with a specific
`VERSION=`.

## Note for Homebrew users

The beta channel is curl-only. The Homebrew tap doesn't carry prereleases —
`homebrew_casks.skip_upload: auto` in `.goreleaser.yaml` keeps RCs off the tap
so `brew upgrade thrum` only ever moves you between stable releases.

If you usually install via `brew install leonletto/tap/thrum` and want to test
an RC, run the curl command above; the install script writes to
`~/.local/bin/thrum`, which takes precedence over the Homebrew binary on most
`PATH` setups. To return to the Homebrew-managed binary, remove the local copy
and run `brew upgrade thrum`.
