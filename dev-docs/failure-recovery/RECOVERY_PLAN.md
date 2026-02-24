# Recovery Plan: Lost UI Work from Sessions 3-6

## What Happened

### Timeline
1. **Session 2** — v0.5.0 release. All changes pushed to GitHub. **Nothing lost.**
2. **Session 3** — Planned Slack-style message redesign. Plan rejected, session continued into Session 4.
3. **Session 4 / Session 6** (same session, split across files):
   - CSS variable migration: replaced all hardcoded `cyan-*` Tailwind classes with CSS variable tokens across 16+ files
   - Committed locally as `e00e795` (20 files, 464 ins, 356 del) — **never pushed**
   - Additional UI fixes committed as `21dd2e0` (10 files, 115 ins, 34 del) — **never pushed**
   - Ran full test plan (67/68 pass)
   - **Disaster**: Agent's CWD drifted into `.thrum` internal sync worktree. Git broke. User had to exit.
4. **Session 5** (longest session):
   - Full test plan execution with screenshots
   - ComposeBar two-row Slack-style layout redesign
   - MentionAutocomplete: show agent display names instead of roles
   - Daemon restart port preservation fix
   - URL hash routing for navigation persistence
   - Polling fallback for real-time messages
   - **None of this was committed**

### Root Cause
The agent's shell CWD somehow resolved into the `.thrum/` internal detached worktree directory, which has a `.git` folder containing only sync metadata (no HEAD, config, or objects). From that point, all git operations failed. The user recloned from GitHub, losing:
- 2 unpushed local commits
- All uncommitted work from Session 5

### What Was Preserved
- The installed Go binary contains the latest embedded UI (backed up as `thrum-binary-backup-20260223`)
- All session transcripts (session1.txt through session6.txt)
- Everything pushed to GitHub through v0.5.0 release

---

## Inventory of Lost Changes

### Group A: CSS Variable Migration (originally commit e00e795)
**20 files, 464 insertions, 356 deletions**

#### A1. New CSS Variables in index.css
Add to BOTH `:root` and `.dark` sections:

| Variable | Light Value | Dark Value | Purpose |
|---|---|---|---|
| `--accent-glow-soft` | `transparent` | `rgba(56, 189, 248, 0.3)` | Button shadow |
| `--accent-glow-hover` | `transparent` | `rgba(56, 189, 248, 0.6)` | Button hover shadow |
| `--mention-highlight` | `rgba(8, 145, 178, 0.1)` | `rgba(56, 189, 248, 0.15)` | Mention bg (others) |
| `--mention-highlight-self` | `rgba(8, 145, 178, 0.2)` | `rgba(56, 189, 248, 0.3)` | Mention bg (self) |

#### A2. Canonical Replacement Mapping

| Tailwind Pattern | CSS Variable Replacement |
|---|---|
| `text-cyan-400`, `text-cyan-500` | `text-[var(--accent-color)]` |
| `text-cyan-300` | `text-[var(--text-secondary)]` |
| `text-cyan-600` | `text-[var(--text-muted)]` |
| `text-cyan-700`, `text-cyan-800` | `text-[var(--text-faint)]` |
| `hover:text-cyan-300`, `hover:text-cyan-500` | `hover:text-[var(--accent-color)]` |
| `bg-cyan-900/20`, `bg-cyan-900/30` | `bg-[var(--accent-subtle-bg)]` |
| `bg-cyan-900/40`, `bg-cyan-900/50` | `bg-[var(--accent-subtle-bg-hover)]` |
| `hover:bg-cyan-900/20`, `hover:bg-cyan-900/30` | `hover:bg-[var(--accent-subtle-bg)]` |
| `hover:bg-cyan-500/5`, `hover:bg-cyan-500/10` | `hover:bg-[var(--accent-subtle-bg)]` |
| `border-cyan-500/10` through `/30` | `border-[var(--accent-border)]` |
| `border-cyan-500/40`, `border-cyan-500/50` | `border-[var(--accent-border-hover)]` |
| `border-cyan-900/40` | `border-[var(--accent-border)]` |
| `border-cyan-400` | `border-[var(--accent-color)]` |
| `ring-cyan-500` | `ring-[var(--accent-color)]` |
| `from-cyan-600` | `from-[var(--accent-hover)]` |
| `to-cyan-500` | `to-[var(--accent-color)]` |
| `divide-cyan-500/10` | `divide-[var(--accent-border)]` |
| `placeholder:text-cyan-800` | `placeholder:text-[var(--text-faint)]` |
| `bg-[#0d1120]` | `bg-[var(--panel-bg-start)]` |
| `from-slate-900 to-slate-800` | `from-[var(--panel-bg-start)] to-[var(--panel-bg-end)]` |
| Button shadow `rgba(56,189,248,...)` | `shadow-[0_0_15px_var(--accent-glow-soft)]` |
| Button hover shadow rgba | `hover:shadow-[0_0_25px_var(--accent-glow-hover)]` |
| Mention bg inline `rgba(56,189,248,0.15)` | `var(--mention-highlight)` |
| Mention bg self inline `rgba(56,189,248,0.3)` | `var(--mention-highlight-self)` |
| Mention text inline styles | `var(--accent-color)` |

