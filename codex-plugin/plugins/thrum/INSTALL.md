# Installing Thrum for Codex

## Prerequisites

- Codex installed (v0.130.0+)
- `features.hooks` is enabled by default on codex 0.130.0+; no action needed. (If somehow disabled: `codex -c features.hooks=true ...`.)
- `features.plugin_hooks = true` MUST be set in `~/.codex/config.toml` for the
  plugin's SessionStart/PreToolUse/Stop hooks to register. As of codex
  0.130.0 this feature is "under development" but functional. Add to your
  `[features]` block:
  ```toml
  [features]
  plugin_hooks = true
  ```
- `thrum` CLI on `PATH`

## Recommended: Marketplace install (Git source)

```bash
codex plugin marketplace add leonletto/thrum --ref thrum-dev
# Then enable the plugin:
printf '\n[plugins."thrum@thrum-marketplace"]\nenabled = true\n' >> ~/.codex/config.toml
```

This stages the thrum plugin into `~/.codex/plugins/cache/thrum-marketplace/thrum/<version>/` and registers the marketplace in `~/.codex/config.toml`. Use `--ref v0.10.3` (or any release tag) for stable installs; `--ref thrum-dev` for pre-release.

The marketplace manifest at the repo root (`<repo>/.agents/plugins/marketplace.json`) points at `./codex-plugin/plugins/thrum` — codex's Git-source flow looks for marketplace.json at the staging root, so the repo-root manifest is required (the `codex-plugin/.agents/plugins/marketplace.json` is kept for local-source installs from a clone).

To upgrade later:

```bash
codex plugin marketplace upgrade thrum-marketplace
```

## Alternative: Local-clone install (dev only)

For users on a clone of the thrum repo or developing the plugin, the simplest path is to install skills directly:

```bash
./codex-plugin/plugins/thrum/scripts/install-skills.sh --force
```

Installs skills directly into `${HOME}/.agents/skills/` (canonical path as of codex v0.130.0).

To wire the plugin's **hooks** from a local clone via the marketplace mechanism:

```bash
codex plugin marketplace add ./codex-plugin
# Then enable the plugin (codex 0.130.0 does not auto-enable local sources):
printf '\n[plugins."thrum@thrum-marketplace"]\nenabled = true\n' >> ~/.codex/config.toml
```

NOTE: codex 0.130.0's local-source `marketplace add` registers the marketplace but does NOT stage the plugin into `~/.codex/plugins/cache/`. To populate the cache for hook firing from a local clone, either (a) use the Git-source flow above instead (recommended for runtime testing), or (b) manually stage:
```bash
mkdir -p ~/.codex/plugins/cache/thrum-marketplace
cp -R ./codex-plugin/plugins/thrum ~/.codex/plugins/cache/thrum-marketplace/thrum
```

## First-run hook approval

On the first codex session after install, codex will display:

> ⚠ 3 hooks need review before they can run. Open /hooks to review them.

Open `/hooks` and approve all three (SessionStart, PreToolUse, Stop). Codex
remembers the approval for subsequent sessions.

## SessionStart auto-prime

After install + hook approval, restart codex. The next session opens with the
Thrum prime briefing already in context — identity, project state, unread
inbox — courtesy of the SessionStart hook.

## Verification

```bash
codex plugin marketplace list   # should include thrum-marketplace
codex features list | grep '^hooks'    # should be: stable / true
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
