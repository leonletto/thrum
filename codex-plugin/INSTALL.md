# Installing Thrum for Codex

## Prerequisites

- Codex installed (v0.130.0+)
- `features.codex_hooks = true` in `~/.codex/config.toml` (auto-set by install if absent)
- `thrum` CLI on `PATH`

## Recommended: Marketplace install

```bash
codex plugin marketplace add leonletto/thrum --sparse codex-plugin --ref v0.10.3
```

This installs the thrum plugin into `~/.agents/plugins/cache/thrum-marketplace/thrum/v0.10.3/` and enables it (`[plugins."thrum@thrum-marketplace"] enabled = true` is added to your `~/.codex/config.toml`).

To upgrade later:

```bash
codex plugin marketplace upgrade thrum-marketplace
```

## Alternative: Local-clone install (legacy / dev)

For users on a clone of the thrum repo or developing the plugin:

```bash
./codex-plugin/scripts/install-skills.sh --force
```

Installs skills directly into `${HOME}/.agents/skills/` (canonical path as of codex v0.130.0).

To wire the plugin's hooks/skills via the marketplace mechanism instead:

```bash
codex plugin marketplace add ./codex-plugin
# Then enable the plugin (codex 0.130.0 does not auto-enable local sources):
printf '\n[plugins."thrum@thrum-marketplace"]\nenabled = true\n' >> ~/.codex/config.toml
```

## SessionStart auto-prime

After install, restart codex. The next session opens with the Thrum prime briefing already in context — identity, project state, unread inbox.

## Verification

```bash
codex plugin marketplace list   # should include thrum-marketplace
codex features list | grep codex_hooks    # should be: stable: true
ls ~/.agents/skills | grep -E '^(thrum|adversarial-|coordinator-|implementer-|researcher-|configure-|efficient-|project-|verify-)'
```

You should see the thrum umbrella skill plus the role/discipline skills.

## Migration from `~/.codex/skills/` (one-time)

If you previously installed thrum via the legacy script:

```bash
mv ~/.codex/skills/thrum* ~/.agents/skills/
mv ~/.codex/skills/orchestrate ~/.agents/skills/
```

(See pm7n.2 for context on the codex v0.130.0 path change.)
