
## Playwright CLI Setup Guide

Playwright CLI is a Claude Code skill that lets agents automate browser
interactions — navigate pages, take screenshots, fill forms, and extract
content. It's useful for capturing visual context, verifying web UI changes, and
testing browser-based features during agent workflows.

### Why Use It

AI agents work with code, but sometimes they need to see or interact with what
the code produces. Common use cases with Thrum:

- **Capture context** — Screenshot a web UI before and after changes, saving
  visual evidence of what was modified
- **Verify changes** — After an agent implements a UI fix, it can open the page
  and confirm the rendering is correct
- **Extract data** — Pull text, network responses, or DOM state from a running
  web application
- **Fill forms and test flows** — Drive a browser through user flows to validate
  end-to-end behavior

### Installation

Playwright CLI runs as a Claude Code skill. There are two ways to set it up:

#### Option 1: Playwright MCP Plugin (Recommended)

The official Playwright MCP plugin from Microsoft provides browser tools
directly to Claude Code:

1. Install the plugin in Claude Code:
   ```
   claude plugin add playwright
   ```

2. The plugin automatically registers MCP tools like `browser_navigate`,
   `browser_click`, `browser_snapshot`, and `browser_take_screenshot`.

3. No additional configuration needed — the plugin runs
   `npx @playwright/mcp@latest` as the MCP server.

#### Option 2: Playwright CLI Skill

If you have the `playwright-cli` binary installed, you can use it via a Claude
Code skill:

1. Install the skill:
   ```bash
   playwright-cli install --skills
   ```

2. Allow the skill in your project's `.claude/settings.local.json`:
   ```json
   {
     "permissions": {
       "allow": ["Skill(playwright-cli)"]
     }
   }
   ```

3. The skill gives agents access to `Bash(playwright-cli:*)` commands.

### Core Commands

Whether using the MCP plugin or the CLI skill, the capabilities are similar:

#### Navigation

```bash
# Open a URL
playwright-cli open https://localhost:3000

# Navigate to a page
playwright-cli goto https://localhost:3000/dashboard

# Go back
playwright-cli back
```

#### Screenshots

```bash
# Screenshot the current viewport
playwright-cli screenshot

# Screenshot a specific element
playwright-cli screenshot --selector ".dashboard-header"

# Full-page screenshot
playwright-cli screenshot --full-page

# Save to a specific file
playwright-cli screenshot --output dashboard.png
```

#### Page Inspection

```bash
# Get an accessibility snapshot (structured page content)
playwright-cli snapshot

# Evaluate JavaScript on the page
playwright-cli eval "document.title"
playwright-cli eval "document.querySelectorAll('.task-card').length"
```

#### Interaction

```bash
# Click an element (by reference from snapshot)
playwright-cli click e3

# Fill a text field
playwright-cli fill e5 "search query"

# Type text (triggers key handlers)
playwright-cli type "hello world"

# Press a key
playwright-cli press Enter
```

#### DevTools

```bash
# View console messages
playwright-cli console

# View network requests
playwright-cli network

# Start/stop tracing
playwright-cli tracing-start
playwright-cli tracing-stop --output trace.zip
```

### Usage with Thrum Agents

A common pattern for agents working on web UI tasks:

```bash
# 1. Agent claims a UI task
bd update <id> --status=in_progress
thrum send "Starting UI task <id>" --to @coordinator

# 2. Open the app and capture the "before" state
playwright-cli open http://localhost:65018
playwright-cli screenshot --output before.png

# 3. Make code changes...

# 4. Reload and capture the "after" state
playwright-cli goto http://localhost:65018
playwright-cli screenshot --output after.png

# 5. Verify the change visually
playwright-cli snapshot    # check accessibility tree

# 6. Close and report
playwright-cli close
bd close <id>
thrum send "Completed <id> — UI verified with screenshots" --to @coordinator
```

### Multiple Browser Sessions

Use named sessions to manage multiple browser windows:

```bash
# Open two sessions
playwright-cli -s=app open http://localhost:3000
playwright-cli -s=docs open http://localhost:8080

# Interact with a specific session
playwright-cli -s=app screenshot
playwright-cli -s=docs click e5

# Close a specific session
playwright-cli -s=app close
```

### Tips

- **Use `snapshot` over `screenshot`** when you need to interact with elements.
  Snapshots return an accessibility tree with element references (`e1`, `e2`,
  etc.) that you can use with `click`, `fill`, and other commands.
- **Screenshots are for context** — save them when you need visual evidence of
  a state change, but use snapshots for programmatic page interaction.
- **Named sessions** are useful when testing multi-page flows or comparing two
  different views side by side.
- **The MCP plugin and CLI skill coexist** — you can have both installed
  simultaneously. The MCP plugin provides `mcp__playwright__*` tools, while the
  skill uses `Bash(playwright-cli:*)`.

### Further Reading

- [Playwright MCP](https://github.com/microsoft/playwright-mcp) — Official
  Microsoft Playwright MCP server
- [Web UI Documentation](../web-ui.md) — Thrum's built-in web UI
- [Recommended Tools](recommended-tools.md) — Overview of all recommended tools
