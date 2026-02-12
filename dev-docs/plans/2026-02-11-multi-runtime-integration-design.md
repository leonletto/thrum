# Multi-Runtime Integration Design

**Date:** 2026-02-11
**Status:** Draft
**Version:** 1.0

## Overview

Add first-class multi-runtime support to Thrum so any AI coding agent (Claude Code, Codex, Gemini, Cursor, Auggie, Amp, or custom) can use Thrum for coordination without modification.

**Goal:** Transform Thrum from "Claude Code coordination tool" to "universal AI agent coordination layer" through runtime-agnostic templates, configuration generation, and documentation.

**Key Insight:** Thrum is already 80% runtime-agnostic. Zero Claude Code dependencies exist in Go source code. The 20% gap is: (1) `thrum init` command to generate runtime-specific configs, (2) a runtime preset registry, (3) `thrum context prime` for session recovery, and (4) documentation/templates.

## Context

### Current State

**What Already Works:**
- Three universal integration paths: MCP Server (stdio JSON-RPC), CLI Commands (bash + JSON), Direct RPC (Unix socket + WebSocket)
- All core functionality (messaging, agent registration, session management) accessible via CLI with `--json` output
- MCP is an open protocol (Linux Foundation, 2025) with first-class support in Claude, ChatGPT, Codex, Cursor, Gemini, VS Code
- File-based identity system (`.thrum/identities/`) works with all runtimes
- Zero runtime-specific code in Go source (all Claude specificity is in markdown docs)