#### A3. Files to Modify

**UI Primitives (5 files):**
1. `ui/packages/web-app/src/components/ui/button.tsx` — 5 cyan + 2 shadow rgbas
2. `ui/packages/web-app/src/components/ui/card.tsx` — 1 cyan + 1 shadow + slate gradient
3. `ui/packages/web-app/src/components/ui/dialog.tsx` — 1 cyan + 1 shadow + gradient → use `--panel-bg-start`/`--panel-bg-end`
4. `ui/packages/web-app/src/components/ui/badge.tsx` — 3 cyan patterns
5. `ui/packages/web-app/src/components/ui/ScopeBadge.tsx` — 3 cyan classes

**Feature Components (8 files):**
6. `ui/packages/web-app/src/components/inbox/ComposeBar.tsx` — 19 cyan + 1 hex
7. `ui/packages/web-app/src/components/groups/GroupChannelView.tsx` — 38 cyan + 2 hex (largest file)
8. `ui/packages/web-app/src/components/inbox/MessageBubble.tsx` — 3 inline style rgba values
9. `ui/packages/web-app/src/components/status/HealthBar.tsx` — 2 cyan
10. `ui/packages/web-app/src/components/agents/AgentCard.tsx` — 1 cyan
11. `ui/packages/web-app/src/components/Sidebar.tsx` — 5 cyan
12. `ui/packages/web-app/src/components/coordination/WhoHasView.tsx` — 3 cyan
13. `ui/packages/web-app/src/components/subscriptions/SubscriptionPanel.tsx` — 2 cyan

**Dropdown fix (1 file):**
14. `ui/packages/web-app/src/components/inbox/MentionAutocomplete.tsx` — `bg-popover` → `bg-[var(--panel-bg-start)]`

**Test files (3 files):**
15. `ui/packages/web-app/src/components/status/__tests__/HealthBar.test.tsx`
16. `ui/packages/web-app/src/components/agents/__tests__/AgentCard.test.tsx`
17. `ui/packages/web-app/src/components/ui/__tests__/ScopeBadge.test.tsx`

**IMPORTANT: `bg-[#0d1120]` must map to `bg-[var(--panel-bg-start)]` (NOT `--app-bg-secondary`).** The original implementation used `--app-bg-secondary` which made slide-out panels and dropdowns transparent in light mode. This was caught and fixed in the same session.

#### A4. Leave Untouched
- Yellow/amber: warning states in ComposeBar
- Red: destructive/error states
- Purple: role-type indicators in GroupChannelView
- Gray: neutral text in HealthBar

---

### Group B: UI Feature Fixes (originally commit 21dd2e0)
**10 files, 115 insertions, 34 deletions**

#### B1. Daemon Restart Port Preservation
**File:** `internal/cli/daemon.go`

In `DaemonRestart()`, read the previous WebSocket port BEFORE calling `DaemonStop` (which deletes `ws.port`), then pass it via `THRUM_WS_PORT` env var to the new daemon process:

```go
func DaemonRestart(repoPath string, localOnly bool) error {
    prevPort := ReadWebSocketPort(repoPath)
    _ = DaemonStop(repoPath)
    time.Sleep(500 * time.Millisecond)
    if prevPort > 0 {
        os.Setenv("THRUM_WS_PORT", fmt.Sprintf("%d", prevPort))
        defer os.Unsetenv("THRUM_WS_PORT")
    }
    return DaemonStart(repoPath, localOnly)
}
```

