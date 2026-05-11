# Install Thrum Codex Plugin — Agent Instructions

> Read this file end-to-end before acting. Your user wants the Thrum codex plugin installed. The script does everything except the final interactive `/hooks` approval, which only the user can do.

## Context

Thrum is a Git-backed messaging system for AI agent coordination. The Thrum codex plugin provides three lifecycle hooks (SessionStart auto-prime, PreToolUse safety block, Stop unread-inbox check) plus a library of role-discipline skills.

Codex 0.130.0's `codex plugin marketplace add` registers a marketplace's metadata but does NOT auto-populate the per-plugin cache at `~/.codex/plugins/cache/<marketplace>/<plugin>/<version>/`. Without that cache, codex can't load the plugin's hooks. The installer below handles that gap; once codex ships cache auto-staging for third-party marketplaces, this whole flow collapses to a single `marketplace add`.

## Prereqs to verify

```bash
command -v codex >/dev/null && echo "ok codex" || echo "MISSING codex CLI on PATH"
command -v jq    >/dev/null && echo "ok jq"    || echo "MISSING jq (brew install jq)"
test -f "$HOME/.codex/config.toml" && echo "ok codex config" || echo "MISSING codex config — run codex once to create it"
codex --version 2>&1 | head -1
```

Codex must be `0.130.0` or newer. If any prereq fails, stop and tell the user — don't try to install codex or jq yourself.

## Run the installer (one command)

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/thrum-dev/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

Defaults install from the `thrum-dev` branch. To pin a release tag instead:

```bash
THRUM_INSTALL_REF=v0.10.3 bash <(curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/v0.10.3/codex-plugin/plugins/thrum/scripts/install-plugin.sh)
```

The script is idempotent — re-running it pulls the latest revision of the configured ref and re-stages the cache. It will:

1. Verify codex + jq are installed.
2. Register `thrum-marketplace` from `https://github.com/leonletto/thrum` (or upgrade it if already registered).
3. Read the plugin version from the staged manifest.
4. Stage the plugin into `~/.codex/plugins/cache/thrum-marketplace/thrum/<version>/`.
5. Add `[plugins."thrum@thrum-marketplace"] enabled = true` to `~/.codex/config.toml`.
6. Add `plugin_hooks = true` under `[features]` in `~/.codex/config.toml`.

If any step fails, the script prints `ERROR: …` and exits non-zero. Report the failure to the user verbatim — do not retry or invent workarounds.

## After the script succeeds

The script prints a "Next steps" block. Surface that block to the user — it contains the interactive steps only they can do:

1. Restart codex in a fresh shell (their terminal codex, IDE codex, whatever they're running).
2. On first launch codex shows: `3 hooks need review before they can run. Open /hooks to review them.`
3. The user runs `/hooks` in codex. For each of PreToolUse, SessionStart, Stop: press Enter to view, `t` to trust, Escape to go back.
4. Restart codex once more. The SessionStart hook then auto-loads the thrum prime briefing on every future session.

## Tell the user when you're done

Send a short status:

> Thrum codex plugin installed at `~/.codex/plugins/cache/thrum-marketplace/thrum/<version>/`. Restart codex and approve the 3 hooks via `/hooks` (one-time security gate). Detailed steps in the script's output above.

## What to do if it fails

- **`codex CLI not found`**: tell the user to install codex from <https://github.com/openai/codex> (or the OpenAI Codex App).
- **`jq not found`**: tell the user `brew install jq` (macOS) or their platform equivalent.
- **`plugin manifest not found at ...`**: codex's marketplace staging layout has likely changed. Stop and report this — it's a real plugin-side bug to file, not a user-fixable issue.
- **`marketplace upgrade failed` / `marketplace add failed`**: usually a network or auth issue with GitHub. Report the underlying codex error verbatim.

## What you should NOT do

- Don't `cp -R` the cache manually — use the script.
- Don't edit `~/.codex/config.toml` by hand — the script handles it idempotently.
- Don't try to drive codex's `/hooks` UI on the user's behalf via tmux send-keys — the hook trust gate is a security boundary; the user approves their own hooks.
- Don't push the user to install codex if they don't have it — your job is the thrum plugin, not codex itself.
