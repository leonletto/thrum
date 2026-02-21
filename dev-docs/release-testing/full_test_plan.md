# Thrum Release Test Plan

Testing plan for thrum using a separate test repository (`littleCADev`) and its
worktrees. The thrum source repo is used **only** for building (Part A) and
plugin management (Part B). All runtime tests (init, daemon, agents, messaging,
etc.) run in the test repo to avoid polluting the source.

## Test Environment

| Purpose                    | Path                                         | Agent Identity   |
| -------------------------- | -------------------------------------------- | ---------------- |
| Thrum source (build only)  | `/Users/leon/dev/opensource/thrum`           | —                |
| Test repo (main)           | `/Users/leon/dev/testing/littleCADev`        | —                |
| Test worktree: coordinator | `~/.workspaces/littleCADev/test-coordinator` | test_coordinator |
| Test worktree: implementer | `~/.workspaces/littleCADev/test-implementer` | test_implementer |

**Important Notes:**

- Self-send is filtered by design—inbox excludes your own messages automatically
- Use `THRUM_NAME` env var to select identity in multi-agent worktrees
- The main repo's marketplace is at
  `/Users/leon/dev/opensource/thrum/.claude-plugin/marketplace.json`
- PreCompact hook script bundled at
  `${CLAUDE_PLUGIN_ROOT}/scripts/pre-compact-save-context.sh`
- Pre-compact backups:
  `/tmp/thrum-pre-compact-{name}-{role}-{module}-{epoch}.md`
- **Name-only routing**: `@name` resolves to agent by name; `@role` resolves to
  auto-created role group (not direct agent)
- **Name≠role validation**: Agent name cannot equal own role, match an existing
  role, or collide with an existing agent name
- **`--all` removed from wait**: `thrum wait` always filters by calling agent
  identity — no `--all` flag

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
# Expected: thrum version <version>+<hash> (or current version)

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
# Expected: Installed thrum plugin version <version>

cat ~/.claude/plugins/installed_plugins.json | grep thrum
# Expected: Shows thrum@thrum with installPath
```

### B4. Verify Plugin Metadata

```bash
cat ~/.claude/plugins/thrum@thrum/.claude-plugin/plugin.json
# Expected: Shows hooks (SessionStart, PreCompact), version <version>
```

---

## Part C: Init & Daemon

### C1. Initialize Test Repo

```bash
cd /Users/leon/dev/testing/littleCADev

# Clean slate
rm -rf .thrum 2>/dev/null
rm -f .claude/settings.json 2>/dev/null

thrum init
# Expected: "Initialized thrum in /Users/leon/dev/testing/littleCADev"

# CRITICAL: Verify .claude/settings.json does NOT have mcpServers (thrum-5611 fix)
if [ -f .claude/settings.json ]; then
  grep -q mcpServers .claude/settings.json && echo "FAIL: mcpServers written" || echo "OK: no mcpServers"
else
  echo "OK: no settings.json created"
fi
```

### C2. Start Daemon

```bash
cd /Users/leon/dev/testing/littleCADev

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
cd ~/.workspaces/littleCADev/test-coordinator
rm -rf .thrum 2>/dev/null

thrum init
# Expected: "Worktree detected — set up redirect to main repo" (thrum-16lv fix)

# Verify redirect
cat .thrum/redirect
# Expected: /Users/leon/dev/testing/littleCADev/.thrum

# Implementer worktree
cd ~/.workspaces/littleCADev/test-implementer
rm -rf .thrum 2>/dev/null

thrum init
# Expected: "Worktree detected — set up redirect to main repo"

cat .thrum/redirect
# Expected: /Users/leon/dev/testing/littleCADev/.thrum
```

### D2. Register Coordinator Agent

```bash
cd ~/.workspaces/littleCADev/test-coordinator

thrum quickstart --name test_coordinator --role coordinator --module testing \
  --intent "Testing thrum release"
# Expected:
#   Registered @test_coordinator (coordinator:testing)
#   Session started: ses_<ulid>
#   Intent: Testing thrum release

thrum whoami
# Expected: Shows test_coordinator / coordinator / testing
```

### D3. Register Implementer Agent

```bash
cd ~/.workspaces/littleCADev/test-implementer

thrum quickstart --name test_implementer --role implementer --module testing \
  --intent "Testing message reception"
# Expected:
#   Registered @test_implementer (implementer:testing)
#   Session started: ses_<ulid>
```

### D4. Verify Team Roster

```bash
cd /Users/leon/dev/testing/littleCADev

thrum team
# Expected: Shows both test_coordinator and test_implementer with roles, sessions, inbox counts
```

---

## Part E: CLI Messaging

### E1. Direct Message (Coordinator → Implementer)

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Hello from coordinator" --to @test_implementer
# Expected: Message sent: msg_<ulid>

# Verify receipt
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread
# Expected: Shows message from @test_coordinator
```

### E2. Reply (Implementer → Coordinator)

```bash
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum send "Reply from implementer" --to @test_coordinator
# Expected: Message sent: msg_<ulid>

# Verify receipt
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum inbox --unread
# Expected: Shows message from @test_implementer
```