#### B2. URL Hash Routing for Navigation Persistence
**File:** `ui/packages/shared-logic/src/stores/uiStore.ts`

Add functions to sync UI state to/from `window.location.hash`:
- `stateFromHash()` — parses hash like `#view=my-inbox&agent=xyz` into UIState
- `stateToHash()` — writes UIState to hash via `replaceState` (no history spam)
- `initialState` merges defaults with hash-restored values
- `uiStore.subscribe()` calls `stateToHash` on every change

#### B3. Polling Fallback for Messages
**File:** `ui/packages/shared-logic/src/hooks/useMessage.ts`

Add `refetchInterval: 5000` to `useMessageList` query options. Browser clients don't have WebSocket push subscriptions (requires agent session), so 5-second polling is the primary refresh mechanism.

#### B4. Polling Fallback for Sessions
**File:** `ui/packages/shared-logic/src/hooks/useSession.ts`

Add `refetchInterval: 30000` to `useSessionList` query options.

---

### Group C: ComposeBar Redesign (uncommitted from Session 5)

#### C1. MentionAutocomplete Agent Name Fix
**File:** `ui/packages/web-app/src/components/inbox/MentionAutocomplete.tsx`

Change agent suggestion labels:
- `label: agent.role` → `label: agent.display || agent.agent_id.replace(/^(agent|user):/, '')`
- `sublabel: agent.display` → `sublabel: agent.role`

Result: typing `@coordinator_` shows "coordinator_main" as suggestion, not just "coordinator"

#### C2. ComposeBar Two-Row Layout
**File:** `ui/packages/web-app/src/components/inbox/ComposeBar.tsx`

Restructure from single-row to two-row Slack-style:

**Before:**
```
[To: chips Select] [textarea] [Send]
```

**After:**
```
┌─────────────────────────────────────────────┐
│ To: @chip @chip  [+ Add]                    │  ← Row 1 (addressing)
├─────────────────────────────────────────────┤
│ Write a message...                  [Send]  │  ← Row 2 (message)
└─────────────────────────────────────────────┘
```

Changes:
- Add `removeChip()` function for dismissing recipient chips
- Remove `Users` import from lucide-react
- Row 1: To: label + combined chips (union of selectedRecipients + @mentions) + "+ Add" button
- Row 2: Full-width MentionAutocomplete + inline Send button
- Chips from selectedRecipients have X button for removal
- Hidden when `groupScope` is set

#### C3. MentionAutocomplete Test Updates
**File:** `ui/packages/web-app/src/components/inbox/__tests__/MentionAutocomplete.test.tsx`

- Mock display values: `'Assistant Bot'` → `'assistant_main'`, `'Research Agent'` → `'researcher_main'`, `'Testing Agent'` → `'tester_main'`
- All `@assistant` assertions → `@assistant_main`, etc.
- Mention extraction: `['assistant']` → `['assistant_main']`, etc.
- Typed text: `'Hello @assistant and @researcher'` → `'Hello @assistant_main and @researcher_main'`, etc.

---

### Group D: Additional Corrections Found During Implementation (from Session 4)

These are fixes the user identified during live testing that were applied on top of the CSS migration.

#### D1. MessageList: Fix React hooks ordering + auto-scroll
**File:** `ui/packages/web-app/src/components/inbox/MessageList.tsx`

The Slack-style redesign (from Session 3 plan) added `scrollEndRef`, `prevMessageCountRef`, `useLayoutEffect`, and `useEffect` hooks AFTER early returns (loading/empty states). This violates React's Rules of Hooks ("Rendered more hooks than during the previous render" — error #310).

Fix: Move these hooks ABOVE the early returns. Also:
- `useLayoutEffect` scrolls to bottom on initial render
- `useEffect` scrolls to bottom when `messages.length` increases (new messages)
- Conversations are `.slice().reverse()` so newest appear at bottom

#### D2. DashboardPage: Remove browser notification auto-request
**File:** `ui/packages/web-app/src/pages/DashboardPage.tsx`

Remove the `useBrowserNotifications` call and `useCurrentUser` import from DashboardPage. The browser notification prompt was annoying users. Keep `useCurrentUser` in the mock for `DashboardPage.test.tsx` since child components still use it.

