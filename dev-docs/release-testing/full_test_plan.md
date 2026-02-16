# Thrum Release Test Plan

Self-contained testing plan for thrum using two pre-existing test worktrees. All tests run within the thrum repository itself—no external projects needed.

## Test Environment

| Worktree | Branch | Path | Agent Identity |
|----------|--------|------|----------------|
| main | `main` | `/Users/leon/dev/opensource/thrum` | — |
| test-coordinator | `test/coordinator` | `~/.workspaces/thrum/test-coordinator` | test-coordinator |
| test-implementer | `test/implementer` | `~/.workspaces/thrum/test-implementer` | test-implementer |

**Important Notes:**
- Self-send is filtered by design—inbox excludes your own messages automatically
- Use `THRUM_NAME` env var to select identity in multi-agent worktrees
- The main repo's marketplace is at `/Users/leon/dev/opensource/thrum/.claude-plugin/marketplace.json`
- PreCompact hook script bundled at `${CLAUDE_PLUGIN_ROOT}/scripts/pre-compact-save-context.sh`
- Pre-compact backups: `/tmp/thrum-pre-compact-{name}-{role}-{module}-{epoch}.md`

---

## Part A: Build & Prerequisites

### A1. Verify tmux Installation

```bash
which tmux && echo "OK" || brew install tmux
tmux -V
# Expected: tmux 3.x or higher
```

### A2. Build from Source

```bash
cd /Users/leon/dev/opensource/thrum
make install
# Expected: Built and installed to ~/.local/bin/thrum

thrum --version
# Expected: thrum version 0.4.2+<hash> (or current version)

which thrum
# Expected: /Users/leon/dev/opensource/thrum/.bin/thrum (or ~/.local/bin/thrum)
```

---

## Part B: Plugin Management

### B1. Uninstall Existing Plugin

```bash
claude plugin uninstall thrum
# Expected: Plugin removed (or "not installed" if fresh)

# Verify removal
cat ~/.claude/plugins/installed_plugins.json 2>/dev/null | grep -q thrum && echo "FAIL: still installed" || echo "OK: removed"
```

### B2. Add Local Marketplace

```bash
# Use the marketplace at the repo root
claude plugin marketplace add /Users/leon/dev/opensource/thrum
# Expected: Marketplace added

claude plugin marketplace list
# Expected: Shows "thrum" marketplace with path
```

### B3. Install from Local Marketplace

```bash
claude plugin install thrum@thrum
# Expected: Installed thrum plugin version 0.4.2

cat ~/.claude/plugins/installed_plugins.json | grep thrum
# Expected: Shows thrum@thrum with installPath
```

### B4. Verify Plugin Metadata

```bash
cat ~/.claude/plugins/thrum@thrum/.claude-plugin/plugin.json
# Expected: Shows hooks (SessionStart, PreCompact), version 0.4.2
```

---

## Part C: Init & Daemon

### C1. Initialize Main Repo

```bash
cd /Users/leon/dev/opensource/thrum

# Clean slate
rm -rf .thrum 2>/dev/null
rm -f .claude/settings.json 2>/dev/null

thrum init
# Expected: "Initialized thrum in /Users/leon/dev/opensource/thrum"

# CRITICAL: Verify .claude/settings.json does NOT have mcpServers (thrum-5611 fix)
if [ -f .claude/settings.json ]; then
  grep -q mcpServers .claude/settings.json && echo "FAIL: mcpServers written" || echo "OK: no mcpServers"
else
  echo "OK: no settings.json created"
fi
```

### C2. Start Daemon

```bash
cd /Users/leon/dev/opensource/thrum

thrum daemon start
# Expected: Daemon started (PID: <number>)

thrum daemon status
# Expected: Daemon running (PID: <number>, uptime: <time>)
```

### C3. Verify Daemon Health

```bash
thrum daemon status --json
# Expected: JSON output with running=true, pid, uptime
```

---

## Part D: Agent Registration

### D1. Initialize Test Worktrees

```bash
# Coordinator worktree
cd ~/.workspaces/thrum/test-coordinator
rm -rf .thrum 2>/dev/null

thrum init
# Expected: "Worktree detected — set up redirect to main repo" (thrum-16lv fix)

# Verify redirect
cat .thrum/redirect
# Expected: /Users/leon/dev/opensource/thrum/.thrum

# Implementer worktree
cd ~/.workspaces/thrum/test-implementer
rm -rf .thrum 2>/dev/null

thrum init
# Expected: "Worktree detected — set up redirect to main repo"

cat .thrum/redirect
# Expected: /Users/leon/dev/opensource/thrum/.thrum
```