### E3. Priority Flag Removed

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Priority flag test" --to @test_implementer -p high 2>&1
# Expected: ERROR — "unknown shorthand flag: 'p' in -p"
# Priority was removed in v0.4.5 — the flag should no longer be accepted
```

### E4. Broadcast Message

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Broadcast test" --to @everyone
# Expected: Message sent (--broadcast also works but --to @everyone is canonical)

# Both agents should receive
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread
# Expected: Shows broadcast message
```

### E5. Mark as Read

```bash
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum message read --all
# Expected: All messages marked as read

THRUM_NAME=test_implementer thrum inbox --unread
# Expected: No unread messages (or empty list)
```

### E6. Send to Unknown Recipient (Negative)

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Should fail" --to @nonexistent_agent
# Expected: ERROR — "unknown recipient(s): @nonexistent_agent"
# Message must NOT be stored

# Verify no message leaked
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread
# Expected: No new messages (the failed send did not produce a message)
```

### E7. Send to Multiple Recipients with One Bad Address (Negative)

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Mixed recipients" --to @test_implementer --to @ghost_agent
# Expected: ERROR — "unknown recipient(s): @ghost_agent"
# Message NOT stored for ANY recipient (atomic rejection)

# Verify implementer did NOT receive message
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread
# Expected: No new messages
```

### E8. Send to Role Name Resolves via Group (Warning)

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Message to role" --to @implementer 2>&1
# Expected: Message sent, but response includes WARNING:
#   "@implementer resolved to group 'implementer', not a specific agent"
# Message IS delivered to all agents with role "implementer" (test_implementer)

cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread
# Expected: Shows the "Message to role" message
```

### E9. Name≠Role Validation on Registration

```bash
# Attempt to register agent whose name equals an existing role
cd /Users/leon/dev/testing/littleCADev
thrum quickstart --name implementer --role tester --module testing --intent "Should fail"
# Expected: ERROR — name "implementer" conflicts with existing role "implementer"

# Attempt to register agent whose name equals its own role
thrum quickstart --name reviewer --role reviewer --module testing --intent "Should fail"
# Expected: ERROR — agent name cannot equal its own role
```

---

## Part F: Groups

### F1. Create Group

```bash
cd /Users/leon/dev/testing/littleCADev

thrum group create test-team
# Expected: Group created: test-team

thrum group list
# Expected: Shows test-team (0 members) and everyone group
```

### F2. Add Members

```bash
thrum group add test-team @test_coordinator
# Expected: Added @test_coordinator to test-team

thrum group add test-team @test_implementer
# Expected: Added @test_implementer to test-team

thrum group list
# Expected: test-team (2 members)
```

### F3. Add by Role

```bash
# Create a new group for role-based membership
thrum group create coordinators
thrum group add coordinators --role coordinator
# Expected: Added all coordinator agents (test_coordinator)

thrum group list
# Expected: Shows coordinators group with 1 member
```

### F4. Send Group Message

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Group message to test-team" --to @test-team
# Expected: Message sent

# Verify implementer received it
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread
# Expected: Shows group message from @test_coordinator
```

### F5. Nested Groups (NOT SUPPORTED)

```bash
cd ~/.workspaces/littleCADev/test-coordinator

# Nested groups are not supported — verify graceful behavior
THRUM_NAME=test_coordinator thrum group create meta_group
THRUM_NAME=test_coordinator thrum group add meta_group test_team
# Expected: Adds "test_team" as a plain agent member (NOT group nesting)
# This does NOT make meta_group a superset of test_team's members

THRUM_NAME=test_coordinator thrum group list
# Expected: meta_group shows 1 member (agent ref "test_team"), NOT 2 expanded agents
```

---

## Part G: Plugin & Slash Commands (tmux)

### G1. Create tmux Sessions

```bash
# Kill existing sessions if any
tmux kill-session -t coordinator 2>/dev/null || true
tmux kill-session -t implementer 2>/dev/null || true

# Create coordinator session
tmux new-session -d -s coordinator -c ~/.workspaces/littleCADev/test-coordinator -x 200 -y 50

# Create implementer session
tmux new-session -d -s implementer -c ~/.workspaces/littleCADev/test-implementer -x 200 -y 50
```

### G2. Launch Claude Code Sessions

```bash
# Launch coordinator with Haiku (fast, sufficient for CLI tests; must unset CLAUDECODE if running from within Claude)
tmux send-keys -t coordinator "unset CLAUDECODE && THRUM_NAME=test_coordinator claude --model haiku --dangerously-skip-permissions" Enter

# Wait for startup
sleep 15

# Launch implementer with Haiku
tmux send-keys -t implementer "unset CLAUDECODE && THRUM_NAME=test_implementer claude --model haiku --dangerously-skip-permissions" Enter

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
# Expected: Shows both test_coordinator and test_implementer agents
```

### G6. Test /thrum:inbox

