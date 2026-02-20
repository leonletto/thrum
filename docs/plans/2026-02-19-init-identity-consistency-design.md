# Thrum Init, Identity & Discovery Consistency

**Date:** 2026-02-19
**Status:** Approved
**Scope:** `thrum init`, identity files, discovery commands (`whoami`, `team`,
`agent list`, `status`, `overview`), JSON/human output parity

## Problem Statement

Thrum's initialization, identity management, and discovery commands have
accumulated inconsistencies:

1. **`thrum init` is incomplete** — creates `.thrum/` but doesn't register an
   agent, start the daemon, or set intent. Users must chain `daemon start` →
   `quickstart` manually.
2. **Identity files discard available information** — branch, intent, session ID,
   repo_id, and display are known at creation time but not persisted.
3. **Discovery commands use different data sources** — `whoami` reads the
   identity file directly; `agent list`/`team`/`status` query the daemon RPC.
   Each builds output from different structs with different fields.
4. **JSON and human output diverge** — `whoami --json` returns `identity_file`,
   `repo_id`, `worktree`, `updated_at` that the human mode omits. Human mode
   shows `Source:` that JSON mode omits.
5. **Intent and branch are missing** from `whoami`, `agent list`, and other
   discovery output.
6. **`display` and `repo_id` are never auto-populated** — always empty unless
   explicitly provided.
7. **No default intents** — agents register without intent unless the caller
   provides `--intent`.
8. **Init doesn't start the daemon** — requires a separate `daemon start` step.

## Design

### 1. Enriched Identity File (v3)

The identity file becomes the persistent source of truth for agent state.

**Current struct (`internal/config/config.go`):**

```go
type IdentityFile struct {
    Version     int         `json:"version"`
    RepoID      string      `json:"repo_id"`
    Agent       AgentConfig `json:"agent"`
    Worktree    string      `json:"worktree"`
    ConfirmedBy string      `json:"confirmed_by"`
    ContextFile string      `json:"context_file,omitempty"`
    UpdatedAt   time.Time   `json:"updated_at"`
}
```

**Proposed struct (version 3):**

```go
type IdentityFile struct {
    Version     int         `json:"version"`                  // 3
    RepoID      string      `json:"repo_id"`                  // Auto: GenerateRepoID(git remote)
    Agent       AgentConfig `json:"agent"`                    // Name, Role, Module, Display
    Worktree    string      `json:"worktree"`                 // Auto: basename of worktree
    Branch      string      `json:"branch"`                   // Auto: current git branch
    Intent      string      `json:"intent"`                   // Default or user-provided
    SessionID   string      `json:"session_id,omitempty"`     // Updated on session start
    ConfirmedBy string      `json:"confirmed_by,omitempty"`
    ContextFile string      `json:"context_file,omitempty"`
    UpdatedAt   time.Time   `json:"updated_at"`
}
```

**Auto-population logic:**

| Field | Source | Fallback |
|---|---|---|
| `repo_id` | `identity.GenerateRepoID(git remote get-url origin)` | empty (local-only repos) |
| `display` | Title-cased `"{Role} ({Module})"`, e.g., "Coordinator (main)" | empty |
| `branch` | `git branch --show-current` | `"main"` |
| `worktree` | `basename $(git rev-parse --show-toplevel)` | directory name |
| `intent` | `--intent` flag → role default → `"Working in {repo}"` | — |

**Migration:** v1/v2 files read normally — missing fields default to zero
values. On next write (init, quickstart, register), file is rewritten as v3
with all fields populated.

### 2. Canonical Output Model (`AgentSummary`)

A single struct used by all discovery commands for both JSON and human output.

```go
type AgentSummary struct {
    AgentID      string `json:"agent_id"`
    Role         string `json:"role"`
    Module       string `json:"module"`
    Display      string `json:"display,omitempty"`
    Branch       string `json:"branch,omitempty"`
    Worktree     string `json:"worktree,omitempty"`
    Intent       string `json:"intent,omitempty"`
    RepoID       string `json:"repo_id,omitempty"`
    SessionID    string `json:"session_id,omitempty"`
    SessionStart string `json:"session_start,omitempty"`
    IdentityFile string `json:"identity_file,omitempty"`
    UpdatedAt    string `json:"updated_at,omitempty"`
    Source       string `json:"source,omitempty"`
    Status       string `json:"status,omitempty"`
}
```

**Builder:**

```go
func BuildAgentSummary(idFile *IdentityFile, idPath string, daemonInfo *WhoamiResult) *AgentSummary
```

Reads identity file first (always works), enriches from daemon when available
(session start time, live status). Gracefully degrades when daemon is down.

**Rendering:**

- `FormatAgentSummary(s *AgentSummary) string` — full multi-line output for
  whoami/status
- `FormatAgentSummaryCompact(s *AgentSummary) string` — one-line for
  team/agent list contexts
- JSON mode: `json.MarshalIndent(summary)` — same fields, same data

**Rules:**

1. JSON and human mode always show the same fields
2. Standard field order: Agent ID → Role → Module → Display → Branch → Intent →
   Session → Worktree → Identity File
3. Empty optional fields omitted in both modes

**Command mapping:**

| Command | Renders via | Additional sections |
|---|---|---|
| `whoami` | `FormatAgentSummary` | — |
| `agent list` | `FormatAgentSummaryCompact` per agent | agent count header |
| `team` | `FormatAgentSummaryCompact` per member | inbox, files, hostname, worktree |
| `status` | `FormatAgentSummary` for self | daemon health, inbox |
| `overview` | `FormatAgentSummary` for self | team, inbox, sync |