### D2. Register Coordinator Agent

```bash
cd ~/.workspaces/thrum/test-coordinator

thrum quickstart --name test-coordinator --role coordinator --module testing \
  --intent "Testing thrum release"
# Expected:
#   Registered @test-coordinator (coordinator:testing)
#   Session started: ses_<ulid>
#   Intent: Testing thrum release

thrum whoami
# Expected: Shows test-coordinator / coordinator / testing
```

### D3. Register Implementer Agent

```bash
cd ~/.workspaces/thrum/test-implementer

thrum quickstart --name test-implementer --role implementer --module testing \
  --intent "Testing message reception"
# Expected:
#   Registered @test-implementer (implementer:testing)
#   Session started: ses_<ulid>
```

### D4. Verify Team Roster

```bash
cd /Users/leon/dev/opensource/thrum

thrum team
# Expected: Shows both test-coordinator and test-implementer with roles, sessions, inbox counts
```

---

## Part E: CLI Messaging

### E1. Direct Message (Coordinator → Implementer)

```bash
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum send "Hello from coordinator" --to @test-implementer
# Expected: Message sent: msg_<ulid>

# Verify receipt
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum inbox --unread
# Expected: Shows message from @test-coordinator
```

### E2. Reply (Implementer → Coordinator)

```bash
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum send "Reply from implementer" --to @test-coordinator
# Expected: Message sent: msg_<ulid>

# Verify receipt
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum inbox --unread
# Expected: Shows message from @test-implementer
```

### E3. Priority Flag Shorthand (thrum-en2c)

```bash
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum send "High priority test" --to @test-implementer -p high
# Expected: Message sent (previously: "unknown shorthand flag: p")

# Verify priority
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum inbox --unread --json | grep priority
# Expected: Shows "priority":"high"
```

### E4. Broadcast Message

```bash
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum send "Broadcast test" --broadcast
# Expected: Message sent

# Both agents should receive
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum inbox --unread
# Expected: Shows broadcast message
```

### E5. Mark as Read

```bash
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum message read --all
# Expected: All messages marked as read

THRUM_NAME=test-implementer thrum inbox --unread
# Expected: No unread messages (or empty list)
```

---

## Part F: Groups

### F1. Create Group

```bash
cd /Users/leon/dev/opensource/thrum

thrum group create test-team
# Expected: Group created: test-team

thrum group list
# Expected: Shows test-team (0 members) and everyone group
```

### F2. Add Members

```bash
thrum group add test-team @test-coordinator
# Expected: Added @test-coordinator to test-team

thrum group add test-team @test-implementer
# Expected: Added @test-implementer to test-team

thrum group list
# Expected: test-team (2 members)
```

### F3. Add by Role

```bash
# Create a new group for role-based membership
thrum group create coordinators
thrum group add coordinators --role coordinator
# Expected: Added all coordinator agents (test-coordinator)

thrum group list
# Expected: Shows coordinators group with 1 member
```

### F4. Send Group Message

```bash
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum send "Group message to test-team" --to @test-team
# Expected: Message sent

# Verify implementer received it
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum inbox --unread
# Expected: Shows group message from @test-coordinator
```

### F5. Nested Groups

```bash
cd /Users/leon/dev/opensource/thrum

thrum group create meta-group
thrum group add meta-group --group test-team
# Expected: Added group test-team to meta-group

thrum group list
# Expected: Shows meta-group containing test-team (which has 2 agents)
```

---

## Part G: Plugin & Slash Commands (tmux)

### G1. Create tmux Sessions

```bash
# Kill existing sessions if any
tmux kill-session -t coordinator 2>/dev/null || true
tmux kill-session -t implementer 2>/dev/null || true

# Create coordinator session
tmux new-session -d -s coordinator -c ~/.workspaces/thrum/test-coordinator -x 200 -y 50

# Create implementer session
tmux new-session -d -s implementer -c ~/.workspaces/thrum/test-implementer -x 200 -y 50
```

### G2. Launch Claude Code Sessions