```bash
# First send a message to coordinator (from implementer via CLI)
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum send "Test inbox slash command" --to @test_coordinator

# Now check inbox via slash command
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "/thrum:inbox" Enter
sleep 2
tmux send-keys -t coordinator Enter
sleep 20

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -25
# Expected: Shows unread message from test_implementer
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
tmux send-keys -t coordinator "send a thrum message to @test_implementer saying 'Hello from slash command test'" Enter
sleep 30

tmux capture-pane -t coordinator -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: "Message sent: msg_<ulid>"

# Verify receipt on implementer
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread
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
cd /Users/leon/dev/testing/littleCADev
thrum quickstart --name test_reviewer --role reviewer --module testing --intent "Third agent test"
# Expected: Registered @test_reviewer
```

### G10. Test /thrum:wait

```bash
# Send message from implementer first
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum send "Wait test message" --to @test_coordinator

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
cd ~/.workspaces/littleCADev/test-coordinator
MSG_ID=$(THRUM_NAME=test_coordinator thrum inbox --unread --json | grep -o '"id":"msg_[^"]*"' | head -1 | cut -d'"' -f4)

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
# Expected: Shows @test_coordinator / coordinator / testing

# Check implementer identity (F7 — worktree identity fix)
tmux send-keys -t implementer C-u
tmux send-keys -t implementer "what is my thrum identity" Enter
sleep 20

tmux capture-pane -t implementer -p -S - -E - 2>&1 | grep -v "^$" | tail -15
# Expected: Shows @test_implementer / implementer / testing (NOT coordinator)
```

### H2. Session 1 → Session 2

```bash
# Coordinator sends to implementer
tmux send-keys -t coordinator C-u
tmux send-keys -t coordinator "send thrum message to @test_implementer: 'Cross-session test from coordinator'" Enter
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
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum context show
# Expected: Shows saved context including recent work description
```

### I3. Test PreCompact Hook

```bash
# Manually trigger the pre-compact script
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator bash /Users/leon/dev/opensource/thrum/claude-plugin/scripts/pre-compact-save-context.sh

# Check for /tmp backup
ls -la /tmp/thrum-pre-compact-test_coordinator-coordinator-testing-*.md
# Expected: Shows backup file with identity + epoch in filename

# Verify backup content
cat /tmp/thrum-pre-compact-test_coordinator-coordinator-testing-*.md | head -30
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
tmux new-session -d -s coordinator -c ~/.workspaces/littleCADev/test-coordinator -x 200 -y 50
tmux send-keys -t coordinator "unset CLAUDECODE && THRUM_NAME=test_coordinator claude --model haiku --dangerously-skip-permissions" Enter
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

### J1. Priority Flag Removed (thrum-en2c → removed in v0.4.5)

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Priority test" --to @test_implementer -p critical 2>&1
# Expected: ERROR — "unknown shorthand flag: 'p' in -p"
# Priority was removed in v0.4.5

THRUM_NAME=test_coordinator thrum send "Priority test" --to @test_implementer --priority high 2>&1
# Expected: ERROR — "unknown flag: --priority"
```

### J2. Prime Unread Count Accuracy (thrum-pwaa)

```bash
# Send fresh message to coordinator
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum send "Unread count verification" --to @test_coordinator

# Check prime shows unread > 0
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum prime | grep -i inbox
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
cd ~/.workspaces/littleCADev/test-coordinator

# Start wait with short timeout
timeout 3 thrum wait --timeout 5s; echo "Exit: $?"
# Expected: Exits cleanly (124 = timeout, or 0 = no messages)

# Immediately start another wait — should NOT error
timeout 3 thrum wait --timeout 5s; echo "Exit: $?"
# Expected: Exits cleanly again (no "subscription already exists" error)
# Previously: Second wait failed with "subscribe: subscription already exists"
```

### J6. Ping Resolves by Agent Name (thrum-8ws1)

```bash
cd /Users/leon/dev/testing/littleCADev

thrum ping @test_coordinator
# Expected: Shows test_coordinator as active with intent
# Previously: "not found (no agent registered with this role)"

thrum ping @test_implementer
# Expected: Shows test_implementer as active with intent
```

### J7. MCP Serve Does NOT Crash (NULL Display Fix)

```bash
cd /Users/leon/dev/testing/littleCADev

# Start MCP server briefly
timeout 3 thrum mcp serve; echo "Exit: $?"
# Expected: Exit 124 (timeout) — NOT crash/panic
# Previously crashed with: runtime error: invalid memory address (NULL display column)
```

### J8. Wait --all Flag Removed (thrum-od0e)

```bash
cd ~/.workspaces/littleCADev/test-coordinator

# --all should be rejected (flag no longer exists)
THRUM_NAME=test_coordinator thrum wait --all --timeout 3s 2>&1
# Expected: ERROR — "unknown flag: --all"
# Previously: --all was accepted but caused all agents to wake for every message

# Wait without --all should work normally (filters by caller identity)
timeout 5 bash -c 'THRUM_NAME=test_coordinator thrum wait --timeout 3s'; echo "Exit: $?"
# Expected: Exit 0 or timeout — no errors
```