**What's Missing:**
- Runtime detection and config generation (`thrum init --runtime <name>`)
- Runtime preset registry (inspired by Gastown's `agents.go`)
- Universal startup script that works from any runtime
- Context recovery command (`thrum context prime --json`)
- Per-runtime integration guides (Codex, Cursor, Gemini)
- Template system for generating runtime-specific configs

**Design Principles:**
1. **CLI-First, MCP-Enhanced** - CLI is the universal baseline, MCP is an optimization layer
2. **File-Based State** - Use files (`.thrum/checkpoint.json`, `.thrum/identities/`) not hooks/memory
3. **Runtime-Agnostic Core** - All features must work via CLI, not just MCP
4. **Graceful Degradation** - If MCP fails, fall back to CLI automatically

## Architecture

This design covers two priority epics:
- **Epic 1 (P0):** `thrum init` Command & Runtime Templates
- **Epic 2 (P1):** Runtime Preset Registry & Enhanced Quickstart

Additional context recovery features (`thrum context prime`, checkpoint system) and documentation are considered P2 work items, tracked separately.

### Epic 1: `thrum init` Command & Runtime Templates (P0)

**Purpose:** Generate runtime-specific configuration files based on detected or specified runtime.

**User Experience:**
```bash
# Auto-detect runtime from environment
thrum init

# Explicit runtime selection
thrum init --runtime claude
thrum init --runtime codex
thrum init --runtime cursor
thrum init --runtime gemini
thrum init --runtime cli-only

# Generate all runtime configs
thrum init --runtime all
```

#### 1.1 Runtime Detection

**Implementation:** `internal/cli/init.go`

**Detection Logic:**
```go
func DetectRuntime(repoPath string) string {
    // Check file markers
    checks := []struct {
        marker string
        name   string
    }{
        {".claude/settings.json", "claude"},
        {".codex", "codex"},
        {".cursorrules", "cursor"},
    }
    for _, c := range checks {
        if fileExists(filepath.Join(repoPath, c.marker)) {
            return c.name
        }
    }

    // Check environment variables
    envChecks := []struct {
        envVar string
        name   string
    }{
        {"CLAUDE_SESSION_ID", "claude"},
        {"CURSOR_SESSION", "cursor"},
        {"GEMINI_CLI", "gemini"},
    }
    for _, c := range envChecks {
        if os.Getenv(c.envVar) != "" {
            return c.name
        }
    }

    return "cli-only" // Universal fallback
}
```

**File Markers Reference:**
| Runtime | File Marker | Env Var |
|---------|-------------|---------|
| Claude  | `.claude/settings.json` | `CLAUDE_SESSION_ID` |
| Codex   | `.codex/` | None |
| Cursor  | `.cursorrules` | `CURSOR_SESSION` |
| Gemini  | None | `GEMINI_CLI` |

#### 1.2 Template Engine

**Embedded Templates:** Use Go's `embed.FS` for portability

**Implementation:**
```go
//go:embed templates/*
var templateFS embed.FS

type TemplateData struct {
    AgentName   string
    AgentRole   string
    AgentModule string
    MCPCommand  string
}

func RenderTemplate(runtime, templateName string, data TemplateData) (string, error) {
    tmpl, err := template.ParseFS(templateFS, fmt.Sprintf("templates/%s/%s", runtime, templateName))
    if err != nil {
        return "", err
    }

    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil {
        return "", err
    }

    return buf.String(), nil
}
```

**Template Variables:**
- `{{.AgentName}}` - Agent identifier (e.g., "alice")
- `{{.AgentRole}}` - Role (e.g., "implementer", "reviewer")
- `{{.AgentModule}}` - Module/area of work (e.g., "auth", "ui")
- `{{.MCPCommand}}` - Full MCP command (e.g., "thrum mcp serve")

#### 1.3 Template Files

**Directory Structure:**
```
internal/cli/templates/
├── claude/
│   ├── settings.json.tmpl      # MCP + hooks config
│   └── session-start.sh.tmpl   # SessionStart hook script
├── codex/
│   ├── session-start.sh.tmpl   # .codex/hooks/session-start
│   ├── AGENTS.md.tmpl          # Instructions file
│   └── mcp-setup.md            # Guide: codex mcp add thrum
├── cursor/
│   ├── cursorrules.tmpl        # .cursorrules file
│   └── mcp-setup.md            # Settings > Tools & MCP guide
├── gemini/
│   ├── instructions.md.tmpl    # ~/.gemini/instructions.md
│   ├── settings.json.tmpl      # ~/.gemini/settings.json (MCP)
│   └── profile.sh.tmpl         # ~/.gemini/profile.sh (startup)
├── cli-only/
│   └── polling-loop.sh         # Message polling script
└── shared/
    └── startup.sh.tmpl         # Universal startup script
```

**Template Snippets:**

**Claude Settings Template (`.claude/settings.json`):**
```json
{
  "mcpServers": {
    "thrum": {
      "type": "stdio",
      "command": "thrum",
      "args": ["mcp", "serve"]
    }
  },
  "hooks": {
    "SessionStart": [{
      "hooks": [{
        "type": "command",
        "command": "export THRUM_NAME={{.AgentName}} && bash scripts/thrum-startup.sh"
      }]
    }]
  }
}
```

**Codex Startup Hook (`.codex/hooks/session-start`):**
```bash
#!/bin/bash
# Auto-generated by thrum init --runtime codex
export THRUM_NAME={{.AgentName}}
export THRUM_ROLE={{.AgentRole}}
export THRUM_MODULE={{.AgentModule}}

# Ensure daemon running
thrum daemon start || true

# Register agent
thrum quickstart \
  --name "$THRUM_NAME" \
  --role "$THRUM_ROLE" \
  --module "$THRUM_MODULE" \
  --json

# Check inbox
thrum inbox --unread --json
```

**Cursor Instructions Template (`.cursorrules`):**
```
# Thrum Agent Coordination

This project uses Thrum for multi-agent coordination.

## Setup
Register: thrum quickstart --name {{.AgentName}} --role {{.AgentRole}} --module {{.AgentModule}}

## Commands
- Send: thrum send "msg" --to @agent --json
- Inbox: thrum inbox --unread --json
- Wait: thrum wait --all --timeout 5m --json
- Agents: thrum agent list --json

All commands support --json for machine-parsable output.
Exit codes: 0=success, 1=timeout, 2=error
```

#### 1.4 Universal Startup Script

**Purpose:** Single script that works from any runtime via hooks, shell, or extension

**File:** `scripts/thrum-startup.sh`

**Implementation:**
```bash
#!/bin/bash
# Universal Thrum agent startup script
set -e

# Configuration (override via env vars or args)
AGENT_NAME="${THRUM_NAME:-${1:-default_agent}}"
AGENT_ROLE="${THRUM_ROLE:-${2:-implementer}}"
AGENT_MODULE="${THRUM_MODULE:-${3:-main}}"
AGENT_INTENT="${THRUM_INTENT:-General agent work}"

# 1. Ensure daemon is running
if ! thrum daemon status &>/dev/null; then
  thrum daemon start
fi

# 2. Register agent (idempotent)
thrum quickstart \
  --name "$AGENT_NAME" \
  --role "$AGENT_ROLE" \
  --module "$AGENT_MODULE" \
  --intent "$AGENT_INTENT" \
  --json

# 3. Check inbox and output context
thrum inbox --unread --json

# 4. Optional: Announce presence
if [ "${THRUM_ANNOUNCE:-false}" = "true" ]; then
  thrum send "Agent $AGENT_NAME online" --broadcast --priority low --json
fi
```

**Usage from Runtime Hooks:**

**Claude:**
```json
{
  "hooks": {
    "SessionStart": [{
      "hooks": [{
        "type": "command",
        "command": "bash scripts/thrum-startup.sh"
      }]
    }]
  }
}
```

**Codex:**
```bash
# .codex/hooks/session-start
source scripts/thrum-startup.sh codex_agent implementer auth
```

**Cursor (extension):**
```typescript
terminal.sendText("bash scripts/thrum-startup.sh cursor_agent reviewer ui");
```

**Gemini:**
```bash
# ~/.gemini/profile.sh
if [ -f "$PWD/scripts/thrum-startup.sh" ]; then
  source "$PWD/scripts/thrum-startup.sh" gemini_agent planner design
fi
```

#### 1.5 JSON Output Verification

**Purpose:** Ensure all CLI commands support `--json` flag for machine parsing

**Test Script:** `scripts/verify-json-output.sh`
```bash
#!/bin/bash
# Verify JSON output coverage

set -e

echo "Testing JSON output for all commands..."

# Agent commands
thrum agent list --json
thrum agent list --context --json
thrum quickstart --name test --role test --module test --json

# Messaging commands
thrum send "test" --to @test --json
thrum inbox --json
thrum inbox --unread --json

# Session commands
thrum status --json
thrum overview --json

# Coordination commands
thrum who-has test.go --json

# Daemon commands
thrum daemon status --json

# Wait command (expect timeout exit 1)
thrum wait --timeout 1s --json || [ $? -eq 1 ]

echo "All commands support JSON output ✓"
```

**Missing Commands to Fix:**
- Any command without `--json` flag needs retrofitting
- Verify exit codes are consistent (0=success, 1=timeout/not-found, 2=error)

#### 1.6 File Output Strategy

**Files Created by Runtime:**

| Runtime | Files Created | Location |
|---------|--------------|----------|
| `claude` | `settings.json`<br>`scripts/thrum-startup.sh` | `.claude/`<br>`scripts/` |
| `codex` | `session-start`<br>`AGENTS.md`<br>`mcp-setup.md` | `.codex/hooks/`<br>root<br>`docs/` |
| `cursor` | `.cursorrules`<br>`mcp-setup.md`<br>`scripts/thrum-startup.sh` | root<br>`docs/`<br>`scripts/` |
| `gemini` | `instructions.md`<br>`settings.json`<br>`profile.sh` | `~/.gemini/`<br>`~/.gemini/`<br>`~/.gemini/` |
| `cli-only` | `scripts/thrum-startup.sh`<br>`docs/cli-usage.md` | `scripts/`<br>`docs/` |

**Conflict Handling:**
- If file exists, prompt user: `File .claude/settings.json already exists. Overwrite? [y/N]`
- Add `--force` flag to skip prompts
- Add `--dry-run` flag to preview changes without writing

### Epic 2: Runtime Preset Registry & Enhanced Quickstart (P1)

**Purpose:** Add runtime metadata system (inspired by Gastown) and enhance `thrum quickstart` with runtime awareness

#### 2.1 Preset Data Model

**Implementation:** `internal/runtime/presets.go`

**Struct Definition:**
```go
package runtime

type RuntimePreset struct {
    Name             string   `json:"name"`
    DisplayName      string   `json:"display_name"`
    Command          string   `json:"command"`
    MCPSupported     bool     `json:"mcp_supported"`
    HooksSupported   bool     `json:"hooks_supported"`
    InstructionsFile string   `json:"instructions_file"`
    MCPConfigPath    string   `json:"mcp_config_path"`
    SetupNotes       string   `json:"setup_notes"`
}

var BuiltinPresets = map[string]RuntimePreset{
    "claude": {
        Name:             "claude",
        DisplayName:      "Claude Code",
        Command:          "claude",
        MCPSupported:     true,
        HooksSupported:   true,
        InstructionsFile: "CLAUDE.md",
        MCPConfigPath:    ".claude/settings.json",
        SetupNotes:       "Add thrum MCP server to .claude/settings.json",
    },
    "codex": {
        Name:             "codex",
        DisplayName:      "OpenAI Codex",
        Command:          "codex",
        MCPSupported:     true,
        HooksSupported:   false,
        InstructionsFile: "AGENTS.md",
        MCPConfigPath:    "Run: codex mcp add thrum 'thrum mcp serve'",
        SetupNotes:       "Use .codex/hooks/session-start for startup",
    },
    "cursor": {
        Name:             "cursor",
        DisplayName:      "Cursor",
        Command:          "cursor-agent",
        MCPSupported:     true,
        HooksSupported:   false,
        InstructionsFile: ".cursorrules",
        MCPConfigPath:    "Settings > Tools & MCP",
        SetupNotes:       "Add MCP server via UI, use startup script",
    },
    "gemini": {
        Name:             "gemini",
        DisplayName:      "Google Gemini Code Assist",
        Command:          "gemini",
        MCPSupported:     true,
        HooksSupported:   false,
        InstructionsFile: "~/.gemini/instructions.md",
        MCPConfigPath:    "~/.gemini/settings.json",
        SetupNotes:       "Global instructions, use profile.sh for startup",
    },
    "auggie": {
        Name:             "auggie",
        DisplayName:      "Augment (Auggie)",
        Command:          "auggie",
        MCPSupported:     false,
        HooksSupported:   false,
        InstructionsFile: "CLAUDE.md",
        MCPConfigPath:    "",
        SetupNotes:       "CLI-only integration (MCP support unknown)",
    },
    "amp": {
        Name:             "amp",
        DisplayName:      "Sourcegraph Amp",
        Command:          "amp",
        MCPSupported:     false,
        HooksSupported:   false,
        InstructionsFile: "CLAUDE.md",
        MCPConfigPath:    "",
        SetupNotes:       "CLI-only integration (MCP support unknown)",
    },
}
```

**Registry Methods:**
```go
func GetPreset(name string) (RuntimePreset, error) {
    if preset, ok := BuiltinPresets[name]; ok {
        return preset, nil
    }
    // Check user config
    userPresets, err := loadUserPresets()
    if err == nil {
        if preset, ok := userPresets[name]; ok {
            return preset, nil
        }
    }
    return RuntimePreset{}, fmt.Errorf("runtime preset %q not found", name)
}

func ListPresets() []RuntimePreset {
    presets := make([]RuntimePreset, 0, len(BuiltinPresets))
    for _, preset := range BuiltinPresets {
        presets = append(presets, preset)
    }
    // Merge user presets
    userPresets, err := loadUserPresets()
    if err == nil {
        for _, preset := range userPresets {
            presets = append(presets, preset)
        }
    }
    return presets
}
```

#### 2.2 User Config

**Config File:** `~/.config/thrum/runtimes.json`

**Schema:**
```json
{
  "default_runtime": "claude",
  "custom_runtimes": {
    "claude-sonnet": {
      "name": "claude-sonnet",
      "display_name": "Claude Sonnet 4.5",
      "command": "claude --model sonnet-4.5",
      "mcp_supported": true,
      "hooks_supported": true,
      "instructions_file": "CLAUDE.md",
      "mcp_config_path": ".claude/settings.json"
    },
    "custom-agent": {
      "name": "custom-agent",
      "display_name": "My Custom Agent",
      "command": "my-agent",
      "mcp_supported": false,
      "hooks_supported": false,
      "instructions_file": "CLAUDE.md"
    }
  }
}
```

**Resolution Hierarchy:**
1. Command-line `--runtime` flag
2. User config `default_runtime`
3. Auto-detection (`DetectRuntime()`)
4. Built-in default (`"cli-only"`)

#### 2.3 CLI Commands

**Implementation:** `internal/cli/runtime.go`

**Commands:**
```bash
# List all runtimes (built-in + custom)
thrum runtime list [--json]

# Show runtime details
thrum runtime show claude [--json]

# Add custom runtime
thrum runtime add myruntime \
  --command my-agent \
  --mcp-supported \
  --instructions-file AGENTS.md

# Set default runtime
thrum runtime set-default gemini

# Remove custom runtime
thrum runtime remove myruntime
```

**Output Examples:**

**`thrum runtime list`:**
```
Built-in Runtimes:
  claude   Claude Code (MCP ✓, Hooks ✓)
  codex    OpenAI Codex (MCP ✓, Hooks ✗)
  cursor   Cursor (MCP ✓, Hooks ✗)
  gemini   Google Gemini Code Assist (MCP ✓, Hooks ✗)
  auggie   Augment (CLI-only)
  amp      Sourcegraph Amp (CLI-only)

Custom Runtimes:
  claude-sonnet   Claude Sonnet 4.5 (MCP ✓, Hooks ✓)

Default: claude
```

**`thrum runtime show claude --json`:**
```json
{
  "name": "claude",
  "display_name": "Claude Code",
  "command": "claude",
  "mcp_supported": true,
  "hooks_supported": true,
  "instructions_file": "CLAUDE.md",
  "mcp_config_path": ".claude/settings.json",
  "setup_notes": "Add thrum MCP server to .claude/settings.json"
}
```

#### 2.4 Enhanced Quickstart

**Modified:** `internal/cli/quickstart.go`

**New Behavior:**
```bash
# Auto-detect runtime and generate configs
thrum quickstart --name alice --role reviewer --module ui

# Explicit runtime (overrides detection)
thrum quickstart --name bob --role implementer --module backend --runtime codex

# Show detected runtime without executing
thrum quickstart --name charlie --role tester --module api --dry-run
# Output: "Detected runtime: cursor. Would create .cursorrules and scripts/thrum-startup.sh"

# Skip config generation (just register agent)
thrum quickstart --name dave --role planner --module design --no-init
```

**New Flags:**
- `--runtime <name>` - Explicitly specify runtime (overrides detection)
- `--dry-run` - Preview changes without writing files
- `--no-init` - Skip config file generation (only register agent)
- `--force` - Overwrite existing files without prompting

**Flow:**
1. Detect runtime via `DetectRuntime()` or use `--runtime` flag
2. Look up preset in registry
3. Check if runtime configs already exist
4. Generate runtime-specific config files (MCP, hooks, instructions)
5. Register agent in daemon (same as current `quickstart`)
6. Output confirmation with next steps

**Output:**
```
✓ Detected runtime: claude
✓ Generated .claude/settings.json
✓ Generated scripts/thrum-startup.sh
✓ Registered agent "alice" (role: reviewer, module: ui)

Next steps:
1. Restart Claude Code to load new MCP server
2. Run: thrum inbox --unread
3. Send a message: thrum send "Hello team" --broadcast

Documentation: docs/runtimes/claude.md
```

#### 2.5 Context Prime Command

**Implementation:** `internal/cli/prime.go`

**Purpose:** Single command to restore full agent context after session restart

**Command:**
```bash
thrum context prime [--json]
```

**Output:**
```json
{
  "identity": {
    "agent_id": "impl_auth",
    "name": "alice",
    "role": "implementer",
    "module": "auth",
    "display": "Alice (Auth)"
  },
  "session": {
    "session_id": "sess_abc123",
    "started_at": "2026-02-11T10:00:00Z",
    "intent": "Implementing JWT authentication",
    "uptime_seconds": 3600
  },
  "agents": {
    "total": 5,
    "active": 3,
    "list": [
      {
        "agent_id": "coordinator",
        "role": "coordinator",
        "status": "active",
        "last_seen": "2s ago"
      },
      {
        "agent_id": "reviewer",
        "role": "reviewer",
        "status": "active",
        "last_seen": "30s ago"
      }
    ]
  },
  "messages": {
    "unread": 2,
    "total": 15,
    "recent": [
      {
        "message_id": "msg_xyz",
        "from": "coordinator",
        "content": "Please review auth PR #42",
        "priority": "high",
        "timestamp": "2026-02-11T10:30:00Z"
      }
    ]
  },
  "work_context": {
    "branch": "feature/jwt-auth",
    "uncommitted_files": ["src/auth/jwt.ts"],
    "unmerged_commits": 3
  }
}
```

**Implementation Strategy:**
```go
func primeContext(cli *Client) (*PrimeContext, error) {
    ctx := &PrimeContext{}

    // 1. Get identity
    identity, err := cli.Agent.Whoami()
    if err != nil {
        return nil, err
    }
    ctx.Identity = identity

    // 2. Get session info
    session, err := cli.Session.Current()
    if err != nil {
        return nil, err
    }
    ctx.Session = session

    // 3. List agents
    agents, err := cli.Agent.List(&ListOptions{Context: true})
    if err != nil {
        return nil, err
    }
    ctx.Agents = agents

    // 4. Get unread messages
    messages, err := cli.Message.List(&ListOptions{Unread: true, Limit: 10})
    if err != nil {
        return nil, err
    }
    ctx.Messages = messages

    // 5. Get git work context
    workCtx, err := getWorkContext()
    if err != nil {
        return nil, err
    }
    ctx.WorkContext = workCtx

    return ctx, nil
}
```

**Use Cases:**
- Agent compacts context, loses state → runs `thrum context prime` to recover
- Session crashes → new session runs `thrum context prime` to resume
- Manual debugging → user runs `thrum context prime --json` to inspect state
- Background listener → calls `thrum context prime` on startup to check for messages

## Implementation Details

### File Paths

**New Files:**
- `internal/cli/init.go` - Main init command implementation
- `internal/cli/init_test.go` - Tests for init command
- `internal/cli/runtime.go` - Runtime management commands
- `internal/cli/runtime_test.go` - Tests for runtime commands
- `internal/cli/prime.go` - Context prime command
- `internal/cli/prime_test.go` - Tests for prime command
- `internal/runtime/presets.go` - Preset definitions and registry
- `internal/runtime/presets_test.go` - Tests for preset registry
- `internal/runtime/detect.go` - Runtime detection logic
- `internal/runtime/detect_test.go` - Tests for detection
- `internal/cli/templates/*.tmpl` - Embedded template files

**Modified Files:**
- `internal/cli/quickstart.go` - Add `--runtime`, `--dry-run`, `--no-init` flags
- `cmd/thrum/main.go` - Register new commands

**Generated Files (by `thrum init`):**
- `scripts/thrum-startup.sh` - Universal startup script
- `.claude/settings.json` - Claude MCP config (if `--runtime claude`)
- `.codex/hooks/session-start` - Codex startup hook (if `--runtime codex`)
- `.cursorrules` - Cursor instructions (if `--runtime cursor`)
- `AGENTS.md` - Codex instructions file (if `--runtime codex`)

### Function Signatures

**Init Command:**
```go
func runInit(cmd *cobra.Command, args []string) error {
    runtime, _ := cmd.Flags().GetString("runtime")
    dryRun, _ := cmd.Flags().GetBool("dry-run")
    force, _ := cmd.Flags().GetBool("force")

    // Auto-detect if not specified
    if runtime == "" {
        runtime = DetectRuntime(".")
    }

    // Get preset
    preset, err := GetPreset(runtime)
    if err != nil {
        return err
    }

    // Generate configs
    if err := generateRuntimeConfigs(preset, dryRun, force); err != nil {
        return err
    }

    return nil
}
```

**Runtime Commands:**
```go
func runRuntimeList(cmd *cobra.Command, args []string) error {
    presets := ListPresets()

    outputJSON, _ := cmd.Flags().GetBool("json")
    if outputJSON {
        return jsonOutput(presets)
    }

    // Human-readable table output
    fmt.Println("Built-in Runtimes:")
    for _, preset := range presets {
        fmt.Printf("  %-12s %s\n", preset.Name, preset.DisplayName)
    }

    return nil
}

func runRuntimeShow(cmd *cobra.Command, args []string) error {
    if len(args) < 1 {
        return fmt.Errorf("usage: thrum runtime show <name>")
    }

    preset, err := GetPreset(args[0])
    if err != nil {
        return err
    }

    outputJSON, _ := cmd.Flags().GetBool("json")
    if outputJSON {
        return jsonOutput(preset)
    }

    // Human-readable output
    fmt.Printf("Name: %s\n", preset.Name)
    fmt.Printf("Display Name: %s\n", preset.DisplayName)
    fmt.Printf("Command: %s\n", preset.Command)
    fmt.Printf("MCP Supported: %v\n", preset.MCPSupported)
    // ... etc

    return nil
}
```

**Context Prime:**
```go
type PrimeContext struct {
    Identity    AgentIdentity       `json:"identity"`
    Session     SessionInfo         `json:"session"`
    Agents      AgentListResult     `json:"agents"`
    Messages    MessageListResult   `json:"messages"`
    WorkContext WorkContext         `json:"work_context"`
}

func runContextPrime(cmd *cobra.Command, args []string) error {
    cli, err := NewClient()
    if err != nil {
        return err
    }

    ctx, err := primeContext(cli)
    if err != nil {
        return err
    }

    outputJSON, _ := cmd.Flags().GetBool("json")
    if outputJSON {
        return jsonOutput(ctx)
    }

    // Human-readable summary
    fmt.Printf("Agent: %s (%s, %s)\n", ctx.Identity.Name, ctx.Identity.Role, ctx.Identity.Module)
    fmt.Printf("Session: %s (%s uptime)\n", ctx.Session.SessionID, formatDuration(ctx.Session.UptimeSeconds))
    fmt.Printf("Agents: %d active, %d total\n", ctx.Agents.Active, ctx.Agents.Total)
    fmt.Printf("Messages: %d unread\n", ctx.Messages.Unread)

    return nil
}
```

## Dependencies

**Epic 1 is independent:**
- No blockers
- Can be implemented in parallel with other work
- Template files can be added incrementally

**Epic 2 depends on Epic 1:**
- Uses runtime detection from init command
- Preset registry references templates from Epic 1
- Enhanced quickstart calls template generation

**External Dependencies:**
- Go stdlib `embed` package (already used)
- Go stdlib `text/template` package (already used)
- No new third-party dependencies

## Testing Strategy

### Unit Tests

**Detection Logic (`internal/runtime/detect_test.go`):**
```go
func TestDetectRuntime(t *testing.T) {
    tests := []struct {
        name     string
        setup    func(tmpDir string)
        env      map[string]string
        expected string
    }{
        {
            name: "Claude via file marker",
            setup: func(dir string) {
                os.MkdirAll(filepath.Join(dir, ".claude"), 0755)
                os.WriteFile(filepath.Join(dir, ".claude/settings.json"), []byte("{}"), 0644)
            },
            expected: "claude",
        },
        {
            name: "Codex via directory",
            setup: func(dir string) {
                os.MkdirAll(filepath.Join(dir, ".codex"), 0755)
            },
            expected: "codex",
        },
        {
            name: "Claude via env var",
            env: map[string]string{
                "CLAUDE_SESSION_ID": "test_session",
            },
            expected: "claude",
        },
        {
            name:     "CLI-only fallback",
            setup:    func(dir string) {},
            expected: "cli-only",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            tmpDir := t.TempDir()
            if tt.setup != nil {
                tt.setup(tmpDir)
            }

            for k, v := range tt.env {
                os.Setenv(k, v)
                defer os.Unsetenv(k)
            }

            result := DetectRuntime(tmpDir)
            if result != tt.expected {
                t.Errorf("expected %q, got %q", tt.expected, result)
            }
        })
    }
}
```

**Template Rendering (`internal/cli/init_test.go`):**
```go
func TestRenderTemplate(t *testing.T) {
    data := TemplateData{
        AgentName:   "test_agent",
        AgentRole:   "implementer",
        AgentModule: "auth",
        MCPCommand:  "thrum mcp serve",
    }

    tests := []struct {
        runtime  string
        template string
        contains []string
    }{
        {
            runtime:  "claude",
            template: "settings.json.tmpl",
            contains: []string{
                `"thrum"`,
                `"thrum mcp serve"`,
                "test_agent",
            },
        },
        {
            runtime:  "codex",
            template: "session-start.sh.tmpl",
            contains: []string{
                "THRUM_NAME=test_agent",
                "THRUM_ROLE=implementer",
                "THRUM_MODULE=auth",
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.runtime+"/"+tt.template, func(t *testing.T) {
            result, err := RenderTemplate(tt.runtime, tt.template, data)
            if err != nil {
                t.Fatal(err)
            }

            for _, substr := range tt.contains {
                if !strings.Contains(result, substr) {
                    t.Errorf("expected output to contain %q", substr)
                }
            }
        })
    }
}
```

**Preset Registry (`internal/runtime/presets_test.go`):**
```go
func TestGetPreset(t *testing.T) {
    tests := []struct {
        name      string
        expectErr bool
    }{
        {name: "claude", expectErr: false},
        {name: "codex", expectErr: false},
        {name: "nonexistent", expectErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            preset, err := GetPreset(tt.name)
            if tt.expectErr {
                if err == nil {
                    t.Error("expected error, got nil")
                }
            } else {
                if err != nil {
                    t.Errorf("unexpected error: %v", err)
                }
                if preset.Name != tt.name {
                    t.Errorf("expected name %q, got %q", tt.name, preset.Name)
                }
            }
        })
    }
}

func TestListPresets(t *testing.T) {
    presets := ListPresets()

    if len(presets) < 6 {
        t.Errorf("expected at least 6 built-in presets, got %d", len(presets))
    }

    // Verify required presets exist
    required := []string{"claude", "codex", "cursor", "gemini", "auggie", "amp"}
    found := make(map[string]bool)
    for _, p := range presets {
        found[p.Name] = true
    }

    for _, name := range required {
        if !found[name] {
            t.Errorf("missing required preset: %s", name)
        }
    }
}
```

### Integration Tests

**Init Command (`tests/integration/init_test.go`):**
```bash
#!/bin/bash
# Integration test for thrum init

set -e

TMPDIR=$(mktemp -d)
cd "$TMPDIR"

# Test auto-detection
mkdir -p .claude
echo '{}' > .claude/settings.json
thrum init --dry-run | grep "Detected runtime: claude"

# Test explicit runtime
thrum init --runtime codex --force
[ -f .codex/hooks/session-start ]
[ -f AGENTS.md ]
[ -f scripts/thrum-startup.sh ]

# Test all runtimes
thrum init --runtime all --force
[ -f .claude/settings.json ]
[ -f .codex/hooks/session-start ]
[ -f .cursorrules ]

# Cleanup
rm -rf "$TMPDIR"

echo "✓ All init tests passed"
```

**Quickstart with Runtime (`tests/integration/quickstart_runtime_test.go`):**
```bash
#!/bin/bash
# Integration test for enhanced quickstart

set -e

TMPDIR=$(mktemp -d)
cd "$TMPDIR"

# Create .cursorrules to trigger detection
echo "test" > .cursorrules

# Ensure daemon running
thrum daemon start || true

# Test quickstart with runtime detection
OUTPUT=$(thrum quickstart --name test_agent --role implementer --module test --dry-run)
echo "$OUTPUT" | grep "Detected runtime: cursor"

# Test explicit runtime override
OUTPUT=$(thrum quickstart --name test_agent --role implementer --module test --runtime claude --dry-run)
echo "$OUTPUT" | grep "Using runtime: claude"

# Cleanup
rm -rf "$TMPDIR"

echo "✓ All quickstart tests passed"
```

**Context Prime (`tests/integration/prime_test.go`):**
```bash
#!/bin/bash
# Integration test for context prime

set -e

# Ensure daemon running
thrum daemon start || true

# Register test agent
thrum quickstart --name prime_test --role implementer --module test

# Send test message
thrum send "Test message" --to @prime_test

# Test context prime
OUTPUT=$(thrum context prime --json)
echo "$OUTPUT" | jq -e '.identity.name == "prime_test"'
echo "$OUTPUT" | jq -e '.messages.unread >= 1'

echo "✓ Context prime test passed"
```

## Out of Scope

The following are **not** part of this design and are tracked as separate work items:

**P2 Features (Future Epics):**
- Checkpoint system (`.thrum/checkpoint.json` save/load/clear)
- Per-runtime integration guides (docs, not code)
- Message listener alternatives (polling, WebSocket examples)

**P3 Features (Nice-to-Haves):**
- Dashboard/TUI for multi-agent monitoring
- Convoy system for coordinated multi-agent work
- Formula system for repeatable workflows
- HTTP API for remote agents

**Documentation:**
- `docs/runtimes/claude.md` - Claude integration guide (enhancement)
- `docs/runtimes/codex.md` - Codex integration guide (new)
- `docs/runtimes/cursor.md` - Cursor integration guide (new)
- `docs/runtimes/gemini.md` - Gemini integration guide (new)
- `docs/runtimes/cli-only.md` - CLI-only integration guide (new)

These will be addressed in follow-up design specs.

## Success Criteria

**Epic 1 (P0) is successful when:**
- ✅ `thrum init` command detects runtime from environment
- ✅ `thrum init --runtime <name>` generates correct config files for 6 runtimes
- ✅ All CLI commands verified to support `--json` flag
- ✅ Universal startup script (`scripts/thrum-startup.sh`) works from any runtime
- ✅ Integration tests pass for config generation

**Epic 2 (P1) is successful when:**
- ✅ Runtime preset registry contains 6 built-in presets
- ✅ User can add custom runtimes via `~/.config/thrum/runtimes.json`
- ✅ `thrum runtime list/show` commands work with `--json` output
- ✅ `thrum quickstart` auto-detects runtime and generates configs
- ✅ `thrum context prime` outputs full agent context as JSON
- ✅ Integration tests pass for enhanced quickstart

**Overall Success:**
- ✅ Any user can run `thrum init` and get working config for their runtime
- ✅ Thrum is usable from Codex, Cursor, Gemini without manual config
- ✅ CLI-only mode works for any runtime with bash execution
- ✅ Documentation clearly states "Works with any AI runtime"

## Timeline Estimate

| Epic | Tasks | Effort (hours) | Days @ 8hr |
|------|-------|----------------|------------|
| **Epic 1 (P0)** | Runtime detection, template engine, init command, JSON audit | 7-12 | 1-1.5 |
| **Epic 2 (P1)** | Preset registry, runtime commands, enhanced quickstart, context prime | 9-13 | 1-2 |
| **Total (P0+P1)** | Core multi-runtime support | **16-25** | **2-3** |

**Realistic Timeline:** 3-4 days of focused work, or 1-2 weeks at 50% allocation.

**Dependencies:** None (can start immediately)

## References

**Related Documents:**
- [Consolidated Report](/Users/leon/dev/opensource/thrum/dev-docs/agent_integration/consolidated_report.md) - Full analysis of runtime landscape
- [Gastown Runtime Patterns](/Users/leon/.ref/gastown/) - Reference implementation

**MCP Protocol:**
- [Anthropic MCP Announcement](https://www.anthropic.com/news/model-context-protocol)
- [MCP as Universal Standard](https://www.unite.ai/what-are-mcp-apps-the-new-standard-turning-ai-responses-into-interactive-interfaces/)
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)

**Thrum Codebase:**
- Existing identity system: `internal/identity/`, `internal/config/`
- Existing CLI commands: `internal/cli/`, `cmd/thrum/`
- Existing MCP server: `internal/mcp/`