```bash
# Launch coordinator (must unset CLAUDECODE if running from within Claude)
tmux send-keys -t coordinator "unset CLAUDECODE && THRUM_NAME=test-coordinator claude --dangerously-skip-permissions" Enter

# Wait for startup
sleep 15

# Launch implementer
tmux send-keys -t implementer "unset CLAUDECODE && THRUM_NAME=test-implementer claude --dangerously-skip-permissions" Enter

sleep 15
```

### G3. Test SessionStart Hook (thrum prime auto-runs)

```bash
# Check coordinator output — SessionStart hook should have run thrum prime
tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -30
# Expected: Claude Code started, may show thrum prime output or session ready

# Check for errors related to mcpServers (should be none — thrum-5611 fix)
tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -i "mcpServers" && echo "FAIL: mcpServers issue" || echo "OK: no mcpServers errors"
```

### G4. Test /thrum:prime

```bash
# Send prime command to coordinator
tmux send-keys -t coordinator "/thrum:prime" Enter
sleep 2
tmux send-keys -t coordinator Enter  # select autocomplete
sleep 25

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -40
# Expected: Agent identity, session, team roster, inbox, branch, daemon status, listener spawn instruction
# CRITICAL: Should end with listener spawn instruction including WAIT_CMD
```

### G5. Test /thrum:team

```bash
tmux send-keys -t coordinator C-u  # clear any auto-suggestion
tmux send-keys -t coordinator "/thrum:team" Enter
sleep 2
tmux send-keys -t coordinator Enter
sleep 20

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -20
# Expected: Shows both test-coordinator and test-implementer agents
```

### G6. Test /thrum:inbox

```bash
# First send a message to coordinator (from implementer via CLI)
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum send "Test inbox slash command" --to @test-coordinator

# Now check inbox via slash command
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:inbox" Enter
sleep 2
tmux send-keys -t coordinator Enter
sleep 20

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -25
# Expected: Shows unread message from test-implementer
```

### G7. Test /thrum:send

```bash
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:send" Enter
sleep 2
tmux send-keys -t coordinator Enter
# Follow the interactive prompts (or just verify command loads)
sleep 5

# For automated testing, send direct command instead
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "send a thrum message to @test-implementer saying 'Hello from slash command test'" Enter
sleep 30

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: "Message sent: msg_<ulid>"

# Verify receipt on implementer
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum inbox --unread
# Expected: Shows message from coordinator
```

### G8. Test /thrum:overview

```bash
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:overview" Enter
sleep 2
tmux send-keys -t coordinator Enter
sleep 25

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -35
# Expected: Combined status, team roster, inbox summary, sync status
```

### G9. Test /thrum:quickstart

```bash
# Create third agent via slash command
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:quickstart" Enter
sleep 2
tmux send-keys -t coordinator Enter
# Interactive mode — would need manual input
# Skip for automated testing

# Verify via CLI instead
cd /Users/leon/dev/opensource/thrum
thrum quickstart --name test-reviewer --role reviewer --module testing --intent "Third agent test"
# Expected: Registered @test-reviewer
```

### G10. Test /thrum:wait

```bash
# Send message from implementer first
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum send "Wait test message" --to @test-coordinator

# Test wait in coordinator session
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "check for new thrum messages using wait" Enter
sleep 25

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -20
# Expected: Agent runs thrum wait, receives message
```

### G11. Test /thrum:reply

```bash
# Get a message ID first
cd ~/.workspaces/thrum/test-coordinator
MSG_ID=$(THRUM_NAME=test-coordinator thrum inbox --unread --json | grep -o '"id":"msg_[^"]*"' | head -1 | cut -d'"' -f4)

if [ -n "$MSG_ID" ]; then
  # Reply via slash command
  tmux send-keys -t coordinator C-u
  tmux send-keys -t coordinator "reply to message $MSG_ID with 'Got it, thanks'" Enter
  sleep 30

  tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -15
  # Expected: Reply sent
else
  echo "SKIP: No message to reply to"
fi
```

### G12. Test /thrum:group

```bash
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:group" Enter
sleep 2
tmux send-keys -t coordinator Enter
# Interactive group command loads
sleep 5

# Verify group list via CLI instead
thrum group list
# Expected: Shows test-team, coordinators, meta-group
```

---

## Part H: Cross-Session Messaging (tmux)

### H1. Verify Identity Resolution

```bash
# Check coordinator identity
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "what is my thrum identity" Enter
sleep 20

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: Shows @test-coordinator / coordinator / testing

# Check implementer identity (F7 — worktree identity fix)
tmux send-keys -t implementer C-u
tmux send-keys -t implementer "what is my thrum identity" Enter
sleep 20

tmux capture-pane -t implementer -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: Shows @test-implementer / implementer / testing (NOT coordinator)
```