### J9. Unknown Recipient Hard Error (thrum-lf1m)

```bash
cd ~/.workspaces/littleCADev/test-coordinator

# CLI: send to non-existent agent
THRUM_NAME=test_coordinator thrum send "fail" --to @does-not-exist 2>&1
# Expected: ERROR listing "@does-not-exist" as unknown
# Previously: silently dropped or delivered to nobody

# Verify message was NOT stored
THRUM_NAME=test_coordinator thrum inbox --json | grep -c "fail"
# Expected: 0 (message not stored)
```

### J10. Name-Only Routing / Auto Role Groups (thrum-8mkl)

```bash
cd /Users/leon/dev/testing/littleCADev

# Register agent with name "alpha" and role "worker"
thrum quickstart --name alpha --role worker --module testing --intent "Routing test"

# Sending to @alpha should route directly to agent "alpha"
thrum quickstart --name beta --role tester --module testing --intent "Routing test sender"
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=beta thrum send "Direct to alpha" --to @alpha
# Expected: Message sent, no warnings

# Sending to @worker should route via auto-created role group, with warning
THRUM_NAME=beta thrum send "To worker role" --to @worker 2>&1
# Expected: Message sent with WARNING about group resolution

# Cleanup
yes | thrum agent delete alpha 2>/dev/null || true
yes | thrum agent delete beta 2>/dev/null || true
```

### J11. Group-Send Warning Excludes @everyone (thrum-fsvi)

```bash
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Everyone message" --to @everyone 2>&1
# Expected: Message sent with NO warning about group resolution
# @everyone is excluded from the "resolved to group" warning
```

### J12. Name≠Role Validation (thrum-8mkl)

```bash
cd /Users/leon/dev/testing/littleCADev

# Register base agent
thrum quickstart --name gamma --role planner --module testing --intent "Validation test"

# Attempt: name matches existing role "planner"
thrum quickstart --name planner --role builder --module testing --intent "Should fail" 2>&1
# Expected: ERROR — name conflicts with existing role

# Attempt: name equals own role
thrum quickstart --name reviewer --role reviewer --module testing --intent "Should fail" 2>&1
# Expected: ERROR — name cannot equal own role

# Re-registration should be allowed (same name+role)
thrum quickstart --name gamma --role planner --module testing --intent "Re-register OK"
# Expected: Success (re-registration skips validation)

# Cleanup
yes | thrum agent delete gamma 2>/dev/null || true
```

---

## Part K: MCP Routing Parity

These tests verify that MCP tool-based messaging has equivalent routing behavior
to the CLI. Run these in a Claude Code session with the Thrum MCP server active,
or via `thrum mcp serve` piped to JSON-RPC.

### K1. MCP Send to Unknown Recipient

```bash
# Via MCP tools in Claude Code session:
# mcp__thrum__send_message(to="@nonexistent", content="Should fail")
# Expected: Error response with "unknown recipient(s): @nonexistent"
# Message NOT stored

# CLI equivalent for verification:
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "MCP parity unknown" --to @nonexistent 2>&1
# Expected: Same error as MCP would produce
```

### K2. MCP Send Includes CallerAgentID

```bash
# When Claude Code sends via MCP send_message, the daemon should
# use CallerAgentID (from identity file) as the sender — NOT blank.
# Verify by sending a message and checking inbox on the receiver:

cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "CallerID test" --to @test_implementer

cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer thrum inbox --unread --json 2>&1 | grep "agent_id"
# Expected: Shows "agent_id":"test_coordinator" — NOT empty/null
```

### K3. MCP check_messages Filters by Agent

```bash
# Send messages to both agents
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "For implementer only" --to @test_implementer

# Check that coordinator does NOT see its own outbound in check_messages
# (MCP check_messages uses ForAgent/ForAgentRole filtering)
THRUM_NAME=test_coordinator thrum inbox --unread --json 2>&1 | grep "For implementer only"
# Expected: NOT found in coordinator's inbox

THRUM_NAME=test_implementer thrum inbox --unread --json 2>&1 | grep "For implementer only"
# Expected: Found in implementer's inbox
```

### K4. MCP Reply Includes Original Sender

```bash
# Coordinator sends to implementer
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Original message" --to @test_implementer

# Get message ID
cd ~/.workspaces/littleCADev/test-implementer
MSG_ID=$(THRUM_NAME=test_implementer thrum inbox --unread --json | grep -o '"id":"msg_[^"]*"' | head -1 | cut -d'"' -f4)

# Implementer replies
THRUM_NAME=test_implementer thrum reply "$MSG_ID" "Reply back"

# Coordinator should receive the reply
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum inbox --unread --json 2>&1 | grep "Reply back"
# Expected: Reply visible in coordinator's inbox
# Previously: Reply routing excluded original sender
```

### K5. MCP Waiter Receives @everyone Broadcasts

