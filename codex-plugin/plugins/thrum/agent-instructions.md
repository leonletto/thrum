# Install Thrum Codex Plugin — Agent Instructions

> Read this file end-to-end before acting. Your user wants you to install the Thrum codex plugin on their machine. The interactive steps at the end (codex restart + `/hooks` approval) must be done by the user — everything else you can handle deterministically with the Bash tool.

## Context

Thrum is a Git-backed messaging system for AI agent coordination. The Thrum codex plugin provides three lifecycle hooks (SessionStart auto-prime, PreToolUse safety block, Stop unread-inbox check) plus a library of role-discipline skills.

Codex 0.130.0's `codex plugin marketplace add` registers a marketplace's metadata but does NOT auto-populate the per-plugin cache at `~/.codex/plugins/cache/<marketplace>/<plugin>/<version>/`. Without that cache, codex can't load the plugin's hooks. The install script in this directory handles that gap.

## Prereqs to verify

Run these checks before proceeding. If any fail, stop and tell the user what's missing — don't try to install the prereqs yourself.

```bash
command -v codex >/dev/null && echo "✓ codex" || echo "✗ codex CLI not on PATH"
command -v jq    >/dev/null && echo "✓ jq"    || echo "✗ jq not on PATH (brew install jq)"
test -f "$HOME/.codex/config.toml" && echo "✓ codex config" || echo "✗ codex config missing — run codex once to create it"
codex --version 2>&1 | head -1
```

Codex must be `0.130.0` or newer.

## Run the installer

The installer is shipped inside the plugin itself. The simplest path is to clone the repo (or use an already-cloned copy) and run it. The script is idempotent — safe to re-run.

```bash
# If user already has the thrum repo cloned, use it:
THRUM_REPO_PATH="${THRUM_REPO_PATH:-}"
if [ -z "$THRUM_REPO_PATH" ]; then
  # Otherwise, ephemerally clone to a temp dir
  THRUM_REPO_PATH="$(mktemp -d)/thrum"
  git clone --depth 1 --branch thrum-dev https://github.com/leonletto/thrum "$THRUM_REPO_PATH"
fi

bash "$THRUM_REPO_PATH/codex-plugin/plugins/thrum/scripts/install-plugin.sh"
```

The script will:

1. Verify codex + jq are installed
2. Register the `thrum-marketplace` (or upgrade it if already registered) from `https://github.com/leonletto/thrum` at the configured ref (default: `thrum-dev`)
3. Read the plugin version from the staged manifest
4. Stage the plugin into `~/.codex/plugins/cache/thrum-marketplace/thrum/<version>/`
5. Add `[plugins."thrum@thrum-marketplace"] enabled = true` to `~/.codex/config.toml`
6. Add `plugin_hooks = true` under `[features]` in `~/.codex/config.toml`

If any step fails, the script prints `ERROR: …` and exits non-zero. Report the failure to the user verbatim — do not retry without understanding the cause.

## After the script succeeds

The script prints a "Next steps" block. Surface that block to the user — it contains the interactive steps only they can do:

1. Restart codex in a fresh shell (your codex agent, their IDE codex, whatever the user is running).
2. On first launch codex shows: `⚠ 3 hooks need review before they can run. Open /hooks to review them.`
3. The user runs `/hooks` in codex. For each of PreToolUse, SessionStart, Stop: press Enter to view, `t` to trust, Escape to go back.
4. Restart codex once more. The SessionStart hook then auto-loads the thrum prime briefing on every future session.

## Tell the user when you're done

Send a short status message:

> Thrum codex plugin installed at `~/.codex/plugins/cache/thrum-marketplace/thrum/<version>/`. Restart codex and approve the 3 hooks via `/hooks` (one-time security gate). Detailed steps in the script's output above.

## What to do if it fails

- **`codex CLI not found`**: tell the user to install codex from <https://github.com/openai/codex> (or the OpenAI Codex App).
- **`jq not found`**: tell the user `brew install jq` (macOS) or their platform equivalent.
- **`plugin manifest not found at ...`**: codex's marketplace staging layout has likely changed. Stop and report this — it's a real plugin-side bug to file, not a user-fixable issue.
- **`marketplace upgrade failed` / `marketplace add failed`**: usually a network or auth issue with GitHub. Report the underlying codex error verbatim.

Do not invent workarounds. If the script fails in a way these instructions don't cover, report verbatim to the user and ask how to proceed.

## What you should NOT do

- Don't `cp -R` the cache manually — use the script.
- Don't edit `~/.codex/config.toml` by hand — the script handles it idempotently.
- Don't try to drive codex's `/hooks` UI on the user's behalf via tmux send-keys — the hook trust gate is a security boundary; the user approves their own hooks.
- Don't push the user to install codex if they don't have it — your job is the thrum plugin, not codex itself.