### H2. Session 1 → Session 2

```bash
# Coordinator sends to implementer
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "send thrum message to @test-implementer: 'Cross-session test from coordinator'" Enter
sleep 30

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: Message sent confirmation
```

### H3. Session 2 Receives & Replies

```bash
# Check implementer received message
tmux send-keys -t implementer C-u
tmux send-keys -t implementer "check my thrum inbox" Enter
sleep 25

tmux capture-pane -t implementer -p -S - -E - 2>&1 | grep -v "^$" | tail -20
# Expected: Shows cross-session message from coordinator

# Send reply
tmux send-keys -t implementer C-u
tmux send-keys -t implementer "reply to coordinator: 'Received cross-session message'" Enter
sleep 30

tmux capture-pane -t implementer -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: Reply sent
```

### H4. Session 1 Confirms Receipt

```bash
# Coordinator checks inbox
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "check inbox for reply" Enter
sleep 25

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -20
# Expected: Shows reply from implementer
```

---

## Part I: Context & Compaction

### I1. Test /thrum:update-context

```bash
# Update context in coordinator session
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:update-context" Enter
sleep 2
tmux send-keys -t coordinator Enter
# This opens an interactive guided context save
sleep 5

# For automated testing, provide context directly
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "update my thrum context with: Testing cross-session messaging and plugin commands" Enter
sleep 30

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: Context updated confirmation
```

### I2. Verify Context Saved

```bash
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum context show
# Expected: Shows saved context including recent work description
```

### I3. Test PreCompact Hook

```bash
# Manually trigger the pre-compact script
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator bash /Users/leon/dev/opensource/thrum/claude-plugin/scripts/pre-compact-save-context.sh

# Check for /tmp backup
ls -la /tmp/thrum-pre-compact-test-coordinator-coordinator-testing-*.md
# Expected: Shows backup file with identity + epoch in filename

# Verify backup content
cat /tmp/thrum-pre-compact-test-coordinator-coordinator-testing-*.md | head -30
# Expected: Shows git state, beads state (if available), thrum status
```

### I4. Test /thrum:load-context

```bash
# Load context in coordinator session
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:load-context" Enter
sleep 2
tmux send-keys -t coordinator Enter
sleep 25

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -30
# Expected: Shows thrum context + /tmp backup (if recent), agent recovers work context
```

### I5. Test Context Persistence Across Sessions

```bash
# Exit coordinator session
tmux send-keys -t coordinator "/exit" Enter
sleep 3
tmux kill-session -t coordinator

# Restart coordinator session
tmux new-session -d -s coordinator -c ~/.workspaces/thrum/test-coordinator -x 200 -y 50
tmux send-keys -t coordinator "unset CLAUDECODE && THRUM_NAME=test-coordinator claude --dangerously-skip-permissions" Enter
sleep 15

# Load context in new session
tmux send-keys -t coordinator "/thrum:load-context" Enter
sleep 2
tmux send-keys -t coordinator Enter
sleep 25

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -25
# Expected: Context loaded successfully, agent aware of previous work
```

---

## Part J: Bugfix Regressions (v0.4.1+)

### J1. Priority Shorthand `-p` (thrum-en2c)

```bash
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum send "Critical priority test" --to @test-implementer -p critical
# Expected: Message sent (previously: "unknown shorthand flag: p")

# Verify via --json
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum inbox --unread --json | grep '"priority":"critical"'
# Expected: Shows critical priority
```

### J2. Prime Unread Count Accuracy (thrum-pwaa)

```bash
# Send fresh message to coordinator
cd ~/.workspaces/thrum/test-implementer
THRUM_NAME=test-implementer thrum send "Unread count verification" --to @test-coordinator

# Check prime shows unread > 0
cd ~/.workspaces/thrum/test-coordinator
THRUM_NAME=test-coordinator thrum prime | grep -i inbox
# Expected: "Inbox: 1 unread" (or more) — NOT "all read"
# Previously: Always showed "all read" because messages marked read during query
```

### J3. Init Detects Worktree (thrum-16lv)