**Replaces:**

- Ad-hoc `map[string]any` in standalone `whoami` (main.go:1137)
- `WhoamiResult`-based `FormatWhoami` in `agent.go`
- Separate formatting in `FormatStatus`, `FormatOverview` for the agent section

### 3. `thrum init` Full Setup Flow

Init becomes the single entry point for setting up thrum in a repository.

**Interactive flow (no flags):**

```
$ thrum init

✓ Created .thrum/ directory structure
✓ Created a-sync branch for message sync
✓ Updated .gitignore

Agent setup:
  Name [coordinator]: _
  Role [implementer]: coordinator
  Module [main]: _

Detected AI runtimes:
  1. Claude Code    (.claude/settings.json)
  2. cli-only
Which is your primary runtime? [1]: _

Intent: Coordinate agents and tasks in thrum
  Edit? [Y/n]: _

✓ Runtime saved to .thrum/config.json (primary: claude)
✓ Daemon started (PID 12345)
✓ Agent registered: coordinator
✓ Session started: ses_01ABC...
✓ Intent set

Agent ID:  coordinator
Role:      @coordinator
Module:    main
Display:   Coordinator (main)
Branch:    main
Intent:    Coordinate agents and tasks in thrum
Session:   ses_01ABC... (just now)
Worktree:  thrum
Identity:  .thrum/identities/coordinator.json
```

**Non-interactive flow (flags provided):**

```bash
thrum init --agent-name coordinator --agent-role coordinator \
  --module main --runtime claude
```

Skips prompts, uses role default for intent, shows same whoami-style
confirmation output.

**Steps in order:**

1. Create `.thrum/` directory structure
2. Prompt for agent name, role, module (or use flags)
3. Detect/select runtime (existing interactive flow)
4. Generate runtime config files
5. Compute default intent, present for editing (interactive) or use default
6. Auto-populate all identity fields (repo_id, display, branch, worktree)
7. Write identity file (v3)
8. Start daemon (if not already running)
9. Register agent with daemon
10. Start session
11. Set intent
12. Display `FormatAgentSummary` output

**`quickstart` compatibility:** Becomes a thin wrapper. If `.thrum/` doesn't
exist, calls init logic first. If it does, re-registers and enriches the
identity file with any missing v3 fields.

### 4. Default Intents

Role-based defaults stored in a map, `{repo}` replaced with repo name from
`basename $(git rev-parse --show-toplevel)`:

| Role | Default Intent |
|---|---|
| `coordinator` | Coordinate agents and tasks in {repo} |
| `implementer` | Implement features and fixes in {repo} |
| `reviewer` | Review code and PRs in {repo} |
| `planner` | Plan architecture and design in {repo} |
| `tester` | Test and validate changes in {repo} |
| *(other)* | Working in {repo} |

Intent updates via `set-intent` also write back to the identity file on disk.

### 5. Intent and Branch in Discovery Output

All discovery commands that show agent information include intent and branch
when available:

- **`whoami`**: shows intent and branch (from identity file, enriched from
  daemon)
- **`agent list`**: shows branch and intent per agent in the box-drawing tree
- **`team`**: shows worktree, branch, and intent per member (worktree was
  previously missing from some views)
- **`status`**: agent section shows intent and branch
- **`overview`**: identity section shows intent and branch

### 6. Backwards Compatibility

- **Identity files:** v1/v2 read without error; missing fields default to zero
  values; rewritten as v3 on next write
- **Quickstart:** all existing flags preserved; startup scripts unchanged
- **Daemon RPC:** `agent.whoami` response gains `branch` and `intent` fields
  (additive, non-breaking); no protocol version bump
- **Startup scripts:** `thrum-startup.sh` templates that use `quickstart`
  continue working — quickstart enriches the file on each run

## Files Affected

### New files
- `internal/cli/defaults.go` — default intents map, auto-population helpers,
  `BuildAgentSummary`, `FormatAgentSummary`, `FormatAgentSummaryCompact`
- `internal/cli/defaults_test.go` — tests for the above

### Modified files
- `internal/config/config.go` — `IdentityFile` struct v3 with branch, intent,
  session_id fields
- `internal/cli/init.go` — init logic expanded to include agent setup, daemon
  start, registration
- `cmd/thrum/main.go` — init command handler: interactive prompts, whoami
  output; whoami command: use `BuildAgentSummary`; quickstart: thin wrapper
  calling init
- `internal/cli/agent.go` — `FormatAgentList`/`FormatAgentListWithContext` use
  `AgentSummary`; remove old `FormatWhoami`
- `internal/cli/status.go` — `WhoamiResult` gains branch/intent fields;
  `FormatStatus` uses `FormatAgentSummary` for agent section
- `internal/cli/team.go` — `FormatTeam` includes worktree consistently, renders
  through `AgentSummary`
- `internal/cli/overview.go` — identity section uses `FormatAgentSummary`
- `internal/cli/quickstart.go` — writes enriched v3 identity file; becomes thin
  wrapper when `.thrum/` missing
- `internal/daemon/rpc/agent.go` — `whoami` RPC returns branch and intent
- Tests for all modified files

## Non-Goals

- Changing the daemon protocol version
- Changing the a-sync branch format
- Modifying MCP tool output (separate concern)
- Multi-worktree identity sharing (already handled by redirect)