#### D3. Theme toggle: Make it actually work
**File:** `ui/packages/web-app/src/index.css`

The theme system (useTheme hook, Tailwind `darkMode: ['class']`) was wired up but non-functional because `:root` and `.dark` had identical dark values. The fix is the CSS variable migration itself (Group A) — with proper light/dark values in `:root` vs `.dark`, the theme toggle works.

#### D4. MentionAutocomplete: Search by agent_id + display + role
**File:** `ui/packages/web-app/src/components/inbox/MentionAutocomplete.tsx`

The original filter only matched `agent.role`. User reported typing `@coordinator_main` only showed groups. Fix: search across `agent.role`, `agent.agent_id`, and `agent.display`. Also separate agent and group suggestions so agents appear first.

This was done in Session 4 (before the Session 5 label change). The Session 5 label change (`agent.display || agent.agent_id.replace(...)`) builds on top of this search improvement.

#### D5. MessageBubble: Fix hardcoded hex colors for light mode
**File:** `ui/packages/web-app/src/components/inbox/MessageBubble.tsx`

User reported light mode was "almost unreadable." The Slack-style flat row layout used hardcoded hex colors (`text-[#e2e8f0]`, `text-[#cbd5e1]`, `text-[#64748b]`) that are invisible on white backgrounds. Fix: replace with CSS variables (`text-[var(--text-secondary)]`, etc.).

Also in `MessageBubble.test.tsx`: update hover class assertion from `hover:bg-[rgba(56,189,248,0.04)]` to `hover:bg-[var(--accent-subtle-bg)]`.

#### D6. Tailwind config: Revert cyan palette override attempt
**File:** `ui/packages/web-app/tailwind.config.js`

Session 4 attempted to override the cyan palette with CSS custom properties (`rgb(var(--cyan-50) / <alpha-value>)`). The user rejected this as "edge case overrides is bad practice" and directed using CSS variables directly instead. The proper fix is Group A (replacing hardcoded cyan classes with CSS var tokens). **Do NOT modify tailwind.config.js.**

---

### Group E: User's New Request — Show git user name in UI

The UI currently shows "thrum" as the user's display name. The `user.identify` RPC already reads `git config user.name` and `user.email` (see `internal/daemon/rpc/user.go` and `ui/packages/shared-logic/src/hooks/useAuth.ts`). The auto-registration flow in `AppShell.tsx` should call `user.identify` to get the real name and pass it to `user.register`.

This is a new feature, not a recovery item. Implement after recovery is complete.

---

## Recovery Execution Plan

### Phase 1: CSS Variable Migration (Groups A + D5 + D6)
- Apply the canonical mapping to all 17 files (Group A)
- Fix MessageBubble hardcoded hex colors for light mode (D5)
- Do NOT modify tailwind.config.js (D6)
- Run tests and build

### Phase 2: UI Structural Fixes (Groups B + D1 + D2 + D3)
- Daemon port fix (B1)
- URL hash routing (B2)
- Polling fallbacks (B3, B4)
- MessageList hooks ordering + auto-scroll (D1)
- Remove browser notification auto-request (D2)
- Theme toggle works via the CSS var migration (D3 — no separate action needed)
- Run Go build and UI tests

### Phase 3: ComposeBar Redesign + MentionAutocomplete (Groups C + D4)
- MentionAutocomplete: search by agent_id + display + role (D4)
- MentionAutocomplete: show display names as labels (C1)
- ComposeBar two-row layout (C2)
- Test updates (C3)
- Run tests and build

### Phase 4: Verification
1. `cd ui && pnpm test -- -- --run` — all 473+ tests pass
2. `cd ui && pnpm build` — clean build
3. `go build ./cmd/thrum/` — Go compiles
4. `grep -r "cyan-" ui/packages/web-app/src/components/ --include="*.tsx"` — 0 results
5. `grep -r "#0d1120" ui/packages/web-app/src/ --include="*.tsx"` — 0 results
6. `make install && thrum daemon restart` — port preserved
7. Visual check via Playwright: open UI, verify dark mode looks same, light mode readable
8. Visual check: refresh page, verify navigation state preserved
9. Visual check: type `@coordinator_` in compose, verify agent names appear first

