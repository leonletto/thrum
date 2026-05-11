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

## Recommended: one-command install

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/thrum-dev/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

That's it. The script registers the marketplace, stages the per-plugin cache (a step codex 0.130.0 doesn't do automatically for third-party marketplaces), enables the plugin, and turns on the `plugin_hooks` feature. It's idempotent — re-run any time to pull the latest revision.

To pin a release tag instead of `thrum-dev`:

```bash
THRUM_INSTALL_REF=v0.10.3 bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/v0.10.3/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

If you have the repo cloned already, you can run it locally instead:

```bash
bash ./codex-plugin/plugins/thrum/scripts/install-plugin.sh
```

After the script completes, follow the "First-run hook approval" steps below.

### Have an AI agent do it

If you have an AI assistant (claude, codex, kiro, etc.) running locally, point it at the agent-instructions doc — it handles everything up to the manual `/hooks` approval:

```
Please install the Thrum codex plugin by following:
https://github.com/leonletto/thrum/blob/thrum-dev/codex-plugin/plugins/thrum/agent-instructions.md
```

Your agent will read the file, run the installer, and tell you when it's time to restart codex and approve hooks.

## Manual: low-level marketplace flow

If you'd rather drive the install steps yourself:

```bash
# 1. Register marketplace
codex plugin marketplace add leonletto/thrum --ref thrum-dev

# 2. Stage cache (codex 0.130.0 doesn't do this for third-party marketplaces)
VERSION=$(jq -r '.version' ~/.codex/.tmp/marketplaces/thrum-marketplace/codex-plugin/plugins/thrum/.codex-plugin/plugin.json)
mkdir -p ~/.codex/plugins/cache/thrum-marketplace/thrum/$VERSION
cp -R ~/.codex/.tmp/marketplaces/thrum-marketplace/codex-plugin/plugins/thrum/. ~/.codex/plugins/cache/thrum-marketplace/thrum/$VERSION/

# 3. Enable plugin + plugin_hooks feature
printf '\n[plugins."thrum@thrum-marketplace"]\nenabled = true\n' >> ~/.codex/config.toml
# Then add plugin_hooks = true under [features] in ~/.codex/config.toml
```

The marketplace manifest at the repo root (`<repo>/.agents/plugins/marketplace.json`) points at `./codex-plugin/plugins/thrum` — codex's Git-source flow looks for marketplace.json at the staging root, so the repo-root manifest is required (the `codex-plugin/.agents/plugins/marketplace.json` is kept for local-source installs from a clone).

To upgrade later:

```bash
codex plugin marketplace upgrade thrum-marketplace
# Then re-stage the cache (steps 2-3 above), or just re-run install-plugin.sh.
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
grep -A1 '^\[marketplaces.thrum-marketplace\]' ~/.codex/config.toml   # should print the registered source
codex features list | grep -E '^(hooks|plugin_hooks) '                # hooks=stable/true; plugin_hooks=under development/true
ls ~/.codex/plugins/cache/thrum-marketplace/thrum/                    # should list one or more <version> dirs
```

You should see the thrum umbrella skill plus the role/discipline skills.

## Migration from `~/.codex/skills/` (one-time)

If you previously installed thrum via the legacy script:

```bash
mv ~/.codex/skills/thrum* ~/.agents/skills/
mv ~/.codex/skills/orchestrate ~/.agents/skills/
```

(See pm7n.2 for context on the codex v0.130.0 path change.)