```bash
# Start wait in background for implementer
cd ~/.workspaces/littleCADev/test-implementer
THRUM_NAME=test_implementer timeout 10 thrum wait --timeout 8s &
WAIT_PID=$!

sleep 1

# Send broadcast from coordinator
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Broadcast for waiter" --to @everyone

# Wait should receive the broadcast
wait $WAIT_PID; echo "Exit: $?"
# Expected: Exit 0 with message received
# Previously: Waiter did not subscribe to @everyone group
```

### K6. MCP list_agents Shows Agent ID

```bash
cd /Users/leon/dev/testing/littleCADev
thrum agent list --json 2>&1
# Expected: Each agent entry includes non-empty "id" or "agent_id" field
# Previously: Display field was empty/NULL
```

### K7. Priority Flag Removed from CLI and MCP

```bash
# Verify -p flag is rejected
cd ~/.workspaces/littleCADev/test-coordinator
THRUM_NAME=test_coordinator thrum send "Priority check" --to @test_implementer -p high 2>&1
# Expected: ERROR — "unknown shorthand flag: 'p' in -p"
# Priority was removed in v0.4.5 — not stored, not queryable
```

---

## Part L: Unit & Integration Tests

### L1. Run Unit Tests

```bash
cd /Users/leon/dev/opensource/thrum

go test ./... -v
# Expected: All tests pass, no failures
```

### L2. Run Integration Tests

```bash
cd /Users/leon/dev/opensource/thrum

go test -tags=integration ./... -v
# Expected: Integration tests pass (may be slower)
```

### L3. Run Resilience Tests

```bash
cd /Users/leon/dev/opensource/thrum

go test -tags=resilience ./... -v
# Expected: Resilience tests pass (stress tests, concurrent access, etc.)
```

### L4. Test Coverage

```bash
cd /Users/leon/dev/opensource/thrum

go test ./... -cover
# Expected: Shows coverage percentages, ideally >60% overall
```

---

## Part M: Cleanup

### M1. Exit Claude Code Sessions

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

### M2. Delete Test Agents

```bash
cd /Users/leon/dev/testing/littleCADev

# Delete agents (pipe yes for confirmation)
yes | thrum agent delete test_coordinator 2>/dev/null || true
yes | thrum agent delete test_implementer 2>/dev/null || true
yes | thrum agent delete test_reviewer 2>/dev/null || true

# Verify deletion
thrum team
# Expected: No agents listed (or only unrelated agents)
```

### M3. Delete Test Groups

```bash
thrum group delete test-team 2>/dev/null || true
thrum group delete coordinators 2>/dev/null || true
thrum group delete meta-group 2>/dev/null || true
# Auto-created role groups from agent registration
thrum group delete coordinator 2>/dev/null || true
thrum group delete implementer 2>/dev/null || true
thrum group delete reviewer 2>/dev/null || true
thrum group delete worker 2>/dev/null || true
thrum group delete tester 2>/dev/null || true
thrum group delete planner 2>/dev/null || true
thrum group delete builder 2>/dev/null || true

thrum group list
# Expected: Only @everyone group remains
```

### M4. Stop Daemon

```bash
cd /Users/leon/dev/testing/littleCADev

thrum daemon stop
# Expected: Daemon stopped

thrum daemon status
# Expected: Daemon not running
```

### M5. Clean Up Worktree Data

```bash
# Clean test_coordinator worktree
cd ~/.workspaces/littleCADev/test-coordinator
rm -rf .thrum 2>/dev/null
rm -f .claude/settings.json 2>/dev/null

# Clean test_implementer worktree
cd ~/.workspaces/littleCADev/test-implementer
rm -rf .thrum 2>/dev/null
rm -f .claude/settings.json 2>/dev/null

# Clean test repo
cd /Users/leon/dev/testing/littleCADev
rm -rf .thrum 2>/dev/null
```

### M6. Clean Up Temp Files

```bash
# Remove pre-compact backups for test agents
rm -f /tmp/thrum-pre-compact-test_coordinator-*.md 2>/dev/null
rm -f /tmp/thrum-pre-compact-test_implementer-*.md 2>/dev/null
rm -f /tmp/thrum-pre-compact-test_reviewer-*.md 2>/dev/null
```

---

## Part N: Install Script & Homebrew (RELEASE DAY ONLY)

⚠️ **Run these tests ONLY on release day after publishing the GitHub release and
Homebrew tap.**

### N1. Remove Existing Binary

```bash
rm -f ~/.local/bin/thrum
which thrum && echo "FAIL: still found" || echo "OK: removed"
```

### N2. Install via curl | sh

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
# Expected:
#   Thrum installer
#   Resolving latest version...
#   Version: v<version> (or current release)
#   Platform: darwin/arm64 (or your platform)
#   Downloading thrum v<version>...
#   Checksum verified (SHA-256)
#   thrum installed to ~/.local/bin/thrum
```

### N3. Verify Installed Version

```bash
thrum version
# Expected: thrum v<version> (build: <hash>, go<version>)

thrum --help
# Expected: Full help output with all commands
```

### N4. Smoke Test After Install

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

### N5. Verify Homebrew Tap Updated

```bash
brew update
brew info leonletto/tap/thrum
# Expected: Shows thrum <version> (current release version)
```

### N6. Install via Homebrew

```bash
# Remove script-installed binary first
rm -f ~/.local/bin/thrum