### Phase 5: Commit & Push
Create 3 commits matching the original intent:
1. `refactor(ui): replace hardcoded cyan classes with CSS variable tokens`
2. `feat(ui): WS port reuse, URL hash routing, polling fallbacks, hooks fix`
3. `feat(ui): ComposeBar two-row layout, agent name autocomplete`

Then `git push origin main`.

### Phase 6: New Feature — Git User Name in UI (Group E)
After recovery is verified, implement the user's request to show `git config user.name` instead of "thrum" in the UI.

### Phase 7: Agent View Redesign — Match Group Channel Minimalism (Group F)
Redesign the agent-inbox header to match the group channel pattern.

---

## Group F: Agent View Redesign (New Work)

### Problem
The agent-inbox view uses `AgentContextPanel` which renders a large bordered card at the top showing 6-8 rows of metadata (AGENT, AGENT ID, INTENT, BRANCH, UNCOMMITTED, CHANGED, HEARTBEAT) plus a prominent red "Delete Agent" button always visible inline. This card takes significant vertical space, pushes messages down, and when agents have modified files, it dominates the view.

### Goal
Match the group channel's minimalist pattern: a thin top bar with key info + a gear icon that opens a slide-out panel for details and destructive actions.

### Current Architecture

**Group channel (good pattern — keep):**
- File: `ui/packages/web-app/src/components/groups/GroupChannelView.tsx`
- Header: Single-line thin bar with `#groupname`, member count badge, MEMBERS link, gear icon
- Settings: Slide-out panel (absolute positioned, w-72) opened by gear icon
- Delete: Hidden inside the settings slide-out panel, behind a confirmation dialog
- Result: Messages get maximum vertical space

**Agent inbox (needs redesign):**
- File: `ui/packages/web-app/src/components/agents/AgentContextPanel.tsx`
- Header: Large bordered card with 6-8 data rows in a grid
- Delete: Prominent red button always visible at the bottom of the card
- Result: Card dominates the view, especially when files are present

### Proposed Design

Replace the AgentContextPanel card with a thin header bar + gear icon slide-out, following the GroupChannelView pattern:

**New agent header bar:**
```
┌────────────────────────────────────────────────────────────┐
│ 🟢 coordinator_main  coordinator  "Running test plan"  ⚙  │
│    (status dot) (name)    (role)       (intent)    (gear)  │
└────────────────────────────────────────────────────────────┘
```

- Status indicator (online/offline dot)
- Agent display name (bold)
- Role as subtle badge
- Intent text (truncated if long)
- Gear icon → opens slide-out panel on right

**Gear slide-out panel (same pattern as group settings):**
```
┌──────────────────┐
│ Agent Details   ✕ │
├──────────────────┤
│ AGENT ID         │
│ coordinator_main │
│                  │
│ ROLE             │
│ coordinator      │
│                  │
│ INTENT           │
│ Running test... │
│                  │
│ BRANCH           │
│ main             │
│                  │
│ UNCOMMITTED      │
│ 3 files          │
│  - file1.tsx     │
│  - file2.tsx     │
│  - file3.tsx     │
│                  │
│ CHANGED          │
│ 5 files          │
│                  │
│ HEARTBEAT        │
│ 2m ago           │
│──────────────────│
│  🗑 Delete Agent │
└──────────────────┘
```

### Files to Modify
1. `ui/packages/web-app/src/components/agents/AgentContextPanel.tsx` — Restructure from card to thin header + slide-out
2. Possibly `ui/packages/web-app/src/components/inbox/InboxView.tsx` — If the panel is mounted there
3. Test files for AgentContextPanel

### Screenshots Reference
Screenshots captured in `dev-docs/failure-recovery/screenshots/`:
- `03-group-everyone.png` — Group header (the good pattern)
- `04-group-gear-settings.png` — Group gear slide-out panel
- `05-agent-coordinator-main.png` — Agent card (the problem)
- `08-agent-coordinator-main-full.png` — Agent card taking up space
- `11-dark-live-feed.png` — Dark mode reference
- `12-dark-agent-view.png` — Dark mode agent card
- `13-dark-group-view.png` — Dark mode group view

---

## Reference: Binary Backup
The file `thrum-binary-backup-20260223` contains the last installed binary with the embedded UI reflecting all these changes. It can be run to visually verify what the UI should look like.