```bash
# Already tested in D1, but verify redirect logic
cd /tmp
git clone /Users/leon/dev/opensource/thrum thrum-test-main
cd thrum-test-main
git worktree add /tmp/thrum-test-wt -b test-wt-branch HEAD

cd /tmp/thrum-test-wt
thrum init
# Expected: "Worktree detected — set up redirect to main repo"

cat .thrum/redirect
# Expected: /tmp/thrum-test-main/.thrum

# Cleanup
cd /tmp/thrum-test-main
git worktree remove /tmp/thrum-test-wt
cd /tmp
rm -rf thrum-test-main
```

### J4. Init Does NOT Write MCP to Settings (thrum-5611)

```bash
# Already tested in C1, double-check here
mkdir -p /tmp/thrum-settings-test && cd /tmp/thrum-settings-test
git init
thrum init

# Verify no mcpServers entry
if [ -f .claude/settings.json ]; then
  grep -q mcpServers .claude/settings.json && echo "FAIL: mcpServers written" || echo "OK: no mcpServers"
else
  echo "OK: no settings.json"
fi

# Cleanup
rm -rf /tmp/thrum-settings-test
```

### J5. Wait Subscription Cleanup on Disconnect (thrum-pgoc)

```bash
cd ~/.workspaces/thrum/test-coordinator

# Start wait with short timeout
timeout 3 thrum wait --all --timeout 5s; echo "Exit: $?"
# Expected: Exits cleanly (124 = timeout, or 0 = no messages)

# Immediately start another wait — should NOT error
timeout 3 thrum wait --all --timeout 5s; echo "Exit: $?"
# Expected: Exits cleanly again (no "subscription already exists" error)
# Previously: Second wait failed with "subscribe: subscription already exists"
```

### J6. Ping Resolves by Agent Name (thrum-8ws1)

```bash
cd /Users/leon/dev/opensource/thrum

thrum ping @test-coordinator
# Expected: Shows test-coordinator as active with intent
# Previously: "not found (no agent registered with this role)"

thrum ping @test-implementer
# Expected: Shows test-implementer as active with intent
```

### J7. MCP Serve Does NOT Crash (NULL Display Fix)

```bash
cd /Users/leon/dev/opensource/thrum

# Start MCP server briefly
timeout 3 thrum mcp serve; echo "Exit: $?"
# Expected: Exit 124 (timeout) — NOT crash/panic
# Previously crashed with: runtime error: invalid memory address (NULL display column)
```

---

## Part K: Unit & Integration Tests

### K1. Run Unit Tests

```bash
cd /Users/leon/dev/opensource/thrum

go test ./... -v
# Expected: All tests pass, no failures
```

### K2. Run Integration Tests

```bash
cd /Users/leon/dev/opensource/thrum

go test -tags=integration ./... -v
# Expected: Integration tests pass (may be slower)
```

### K3. Run Resilience Tests

```bash
cd /Users/leon/dev/opensource/thrum

go test -tags=resilience ./... -v
# Expected: Resilience tests pass (stress tests, concurrent access, etc.)
```

### K4. Test Coverage

```bash
cd /Users/leon/dev/opensource/thrum

go test ./... -cover
# Expected: Shows coverage percentages, ideally >60% overall
```

---

## Part L: Cleanup

### L1. Exit Claude Code Sessions

```bash
# Exit coordinator
tmux send-keys -t coordinator "/exit" Enter 2>/dev/null
sleep 3

# Exit implementer
tmux send-keys -t implementer "/exit" Enter 2>/dev/null
sleep 3

# Kill tmux sessions
tmux kill-session -t coordinator 2>/dev/null || true
tmux kill-session -t implementer 2>/dev/null || true
```

### L2. Delete Test Agents

```bash
cd /Users/leon/dev/opensource/thrum

# Delete agents (pipe yes for confirmation)
yes | thrum agent delete test-coordinator 2>/dev/null || true
yes | thrum agent delete test-implementer 2>/dev/null || true
yes | thrum agent delete test-reviewer 2>/dev/null || true

# Verify deletion
thrum team
# Expected: No agents listed (or only unrelated agents)
```

### L3. Delete Test Groups

```bash
thrum group delete test-team 2>/dev/null || true
thrum group delete coordinators 2>/dev/null || true
thrum group delete meta-group 2>/dev/null || true

thrum group list
# Expected: Only @everyone group remains
```

### L4. Stop Daemon

```bash
cd /Users/leon/dev/opensource/thrum

thrum daemon stop
# Expected: Daemon stopped

thrum daemon status
# Expected: Daemon not running
```