brew install leonletto/tap/thrum
# Expected: Installs thrum <version> from cask

thrum version
# Expected: thrum v<version>
```

### N7. Homebrew Smoke Test

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

### N8. Upgrade Path (if previously installed)

```bash
brew upgrade leonletto/tap/thrum
# Expected: Upgrades to <version> (or "already installed" if current)

thrum version
# Expected: v<version>
```

---

## Part O: Remote VM Testing (leondev)

Tests run on a clean macOS ARM64 VM (`leondev` in SSH config) to validate
install paths and behavior on a separate machine. All commands prefixed with
`ssh leondev`.

**Prerequisites:**

- SSH access configured as `leondev` in `~/.ssh/config`
- Homebrew installed on VM (install manually if needed:
  `/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"`)
- Test repo cloned at `/Users/leon/dev/testing/littleCADev` on VM (same as
  local)

**VM baseline:** macOS Darwin 25.2.0, arm64 (VMAPPLE), curl 8.7.1, git 2.50.1.

### O1. Verify VM Prerequisites

```bash
ssh leondev 'brew --version'
# Expected: Homebrew 5.x

ssh leondev 'git -C /Users/leon/dev/testing/littleCADev status --short'
# Expected: Clean repo (or shows status without error)
```

### O2. Install thrum via curl | sh

```bash
ssh leondev 'curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh'
# Expected:
#   Thrum installer
#   Platform: darwin/arm64
#   Downloading thrum v<version>...
#   Checksum verified (SHA-256)
#   thrum installed to ~/.local/bin/thrum

ssh leondev '~/.local/bin/thrum version'
# Expected: thrum v<version> (build: <hash>)
```

### O3. Init & Smoke Test (curl install)

```bash
ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && rm -rf .thrum 2>/dev/null && thrum init'
# Expected: "Initialized thrum in /Users/leon/dev/testing/littleCADev"

ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && thrum daemon start && thrum daemon status'
# Expected: Daemon running

ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && thrum quickstart --name remote-tester --role tester --module smoke --intent "Remote smoke test" && thrum prime'
# Expected: Full prime output — agent registered, session started

ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && thrum send "Hello" --to @remote-tester && thrum inbox --json'
# Expected: Self-send filtered — message NOT in inbox
```

### O4. Remote Negative Tests

```bash
# Unknown recipient
ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && thrum send "fail" --to @nonexistent 2>&1'
# Expected: ERROR — unknown recipient(s): @nonexistent

# Wait --all rejected
ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && thrum wait --all --timeout 3s 2>&1'
# Expected: ERROR — unknown flag: --all

# Name≠role validation
ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && thrum quickstart --name tester --role tester --module smoke --intent "Should fail" 2>&1'
# Expected: ERROR — agent name cannot equal its own role
```

### O5. Cleanup curl Install

```bash
ssh leondev 'export PATH="$HOME/.local/bin:$PATH" && cd /Users/leon/dev/testing/littleCADev && thrum daemon stop'
ssh leondev 'rm -f ~/.local/bin/thrum && rm -rf ~/.local/state/thrum'
ssh leondev 'cd /Users/leon/dev/testing/littleCADev && rm -rf .thrum 2>/dev/null'
```

### O6. Install thrum via Homebrew Tap

```bash
ssh leondev 'eval "$(/opt/homebrew/bin/brew shellenv)" && brew install leonletto/tap/thrum'
# Expected: Installed thrum

ssh leondev 'eval "$(/opt/homebrew/bin/brew shellenv)" && thrum version'
# Expected: thrum v<version>
```

### O7. Smoke Test (Homebrew install)

```bash
ssh leondev 'eval "$(/opt/homebrew/bin/brew shellenv)" && cd /Users/leon/dev/testing/littleCADev && rm -rf .thrum 2>/dev/null && thrum init && thrum daemon start && thrum quickstart --name brew-tester --role tester --module brew --intent "Brew smoke test" && thrum prime'
# Expected: Full prime output

ssh leondev 'eval "$(/opt/homebrew/bin/brew shellenv)" && cd /Users/leon/dev/testing/littleCADev && thrum daemon stop && rm -rf .thrum 2>/dev/null'
```

### O8. Homebrew Upgrade Path

```bash
ssh leondev 'eval "$(/opt/homebrew/bin/brew shellenv)" && brew upgrade leonletto/tap/thrum'
# Expected: Already at latest (or upgrades)

ssh leondev 'eval "$(/opt/homebrew/bin/brew shellenv)" && thrum version'
# Expected: Latest version
```

### O9. Deploy Binary via make deploy-remote

```bash
# From local machine
cd /Users/leon/dev/opensource/thrum
make deploy-remote REMOTE=leondev
# Expected: Binary built, scp'd, xattr cleared, codesigned on remote

ssh leondev '~/.local/bin/thrum version'
# Expected: Matches local dev build version+hash
```

### O10. VM Full Cleanup

