## Thrum Beta Channel

Every Thrum release goes through a soak window as `-rc.N` (release candidate)
before being promoted to stable. Beta users help catch regressions before they
hit `releases/latest`. This guide covers how to opt in, what to expect, and how
to report what you find.

> **Current pre-release:
> [`v0.10.6-rc.1`](https://github.com/leonletto/thrum/releases/tag/v0.10.6-rc.1)**
> (in soak). Headline: the **sync re-architecture** (thrum-s6os) — the
> cross-machine wire stream is now event-triggered rather than 60-second-polled,
> idle daemons produce zero commits on `a-sync`, and busy clusters stop
> accumulating heartbeat noise. ⚠ **Mixed-cluster warning:** v0.10.5 peers
> cannot read messages from upgraded v0.10.6 peers (v0.10.6 writes only to the
> new `messages-v2/` path); upgrade all peers in one window. Also new:
> timestamped pre-migration DB backups (with a hard halt if backup fails), an
> RC-tag plugin distribution scheme so `/plugin update` upgrades cleanly through
> the rc pipeline, two new daemon config keys (`events_retention_days`,
> `compaction_size_threshold_mb`), and skill-review-loop improvements
> (`verify-against-source`, Phase 0 review gate). Full notes:
> [What's New](whats-new.md) and the
> [CHANGELOG `[Unreleased]` section](https://github.com/leonletto/thrum/blob/main/CHANGELOG.md).

### Quick install for `v0.10.6-rc.1`

Binary and Codex plugin (run in your shell):

```bash
# Binary
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | VERSION=v0.10.6-rc.1 sh

# Codex plugin (matches release/v0.10.6)
THRUM_INSTALL_REF=release/v0.10.6 bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/release/v0.10.6/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

Claude Code plugin (run inside Claude):

```text
/plugin marketplace add leonletto/thrum#release/v0.10.6
/plugin install thrum@thrum
/reload-plugins
```

For refresh between rc.N bumps, switch-back to stable, and the parameterized
versions of these commands, see
[How to install the matching plugins](#how-to-install-the-matching-claude-code-and-codex-plugins)
below.

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

## How to install the matching Claude Code and Codex plugins

The plugins live in the same repo as the binary, so the release branch
(`release/vX.Y.Z`) carries the plugin payload that matches the RC binary you
just installed. Install from that branch instead of the default marketplace so
the plugin's slash commands, hooks, and skills stay in lockstep with the daemon.

### Claude Code plugin

Inside Claude Code, add the marketplace pinned to the release branch using the
`<github-user>/<repo>#<branch>` shorthand, then install the plugin:

```text
/plugin marketplace add leonletto/thrum#release/vX.Y.Z
/plugin install thrum@thrum
/reload-plugins
```

The first command registers the marketplace named `thrum` and pins it to the
release branch. The second installs the `thrum` plugin from that marketplace
(`<plugin-name>@<marketplace-name>`).

When a new rc.N drops on the same release branch, refresh the marketplace
without re-adding it:

```text
/plugin marketplace update thrum
/reload-plugins
```

If you've previously installed the stable marketplace and want to switch back to
it after a release ships, remove the beta marketplace first:

```text
/plugin marketplace remove thrum
/plugin marketplace add leonletto/thrum
/plugin install thrum@thrum
```

### Codex plugin

The Codex installer accepts a `THRUM_INSTALL_REF` env var. Set it to the release
branch and run the installer from that same branch so both the script and the
plugin payload come from the same revision:

```bash
THRUM_INSTALL_REF=release/vX.Y.Z bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/release/vX.Y.Z/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

When a new rc.N drops on the same release branch, re-run the same command — the
installer is idempotent on size+mtime and re-stages the cache from the latest
revision of the branch.

To switch back to stable after a release ships, rerun the installer with the
default ref:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/thrum-dev/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

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

When you're done testing, revert the binary and any plugins you installed from
the release branch back to their stable counterparts.

### Binary

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
thrum daemon restart
thrum version
```

Stable releases come without a `-rc.N` suffix. Once you're back on stable,
you'll only receive non-prerelease updates until you opt back in with a specific
`VERSION=`.

### Claude Code plugin

If you installed the plugin from a release branch (via
`leonletto/thrum#release/vX.Y.Z`), remove that marketplace and re-add the stable
one before re-installing:

```text
/plugin marketplace remove thrum
/plugin marketplace add leonletto/thrum
/plugin install thrum@thrum
/reload-plugins
```

The remove step is required — Claude Code keeps the cached source URL with the
branch ref pinned, so re-adding without removing first leaves you on the same
beta source.

### Codex plugin

Rerun the installer without `THRUM_INSTALL_REF`, pulling from `thrum-dev`:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/thrum-dev/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

The installer is idempotent and re-stages the cache, so the previous branch
state is overwritten in place. No remove step is needed for the Codex plugin.

> **Note:** the Codex plugin currently tracks `thrum-dev` rather than a
> versioned release tag. Once the plugin starts shipping versioned releases (the
> way the Claude plugin does via `marketplace.json`), the revert command here
> will gain a version pin similar to the Claude flow above.

## Note for Homebrew users

The beta channel is curl-only. The Homebrew tap doesn't carry prereleases —
`homebrew_casks.skip_upload: auto` in `.goreleaser.yaml` keeps RCs off the tap
so `brew upgrade thrum` only ever moves you between stable releases.

If you usually install via `brew install leonletto/tap/thrum` and want to test
an RC, run the curl command above; the install script writes to
`~/.local/bin/thrum`, which takes precedence over the Homebrew binary on most
`PATH` setups. To return to the Homebrew-managed binary, remove the local copy
and run `brew upgrade thrum`.