### L5. Clean Up Worktree Data

```bash
# Clean test-coordinator worktree
cd ~/.workspaces/thrum/test-coordinator
rm -rf .thrum 2>/dev/null
rm -f .claude/settings.json 2>/dev/null

# Clean test-implementer worktree
cd ~/.workspaces/thrum/test-implementer
rm -rf .thrum 2>/dev/null
rm -f .claude/settings.json 2>/dev/null

# Clean main repo
cd /Users/leon/dev/opensource/thrum
rm -rf .thrum 2>/dev/null
```

### L6. Clean Up Temp Files

```bash
# Remove pre-compact backups for test agents
rm -f /tmp/thrum-pre-compact-test-coordinator-*.md 2>/dev/null
rm -f /tmp/thrum-pre-compact-test-implementer-*.md 2>/dev/null
rm -f /tmp/thrum-pre-compact-test-reviewer-*.md 2>/dev/null
```

---

## Part M: Install Script & Homebrew (RELEASE DAY ONLY)

⚠️ **Run these tests ONLY on release day after publishing the GitHub release and Homebrew tap.**

### M1. Remove Existing Binary

```bash
rm -f ~/.local/bin/thrum
which thrum && echo "FAIL: still found" || echo "OK: removed"
```

### M2. Install via curl | sh

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
# Expected:
#   Thrum installer
#   Resolving latest version...
#   Version: v0.4.2 (or current release)
#   Platform: darwin/arm64 (or your platform)
#   Downloading thrum v0.4.2...
#   Checksum verified (SHA-256)
#   thrum installed to ~/.local/bin/thrum
```

### M3. Verify Installed Version

```bash
thrum version
# Expected: thrum v0.4.2 (build: <hash>, go<version>)

thrum --help
# Expected: Full help output with all commands
```

### M4. Smoke Test After Install

```bash
mkdir -p /tmp/thrum-install-smoke && cd /tmp/thrum-install-smoke
git init
thrum init
thrum daemon start
thrum daemon status
# Expected: Daemon running

thrum quickstart --role tester --module smoke --intent "Install smoke test"
thrum prime
# Expected: Full prime output

thrum daemon stop
cd /tmp
rm -rf /tmp/thrum-install-smoke
```

### M5. Verify Homebrew Tap Updated

```bash
brew update
brew info leonletto/tap/thrum
# Expected: Shows thrum 0.4.2 (current release version)
```

### M6. Install via Homebrew

```bash
# Remove script-installed binary first
rm -f ~/.local/bin/thrum

brew install leonletto/tap/thrum
# Expected: Installs thrum 0.4.2 from cask

thrum version
# Expected: thrum v0.4.2
```

### M7. Homebrew Smoke Test

```bash
mkdir -p /tmp/thrum-brew-smoke && cd /tmp/thrum-brew-smoke
git init
thrum init
thrum daemon start
thrum quickstart --role tester --module brew --intent "Homebrew smoke test"
thrum prime
# Expected: Full prime output with correct version

thrum daemon stop
cd /tmp
rm -rf /tmp/thrum-brew-smoke
```

### M8. Upgrade Path (if previously installed)

```bash
brew upgrade leonletto/tap/thrum
# Expected: Upgrades to 0.4.2 (or "already installed" if current)