```bash
ssh leondev 'rm -f ~/.local/bin/thrum'
ssh leondev 'eval "$(/opt/homebrew/bin/brew shellenv)" && brew uninstall leonletto/tap/thrum 2>/dev/null || true'
ssh leondev 'rm -rf ~/.local/state/thrum ~/.config/thrum'
ssh leondev 'cd /Users/leon/dev/testing/littleCADev && rm -rf .thrum .claude/settings.json 2>/dev/null'

ssh leondev 'which thrum 2>/dev/null && echo "FAIL: still found" || echo "OK: fully removed"'
```

---

## Results Tracker

| Test ID                                           | Description                               | Status | Notes                               |
| ------------------------------------------------- | ----------------------------------------- | ------ | ----------------------------------- |
| **Part A: Build & Prerequisites**                 |                                           |        |                                     |
| A1                                                | Verify tmux installation                  | ☐      |                                     |
| A2                                                | Build from source                         | ☐      |                                     |
| **Part B: Plugin Management**                     |                                           |        |                                     |
| B1                                                | Uninstall existing plugin                 | ☐      |                                     |
| B2                                                | Add local marketplace                     | ☐      |                                     |
| B3                                                | Install from local marketplace            | ☐      |                                     |
| B4                                                | Verify plugin metadata                    | ☐      |                                     |
| **Part C: Init & Daemon**                         |                                           |        |                                     |
| C1                                                | Initialize main repo                      | ☐      | thrum-5611 fix                      |
| C2                                                | Start daemon                              | ☐      |                                     |
| C3                                                | Verify daemon health                      | ☐      |                                     |
| **Part D: Agent Registration**                    |                                           |        |                                     |
| D1                                                | Initialize test worktrees                 | ☐      | thrum-16lv fix                      |
| D2                                                | Register coordinator agent                | ☐      |                                     |
| D3                                                | Register implementer agent                | ☐      |                                     |
| D4                                                | Verify team roster                        | ☐      |                                     |
| **Part E: CLI Messaging**                         |                                           |        |                                     |
| E1                                                | Direct message                            | ☐      |                                     |
| E2                                                | Reply message                             | ☐      |                                     |
| E3                                                | Priority flag removed                     | ☐      | -p rejected in v0.4.5               |
| E4                                                | Broadcast message                         | ☐      | --to @everyone canonical            |
| E5                                                | Mark as read                              | ☐      |                                     |
| E6                                                | Send to unknown recipient (negative)      | ☐      | thrum-lf1m                          |
| E7                                                | Mixed valid+invalid recipients (negative) | ☐      | thrum-lf1m, atomic reject           |
| E8                                                | Send to role → group warning              | ☐      | thrum-fsvi, auto role groups        |
| E9                                                | Name≠role validation on registration      | ☐      | thrum-8mkl                          |
| **Part F: Groups**                                |                                           |        |                                     |
| F1                                                | Create group                              | ☐      |                                     |
| F2                                                | Add members                               | ☐      |                                     |
| F3                                                | Add by role                               | ☐      |                                     |
| F4                                                | Send group message                        | ☐      |                                     |
| F5                                                | Nested groups                             | ☐      |                                     |
| **Part G: Plugin & Slash Commands**               |                                           |        |                                     |
| G1                                                | Create tmux sessions                      | ☐      |                                     |
| G2                                                | Launch Claude Code sessions               | ☐      |                                     |
| G3                                                | Test SessionStart hook                    | ☐      | thrum-5611 fix                      |
| G4                                                | Test /thrum:prime                         | ☐      |                                     |
| G5                                                | Test /thrum:team                          | ☐      |                                     |
| G6                                                | Test /thrum:inbox                         | ☐      |                                     |
| G7                                                | Test /thrum:send                          | ☐      |                                     |
| G8                                                | Test /thrum:overview                      | ☐      |                                     |
| G9                                                | Test /thrum:quickstart                    | ☐      |                                     |
| G10                                               | Test /thrum:wait                          | ☐      |                                     |
| G11                                               | Test /thrum:reply                         | ☐      |                                     |
| G12                                               | Test /thrum:group                         | ☐      |                                     |
| **Part H: Cross-Session Messaging**               |                                           |        |                                     |
| H1                                                | Verify identity resolution                | ☐      | F7 worktree fix                     |
| H2                                                | Session 1 → Session 2                     | ☐      |                                     |
| H3                                                | Session 2 receives & replies              | ☐      |                                     |
| H4                                                | Session 1 confirms receipt                | ☐      |                                     |
| **Part I: Context & Compaction**                  |                                           |        |                                     |
| I1                                                | Test /thrum:update-context                | ☐      |                                     |
| I2                                                | Verify context saved                      | ☐      |                                     |
| I3                                                | Test PreCompact hook                      | ☐      |                                     |
| I4                                                | Test /thrum:load-context                  | ☐      |                                     |
| I5                                                | Test context persistence                  | ☐      |                                     |
| **Part J: Bugfix Regressions**                    |                                           |        |                                     |
| J1                                                | Priority flag removed                     | ☐      | -p and --priority rejected          |
| J2                                                | Prime unread count accuracy               | ☐      | thrum-pwaa                          |
| J3                                                | Init detects worktree                     | ☐      | thrum-16lv                          |
| J4                                                | Init no MCP in settings                   | ☐      | thrum-5611                          |
| J5                                                | Wait subscription cleanup                 | ☐      | thrum-pgoc                          |
| J6                                                | Ping resolves by name                     | ☐      | thrum-8ws1                          |
| J7                                                | MCP serve doesn't crash                   | ☐      | NULL display fix                    |
| J8                                                | Wait --all flag removed                   | ☐      | thrum-od0e                          |
| J9                                                | Unknown recipient hard error              | ☐      | thrum-lf1m                          |
| J10                                               | Name-only routing / auto role groups      | ☐      | thrum-8mkl                          |
| J11                                               | Group-send warning excludes @everyone     | ☐      | thrum-fsvi                          |
| J12                                               | Name≠role validation                      | ☐      | thrum-8mkl                          |
| **Part K: MCP Routing Parity**                    |                                           |        |                                     |
| K1                                                | MCP send to unknown recipient             | ☐      | thrum-lf1m                          |
| K2                                                | MCP send includes CallerAgentID           | ☐      | thrum-hvj9                          |
| K3                                                | MCP check_messages filters by agent       | ☐      | thrum-44ns                          |
| K4                                                | MCP reply includes original sender        | ☐      | thrum-n397                          |
| K5                                                | MCP waiter receives @everyone broadcasts  | ☐      | thrum-3hxv                          |
| K6                                                | MCP list_agents shows agent ID            | ☐      | thrum-5fum                          |
| K7                                                | Priority flag removed from CLI/MCP        | ☐      | Removed in v0.4.5                   |
| **Part L: Unit & Integration Tests**              |                                           |        |                                     |
| L1                                                | Run unit tests                            | ☐      |                                     |
| L2                                                | Run integration tests                     | ☐      |                                     |
| L3                                                | Run resilience tests                      | ☐      |                                     |
| L4                                                | Test coverage                             | ☐      |                                     |
| **Part M: Cleanup**                               |                                           |        |                                     |
| M1                                                | Exit Claude Code sessions                 | ☐      |                                     |
| M2                                                | Delete test agents                        | ☐      |                                     |
| M3                                                | Delete test groups                        | ☐      |                                     |
| M4                                                | Stop daemon                               | ☐      |                                     |
| M5                                                | Clean worktree data                       | ☐      |                                     |
| M6                                                | Clean temp files                          | ☐      |                                     |
| **Part N: Install & Homebrew (Release Day Only)** |                                           |        |                                     |
| N1                                                | Remove existing binary                    | ☐      |                                     |
| N2                                                | Install via curl \| sh                    | ☐      |                                     |
| N3                                                | Verify installed version                  | ☐      |                                     |
| N4                                                | Smoke test after install                  | ☐      |                                     |
| N5                                                | Verify Homebrew tap                       | ☐      |                                     |
| N6                                                | Install via Homebrew                      | ☐      |                                     |
| N7                                                | Homebrew smoke test                       | ☐      |                                     |
| N8                                                | Upgrade path                              | ☐      |                                     |
| **Part O: Remote VM Testing (leondev)**           |                                           |        |                                     |
| O1                                                | Verify VM prerequisites                   | ☐      | Homebrew, test repo                 |
| O2                                                | Install thrum via curl \| sh              | ☐      | Clean machine                       |
| O3                                                | Init & smoke test (curl install)          | ☐      | Uses littleCADev repo               |
| O4                                                | Remote negative tests                     | ☐      | Unknown recipient, --all, name≠role |
| O5                                                | Cleanup curl install                      | ☐      |                                     |
| O6                                                | Install thrum via Homebrew tap            | ☐      |                                     |
| O7                                                | Smoke test (Homebrew install)             | ☐      |                                     |
| O8                                                | Homebrew upgrade path                     | ☐      |                                     |
| O9                                                | Deploy binary via make deploy-remote      | ☐      |                                     |
| O10                                               | VM full cleanup                           | ☐      |                                     |

---

## Notes

- **Tmux tips:**
  - Attach to session: `tmux attach -t coordinator`
  - Switch sessions while attached: `Ctrl-b )` (next) or `Ctrl-b (` (prev)
  - Detach: `Ctrl-b d`
  - List sessions: `tmux list-sessions`

- **Common issues:**
  - If Claude Code hangs on startup, check for rogue mcpServers in
    .claude/settings.json
  - If identity wrong in worktree, set `THRUM_NAME` env var explicitly
  - If messages not received, verify both agents registered and daemon running
  - If wait times out, check daemon logs: `cat ~/.local/state/thrum/daemon.log`

- **Testing shortcuts:**
  - Mark status column: `☑` = pass, `☐` = pending, `☒` = fail
  - For quick smoke test, run Parts A-E only (build, plugin, daemon, messaging)
  - Full test suite takes ~45-60 minutes