thrum version
# Expected: v0.4.2
```

---

## Results Tracker

| Test ID | Description | Status | Notes |
|---------|-------------|--------|-------|
| **Part A: Build & Prerequisites** | | | |
| A1 | Verify tmux installation | ☐ | |
| A2 | Build from source | ☐ | |
| **Part B: Plugin Management** | | | |
| B1 | Uninstall existing plugin | ☐ | |
| B2 | Add local marketplace | ☐ | |
| B3 | Install from local marketplace | ☐ | |
| B4 | Verify plugin metadata | ☐ | |
| **Part C: Init & Daemon** | | | |
| C1 | Initialize main repo | ☐ | thrum-5611 fix |
| C2 | Start daemon | ☐ | |
| C3 | Verify daemon health | ☐ | |
| **Part D: Agent Registration** | | | |
| D1 | Initialize test worktrees | ☐ | thrum-16lv fix |
| D2 | Register coordinator agent | ☐ | |
| D3 | Register implementer agent | ☐ | |
| D4 | Verify team roster | ☐ | |
| **Part E: CLI Messaging** | | | |
| E1 | Direct message | ☐ | |
| E2 | Reply message | ☐ | |
| E3 | Priority flag shorthand | ☐ | thrum-en2c fix |
| E4 | Broadcast message | ☐ | |
| E5 | Mark as read | ☐ | |
| **Part F: Groups** | | | |
| F1 | Create group | ☐ | |
| F2 | Add members | ☐ | |
| F3 | Add by role | ☐ | |
| F4 | Send group message | ☐ | |
| F5 | Nested groups | ☐ | |
| **Part G: Plugin & Slash Commands** | | | |
| G1 | Create tmux sessions | ☐ | |
| G2 | Launch Claude Code sessions | ☐ | |
| G3 | Test SessionStart hook | ☐ | thrum-5611 fix |
| G4 | Test /thrum:prime | ☐ | |
| G5 | Test /thrum:team | ☐ | |
| G6 | Test /thrum:inbox | ☐ | |
| G7 | Test /thrum:send | ☐ | |
| G8 | Test /thrum:overview | ☐ | |
| G9 | Test /thrum:quickstart | ☐ | |
| G10 | Test /thrum:wait | ☐ | |
| G11 | Test /thrum:reply | ☐ | |
| G12 | Test /thrum:group | ☐ | |
| **Part H: Cross-Session Messaging** | | | |
| H1 | Verify identity resolution | ☐ | F7 worktree fix |
| H2 | Session 1 → Session 2 | ☐ | |
| H3 | Session 2 receives & replies | ☐ | |
| H4 | Session 1 confirms receipt | ☐ | |
| **Part I: Context & Compaction** | | | |
| I1 | Test /thrum:update-context | ☐ | |
| I2 | Verify context saved | ☐ | |
| I3 | Test PreCompact hook | ☐ | |
| I4 | Test /thrum:load-context | ☐ | |
| I5 | Test context persistence | ☐ | |
| **Part J: Bugfix Regressions** | | | |
| J1 | Priority shorthand -p | ☐ | thrum-en2c |
| J2 | Prime unread count accuracy | ☐ | thrum-pwaa |
| J3 | Init detects worktree | ☐ | thrum-16lv |
| J4 | Init no MCP in settings | ☐ | thrum-5611 |
| J5 | Wait subscription cleanup | ☐ | thrum-pgoc |
| J6 | Ping resolves by name | ☐ | thrum-8ws1 |
| J7 | MCP serve doesn't crash | ☐ | NULL display fix |
| **Part K: Unit & Integration Tests** | | | |
| K1 | Run unit tests | ☐ | |
| K2 | Run integration tests | ☐ | |
| K3 | Run resilience tests | ☐ | |
| K4 | Test coverage | ☐ | |
| **Part L: Cleanup** | | | |
| L1 | Exit Claude Code sessions | ☐ | |
| L2 | Delete test agents | ☐ | |
| L3 | Delete test groups | ☐ | |
| L4 | Stop daemon | ☐ | |
| L5 | Clean worktree data | ☐ | |
| L6 | Clean temp files | ☐ | |
| **Part M: Install & Homebrew (Release Day Only)** | | | |
| M1 | Remove existing binary | ☐ | |
| M2 | Install via curl \| sh | ☐ | |
| M3 | Verify installed version | ☐ | |
| M4 | Smoke test after install | ☐ | |
| M5 | Verify Homebrew tap | ☐ | |
| M6 | Install via Homebrew | ☐ | |
| M7 | Homebrew smoke test | ☐ | |
| M8 | Upgrade path | ☐ | |

---

## Notes

- **Tmux tips:**
  - Attach to session: `tmux attach -t coordinator`
  - Switch sessions while attached: `Ctrl-b )` (next) or `Ctrl-b (` (prev)
  - Detach: `Ctrl-b d`
  - List sessions: `tmux list-sessions`

- **Common issues:**
  - If Claude Code hangs on startup, check for rogue mcpServers in .claude/settings.json
  - If identity wrong in worktree, set `THRUM_NAME` env var explicitly
  - If messages not received, verify both agents registered and daemon running
  - If wait times out, check daemon logs: `cat ~/.local/state/thrum/daemon.log`

- **Testing shortcuts:**
  - Mark status column: `☑` = pass, `☐` = pending, `☒` = fail
  - For quick smoke test, run Parts A-E only (build, plugin, daemon, messaging)
  - Full test suite takes ~45-60 minutes
