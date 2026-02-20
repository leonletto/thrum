# Init, Identity & Discovery Consistency Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to
> implement this plan task-by-task.

**Goal:** Make thrum init the single entry point for setup, enrich identity
files with all available data, and ensure all discovery commands show consistent
output in both JSON and human modes.

**Architecture:** Introduce a canonical `AgentSummary` struct used by all
discovery commands. Enrich `IdentityFile` to v3 with branch, intent, session.
Refactor `thrum init` to do full setup (create, prompt, daemon, register,
session, intent). All Format functions and JSON output derive from the same
struct.

**Tech Stack:** Go, Cobra CLI, JSON-RPC daemon

---

### Task 1: Add default intents and git helpers

Create `internal/cli/defaults.go` with the default intent map and git helper
functions that will be used by init, quickstart, and identity enrichment.

**Files:**
- Create: `internal/cli/defaults.go`
- Create: `internal/cli/defaults_test.go`

**Step 1: Write the failing test**

```go
// internal/cli/defaults_test.go
package cli

import "testing"

func TestDefaultIntent(t *testing.T) {
	tests := []struct {
		role     string
		repo     string
		expected string
	}{
		{"coordinator", "thrum", "Coordinate agents and tasks in thrum"},
		{"implementer", "thrum", "Implement features and fixes in thrum"},
		{"reviewer", "myapp", "Review code and PRs in myapp"},
		{"planner", "thrum", "Plan architecture and design in thrum"},
		{"tester", "thrum", "Test and validate changes in thrum"},
		{"unknown_role", "thrum", "Working in thrum"},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			got := DefaultIntent(tt.role, tt.repo)
			if got != tt.expected {
				t.Errorf("DefaultIntent(%q, %q) = %q, want %q", tt.role, tt.repo, got, tt.expected)
			}
		})
	}
}

func TestAutoDisplay(t *testing.T) {
	tests := []struct {
		role, module, expected string
	}{
		{"coordinator", "main", "Coordinator (main)"},
		{"implementer", "auth", "Implementer (auth)"},
	}
	for _, tt := range tests {
		t.Run(tt.role+"_"+tt.module, func(t *testing.T) {
			got := AutoDisplay(tt.role, tt.module)
			if got != tt.expected {
				t.Errorf("AutoDisplay(%q, %q) = %q, want %q", tt.role, tt.module, got, tt.expected)
			}
		})
	}
}

func TestGetRepoName(t *testing.T) {
	// Test fallback when not in a git repo
	got := GetRepoName("/nonexistent/path")
	if got != "unknown" {
		// Fallback should be the directory basename or "unknown"
		t.Logf("GetRepoName for non-repo: %q", got)
	}
}

func TestGetCurrentBranch(t *testing.T) {
	// Test fallback when not in a git repo
	got := GetCurrentBranch("/nonexistent/path")
	if got != "main" {
		t.Errorf("GetCurrentBranch fallback = %q, want %q", got, "main")
	}
}

func TestGetRepoID(t *testing.T) {
	// Test fallback when no remote
	got := GetRepoID("/nonexistent/path")
	if got != "" {
		t.Logf("GetRepoID for non-repo: %q", got)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestDefaultIntent|TestAutoDisplay|TestGetRepoName|TestGetCurrentBranch|TestGetRepoID' -v`
Expected: FAIL — functions not defined

**Step 3: Write minimal implementation**

```go
// internal/cli/defaults.go
package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/leonletto/thrum/internal/identity"
)

// defaultIntents maps roles to their default intent templates.
// {repo} is replaced with the repository name.
var defaultIntents = map[string]string{
	"coordinator": "Coordinate agents and tasks in {repo}",
	"implementer": "Implement features and fixes in {repo}",
	"reviewer":    "Review code and PRs in {repo}",
	"planner":     "Plan architecture and design in {repo}",
	"tester":      "Test and validate changes in {repo}",
}

// DefaultIntent returns the default intent for a role and repo name.
func DefaultIntent(role, repoName string) string {
	tmpl, ok := defaultIntents[role]
	if !ok {
		tmpl = "Working in {repo}"
	}
	return strings.ReplaceAll(tmpl, "{repo}", repoName)
}

// AutoDisplay generates a display name from role and module.
// e.g., "coordinator", "main" -> "Coordinator (main)"
func AutoDisplay(role, module string) string {
	if role == "" {
		return ""
	}
	title := strings.ToUpper(role[:1]) + role[1:]
	if module != "" {
		return fmt.Sprintf("%s (%s)", title, module)
	}
	return title
}

// GetRepoName returns the repository name from git toplevel basename.
// Falls back to "unknown" if not in a git repo.
func GetRepoName(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	topLevel := strings.TrimSpace(string(out))
	parts := strings.Split(topLevel, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "unknown"
}

// GetCurrentBranch returns the current git branch.
// Falls back to "main" if not in a git repo.
func GetCurrentBranch(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "branch", "--show-current")
	out, err := cmd.Output()
	if err != nil {
		return "main"
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		return "main"
	}
	return branch
}

// GetRepoID returns the repo ID from git remote origin URL.
// Returns empty string if no remote or not a git repo.
func GetRepoID(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "remote", "get-url", "origin")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	originURL := strings.TrimSpace(string(out))
	if originURL == "" {
		return ""
	}
	repoID, err := identity.GenerateRepoID(originURL)
	if err != nil {
		return ""
	}
	return repoID
}

// GetWorktreeName returns the basename of the git worktree root.
// Falls back to the basename of repoPath.
func GetWorktreeName(repoPath string) string {
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		parts := strings.Split(repoPath, "/")
		if len(parts) > 0 {
			return parts[len(parts)-1]
		}
		return ""
	}
	topLevel := strings.TrimSpace(string(out))
	parts := strings.Split(topLevel, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ""
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestDefaultIntent|TestAutoDisplay|TestGetRepoName|TestGetCurrentBranch|TestGetRepoID' -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/cli/defaults.go internal/cli/defaults_test.go
git commit -m "feat: add default intents and git auto-population helpers"
```

---

### Task 2: Enrich IdentityFile struct to v3

Add `Branch`, `Intent`, and `SessionID` fields to `IdentityFile`. Bump version
to 3 on write. Existing v1/v2 files continue to load (missing fields default to
zero values).

**Files:**
- Modify: `internal/config/config.go:32-41` (IdentityFile struct)
- Modify: `internal/config/config_test.go` (add v3 tests)

**Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestIdentityFileV3Fields(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750)

	identity := &IdentityFile{
		Version:   3,
		RepoID:    "r_TEST123456",
		Agent:     AgentConfig{Kind: "agent", Name: "coordinator", Role: "coordinator", Module: "main", Display: "Coordinator (main)"},
		Worktree:  "thrum",
		Branch:    "main",
		Intent:    "Coordinate agents and tasks in thrum",
		SessionID: "ses_01ABC",
	}

	if err := SaveIdentityFile(thrumDir, identity); err != nil {
		t.Fatalf("SaveIdentityFile: %v", err)
	}

	loaded, _, err := LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadIdentityWithPath: %v", err)
	}

	if loaded.Version != 3 {
		t.Errorf("Version = %d, want 3", loaded.Version)
	}
	if loaded.Branch != "main" {
		t.Errorf("Branch = %q, want %q", loaded.Branch, "main")
	}
	if loaded.Intent != "Coordinate agents and tasks in thrum" {
		t.Errorf("Intent = %q, want correct default", loaded.Intent)
	}
	if loaded.SessionID != "ses_01ABC" {
		t.Errorf("SessionID = %q, want %q", loaded.SessionID, "ses_01ABC")
	}
	if loaded.Agent.Display != "Coordinator (main)" {
		t.Errorf("Display = %q, want %q", loaded.Agent.Display, "Coordinator (main)")
	}
}

func TestIdentityFileV1Compat(t *testing.T) {
	// Verify v1 file (missing new fields) loads without error
	tmpDir := t.TempDir()
	identitiesDir := filepath.Join(tmpDir, ".thrum", "identities")
	os.MkdirAll(identitiesDir, 0750)

	v1Data := `{"version":1,"repo_id":"","agent":{"Kind":"agent","Name":"old_agent","Role":"implementer","Module":"main","Display":""},"worktree":"thrum","confirmed_by":"","updated_at":"2026-01-01T00:00:00Z"}`
	os.WriteFile(filepath.Join(identitiesDir, "old_agent.json"), []byte(v1Data), 0600)

	loaded, _, err := LoadIdentityWithPath(tmpDir)
	if err != nil {
		t.Fatalf("LoadIdentityWithPath v1 file: %v", err)
	}
	if loaded.Branch != "" {
		t.Errorf("v1 file Branch should be empty, got %q", loaded.Branch)
	}
	if loaded.Intent != "" {
		t.Errorf("v1 file Intent should be empty, got %q", loaded.Intent)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/config/ -run 'TestIdentityFileV3|TestIdentityFileV1Compat' -v`
Expected: FAIL — `Branch`, `Intent`, `SessionID` fields don't exist

**Step 3: Update the IdentityFile struct**

In `internal/config/config.go:32-41`, replace:

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

With:

```go
type IdentityFile struct {
	Version     int         `json:"version"`
	RepoID      string      `json:"repo_id"`
	Agent       AgentConfig `json:"agent"`
	Worktree    string      `json:"worktree"`
	Branch      string      `json:"branch,omitempty"`
	Intent      string      `json:"intent,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	ConfirmedBy string      `json:"confirmed_by,omitempty"`
	ContextFile string      `json:"context_file,omitempty"`
	UpdatedAt   time.Time   `json:"updated_at"`
}
```

Also update `SaveIdentityFile` to set `Version = 3` on write if it's less
than 3:

In `internal/config/config.go` inside `SaveIdentityFile`, before marshalling:

```go
if identity.Version < 3 {
    identity.Version = 3
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/config/ -run 'TestIdentityFileV3|TestIdentityFileV1Compat' -v`
Expected: PASS

**Step 5: Run full config test suite**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/config/ -v`
Expected: PASS (no regressions)

**Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: enrich IdentityFile to v3 with branch, intent, session_id"
```

---

### Task 3: Create AgentSummary canonical output struct

Add `AgentSummary`, `BuildAgentSummary`, `FormatAgentSummary`, and
`FormatAgentSummaryCompact` to a new file.

**Files:**
- Create: `internal/cli/agent_summary.go`
- Create: `internal/cli/agent_summary_test.go`

**Step 1: Write the failing test**

```go
// internal/cli/agent_summary_test.go
package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestBuildAgentSummary_FromIdentityFile(t *testing.T) {
	idFile := &config.IdentityFile{
		Version:   3,
		RepoID:    "r_TEST123456",
		Agent:     config.AgentConfig{Name: "coordinator", Role: "coordinator", Module: "main", Display: "Coordinator (main)"},
		Worktree:  "thrum",
		Branch:    "main",
		Intent:    "Coordinate agents and tasks in thrum",
		SessionID: "ses_01ABC",
	}

	summary := BuildAgentSummary(idFile, ".thrum/identities/coordinator.json", nil)

	if summary.AgentID != "coordinator" {
		t.Errorf("AgentID = %q, want %q", summary.AgentID, "coordinator")
	}
	if summary.Role != "coordinator" {
		t.Errorf("Role = %q", summary.Role)
	}
	if summary.Branch != "main" {
		t.Errorf("Branch = %q", summary.Branch)
	}
	if summary.Intent != "Coordinate agents and tasks in thrum" {
		t.Errorf("Intent = %q", summary.Intent)
	}
	if summary.IdentityFile != ".thrum/identities/coordinator.json" {
		t.Errorf("IdentityFile = %q", summary.IdentityFile)
	}
}

func TestBuildAgentSummary_DaemonEnrichment(t *testing.T) {
	idFile := &config.IdentityFile{
		Version:  3,
		Agent:    config.AgentConfig{Name: "coordinator", Role: "coordinator", Module: "main"},
		Worktree: "thrum",
		Branch:   "main",
	}
	daemonInfo := &WhoamiResult{
		AgentID:      "coordinator",
		SessionID:    "ses_LIVE",
		SessionStart: "2026-02-19T12:00:00Z",
	}

	summary := BuildAgentSummary(idFile, ".thrum/identities/coordinator.json", daemonInfo)

	// Daemon session should override file session
	if summary.SessionID != "ses_LIVE" {
		t.Errorf("SessionID = %q, want daemon value %q", summary.SessionID, "ses_LIVE")
	}
	if summary.SessionStart != "2026-02-19T12:00:00Z" {
		t.Errorf("SessionStart = %q", summary.SessionStart)
	}
}

func TestFormatAgentSummary(t *testing.T) {
	summary := &AgentSummary{
		AgentID:      "coordinator",
		Role:         "coordinator",
		Module:       "main",
		Display:      "Coordinator (main)",
		Branch:       "main",
		Intent:       "Coordinate agents and tasks in thrum",
		SessionID:    "ses_01ABC",
		SessionStart: "2026-02-19T12:00:00Z",
		Worktree:     "thrum",
		IdentityFile: ".thrum/identities/coordinator.json",
	}

	output := FormatAgentSummary(summary)

	for _, field := range []string{
		"Agent ID:",
		"coordinator",
		"Role:",
		"Module:",
		"main",
		"Branch:",
		"Intent:",
		"Session:",
		"ses_01ABC",
		"Worktree:",
		"thrum",
		"Identity:",
	} {
		if !strings.Contains(output, field) {
			t.Errorf("Output missing %q:\n%s", field, output)
		}
	}
}

func TestFormatAgentSummaryCompact(t *testing.T) {
	summary := &AgentSummary{
		AgentID: "coordinator",
		Role:    "coordinator",
		Module:  "main",
		Branch:  "main",
		Intent:  "Coordinate agents and tasks in thrum",
		Status:  "active",
	}

	output := FormatAgentSummaryCompact(summary)

	if !strings.Contains(output, "@coordinator") {
		t.Errorf("Compact output missing @coordinator: %q", output)
	}
	if !strings.Contains(output, "main") {
		t.Errorf("Compact output missing branch: %q", output)
	}
}

func TestAgentSummary_JSONParity(t *testing.T) {
	summary := &AgentSummary{
		AgentID:      "coordinator",
		Role:         "coordinator",
		Module:       "main",
		Display:      "Coordinator (main)",
		Branch:       "main",
		Intent:       "Coordinate agents and tasks in thrum",
		Worktree:     "thrum",
		SessionID:    "ses_01ABC",
		IdentityFile: ".thrum/identities/coordinator.json",
	}

	// JSON output
	jsonBytes, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	jsonStr := string(jsonBytes)

	// Human output
	humanStr := FormatAgentSummary(summary)

	// Every non-empty field in JSON should have corresponding data in human output
	for _, field := range []string{"coordinator", "main", "thrum", "ses_01ABC"} {
		if !strings.Contains(jsonStr, field) {
			t.Errorf("JSON missing %q", field)
		}
		if !strings.Contains(humanStr, field) {
			t.Errorf("Human missing %q", field)
		}
	}
}
```

**Step 2: Run test to verify it fails**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestBuildAgentSummary|TestFormatAgentSummary|TestAgentSummary_JSON' -v`
Expected: FAIL — types not defined

**Step 3: Write the implementation**

```go
// internal/cli/agent_summary.go
package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/config"
)

// AgentSummary is the canonical representation of an agent's identity and state.
// Used by whoami, team, agent list, status, overview.
// JSON mode marshals this directly; human mode uses FormatAgentSummary.
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

// BuildAgentSummary constructs an AgentSummary from an identity file and
// optional daemon info. Identity file is the base; daemon enriches with
// live session data.
func BuildAgentSummary(idFile *config.IdentityFile, idPath string, daemonInfo *WhoamiResult) *AgentSummary {
	s := &AgentSummary{
		AgentID:      idFile.Agent.Name,
		Role:         idFile.Agent.Role,
		Module:       idFile.Agent.Module,
		Display:      idFile.Agent.Display,
		Branch:       idFile.Branch,
		Worktree:     idFile.Worktree,
		Intent:       idFile.Intent,
		RepoID:       idFile.RepoID,
		SessionID:    idFile.SessionID,
		IdentityFile: idPath,
		Source:       "file",
	}

	if !idFile.UpdatedAt.IsZero() {
		s.UpdatedAt = idFile.UpdatedAt.Format(time.RFC3339)
	}

	// Enrich from daemon if available
	if daemonInfo != nil {
		s.Source = "daemon"
		if daemonInfo.SessionID != "" {
			s.SessionID = daemonInfo.SessionID
		}
		if daemonInfo.SessionStart != "" {
			s.SessionStart = daemonInfo.SessionStart
		}
		if daemonInfo.Display != "" {
			s.Display = daemonInfo.Display
		}
	}

	return s
}

// FormatAgentSummary formats an AgentSummary for multi-line human-readable
// display. Used by whoami, status, overview for the "self" section.
func FormatAgentSummary(s *AgentSummary) string {
	var out strings.Builder

	out.WriteString(fmt.Sprintf("Agent ID:  %s\n", s.AgentID))
	out.WriteString(fmt.Sprintf("Role:      @%s\n", s.Role))

	if s.Module != "" {
		out.WriteString(fmt.Sprintf("Module:    %s\n", s.Module))
	}
	if s.Display != "" {
		out.WriteString(fmt.Sprintf("Display:   %s\n", s.Display))
	}
	if s.Branch != "" {
		out.WriteString(fmt.Sprintf("Branch:    %s\n", s.Branch))
	}
	if s.Intent != "" {
		out.WriteString(fmt.Sprintf("Intent:    %s\n", s.Intent))
	}

	if s.SessionID != "" {
		sessionAge := ""
		if s.SessionStart != "" {
			if t, err := time.Parse(time.RFC3339, s.SessionStart); err == nil {
				sessionAge = fmt.Sprintf(" (%s ago)", formatDuration(time.Since(t)))
			}
		}
		out.WriteString(fmt.Sprintf("Session:   %s%s\n", s.SessionID, sessionAge))
	} else {
		out.WriteString("Session:   none (use 'thrum session start' to begin)\n")
	}

	if s.Worktree != "" {
		out.WriteString(fmt.Sprintf("Worktree:  %s\n", s.Worktree))
	}
	if s.IdentityFile != "" {
		out.WriteString(fmt.Sprintf("Identity:  %s\n", s.IdentityFile))
	}

	return out.String()
}

// FormatAgentSummaryCompact formats an AgentSummary as a single-line summary.
// Used in team and agent list contexts.
// Format: "● @name (module) — intent [branch]"
func FormatAgentSummaryCompact(s *AgentSummary) string {
	icon := "○"
	if s.Status == "active" {
		icon = "●"
	}

	parts := []string{fmt.Sprintf("%s @%s (%s)", icon, s.AgentID, s.Module)}

	if s.Intent != "" {
		parts = append(parts, fmt.Sprintf("— %s", s.Intent))
	}
	if s.Branch != "" {
		parts = append(parts, fmt.Sprintf("[%s]", s.Branch))
	}

	return strings.Join(parts, " ")
}
```

**Step 4: Run test to verify it passes**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestBuildAgentSummary|TestFormatAgentSummary|TestAgentSummary_JSON' -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/cli/agent_summary.go internal/cli/agent_summary_test.go
git commit -m "feat: add AgentSummary canonical output model"
```

---

### Task 4: Refactor whoami to use AgentSummary

Replace the standalone whoami command's ad-hoc output with `BuildAgentSummary`
and `FormatAgentSummary`. Remove the daemon-based whoami path — the standalone
file-based approach with optional daemon enrichment replaces both.

**Files:**
- Modify: `cmd/thrum/main.go:1118-1168` (standalone whoami command)
- Modify: `cmd/thrum/main.go:1757-1789` (agent whoami subcommand)

**Step 1: Update standalone whoami command**

In `cmd/thrum/main.go`, replace the whoami RunE at lines 1118-1164. The new
logic:

1. Load identity file (existing `config.LoadIdentityWithPath`)
2. Attempt daemon connection for enrichment (non-fatal if fails)
3. Call `BuildAgentSummary`
4. JSON mode: `json.MarshalIndent(summary)`
5. Human mode: `FormatAgentSummary(summary)`

```go
RunE: func(cmd *cobra.Command, args []string) error {
    identityFile, identityPath, err := config.LoadIdentityWithPath(flagRepo)
    if err != nil {
        thrumDir := filepath.Join(flagRepo, ".thrum")
        if _, statErr := os.Stat(thrumDir); os.IsNotExist(statErr) {
            return fmt.Errorf("thrum not initialized in this repository\n  Run 'thrum init' first")
        }
        identitiesDir := filepath.Join(thrumDir, "identities")
        if _, statErr := os.Stat(identitiesDir); os.IsNotExist(statErr) {
            return fmt.Errorf("no agent identities registered\n  Run 'thrum quickstart --role <role> --module <module>' to register")
        }
        return err
    }

    // Try daemon enrichment (non-fatal)
    var daemonInfo *cli.WhoamiResult
    if client, err := getClient(); err == nil {
        defer func() { _ = client.Close() }()
        if result, err := cli.AgentWhoami(client, identityFile.Agent.Name); err == nil {
            daemonInfo = result
        }
    }

    summary := cli.BuildAgentSummary(identityFile, identityPath, daemonInfo)

    if flagJSON {
        output, err := json.MarshalIndent(summary, "", "  ")
        if err != nil {
            return fmt.Errorf("marshal JSON output: %w", err)
        }
        fmt.Println(string(output))
    } else {
        fmt.Print(cli.FormatAgentSummary(summary))
    }

    return nil
},
```

**Step 2: Update agent whoami subcommand**

In `cmd/thrum/main.go` around line 1762, replace the agent whoami handler to
also use `BuildAgentSummary`. Load identity file first, then call daemon for
enrichment:

```go
RunE: func(cmd *cobra.Command, args []string) error {
    // Load identity from file
    identityFile, identityPath, err := config.LoadIdentityWithPath(flagRepo)
    if err != nil {
        return fmt.Errorf("failed to resolve agent identity: %w\n  Register with: thrum quickstart --name <name> --role <role> --module <module>", err)
    }

    // Try daemon enrichment
    var daemonInfo *cli.WhoamiResult
    if client, clientErr := getClient(); clientErr == nil {
        defer func() { _ = client.Close() }()
        if result, rpcErr := cli.AgentWhoami(client, identityFile.Agent.Name); rpcErr == nil {
            daemonInfo = result
        }
    }

    summary := cli.BuildAgentSummary(identityFile, identityPath, daemonInfo)

    if flagJSON {
        output, _ := json.MarshalIndent(summary, "", "  ")
        fmt.Println(string(output))
    } else {
        fmt.Print(cli.FormatAgentSummary(summary))
    }

    return nil
},
```

**Step 3: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go build ./cmd/thrum/ && go test ./internal/cli/ -v`
Expected: Build succeeds, tests pass

**Step 4: Manual verification**

Run: `thrum whoami` and `thrum whoami --json` — both should show the same
fields.

**Step 5: Commit**

```bash
git add cmd/thrum/main.go
git commit -m "refactor: whoami uses AgentSummary for consistent JSON/human output"
```

---

### Task 5: Refactor agent list to show branch and intent

Update `FormatAgentList` and `FormatAgentListWithContext` to include branch and
intent in the box-drawing tree output.

**Files:**
- Modify: `internal/cli/agent.go:231-287` (FormatAgentList)
- Modify: `internal/cli/agent.go:345-360+` (FormatAgentListWithContext)
- Modify: `internal/cli/agent_test.go` (update expected output)
- Modify: `internal/cli/coverage_test.go` (update expected output)

**Step 1: Update FormatAgentListWithContext**

The `FormatAgentListWithContext` function already has access to `AgentWorkContext`
which contains `Branch` and `Intent`. Update it to always show these fields in
the box tree:

After the status line (active/offline), add:

```go
if ctx.Branch != "" {
    output.WriteString(fmt.Sprintf("│  Branch:  %s\n", ctx.Branch))
}
if ctx.Intent != "" {
    output.WriteString(fmt.Sprintf("│  Intent:  %s\n", ctx.Intent))
}
```

**Step 2: Update FormatAgentList (basic view)**

The basic `FormatAgentList` doesn't have context info. This is acceptable —
the basic view shows registration info. The `--context` flag adds the
enriched view. No change needed here beyond ensuring the test expectations
match.

**Step 3: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestFormatAgentList' -v`
Expected: PASS (update test assertions if they check exact output)

**Step 4: Commit**

```bash
git add internal/cli/agent.go internal/cli/agent_test.go internal/cli/coverage_test.go
git commit -m "feat: agent list shows branch and intent in context view"
```

---

### Task 6: Update team output to consistently show worktree

Ensure `FormatTeam` always shows worktree, not just as part of "Location".
Add it as a dedicated field.

**Files:**
- Modify: `internal/cli/team.go:63-178` (FormatTeam)

**Step 1: Update FormatTeam**

The current `FormatTeam` shows worktree as part of "Location" combined with
hostname. Add a separate "Worktree:" line after "Location:" when worktree is
present, so it's always visible:

After the Location block (line ~96), the intent is already shown (line 126).
Check that worktree is shown independently of hostname. If `WorktreePath` is
set, show it as a dedicated line even if hostname is empty. The current code
already handles this in the Location section — verify it works when hostname
is empty (the `else` branch at line 91 handles this).

The key fix: ensure `Worktree:` appears as a labeled field, not buried in
Location. Replace the Location block:

```go
// Worktree
if m.WorktreePath != "" {
    out.WriteString(fmt.Sprintf("Worktree: %s\n", filepath.Base(m.WorktreePath)))
}

// Hostname
if m.Hostname != "" {
    out.WriteString(fmt.Sprintf("Host:     %s\n", m.Hostname))
}
```

**Step 2: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestFormatTeam' -v`
Expected: PASS (update assertions if needed)

**Step 3: Commit**

```bash
git add internal/cli/team.go
git commit -m "feat: team output shows worktree as dedicated field"
```

---

### Task 7: Update status and overview to use FormatAgentSummary

Replace the agent section in `FormatStatus` and `FormatOverview` with
`FormatAgentSummary` for the "self" identity display.

**Files:**
- Modify: `internal/cli/status.go:142-168` (FormatStatus agent section)
- Modify: `internal/cli/overview.go:87-135` (FormatOverview identity section)
- Modify: `internal/cli/status_test.go`
- Modify: `internal/cli/overview_test.go`

**Step 1: Update FormatStatus**

In the agent section of `FormatStatus` (lines 147-168), replace the manual
formatting with a call to `FormatAgentSummary`. Build the summary from
`StatusResult.Agent` and `StatusResult.WorkContext`:

```go
if result.Agent != nil {
    summary := &AgentSummary{
        AgentID:      result.Agent.AgentID,
        Role:         result.Agent.Role,
        Module:       result.Agent.Module,
        Display:      result.Agent.Display,
        SessionID:    result.Agent.SessionID,
        SessionStart: result.Agent.SessionStart,
    }
    // Enrich from work context
    if result.WorkContext != nil {
        summary.Branch = result.WorkContext.Branch
        summary.Intent = result.WorkContext.Intent
    }
    output.WriteString(FormatAgentSummary(summary))
}
```

**Step 2: Update FormatOverview**

Similarly, replace the identity section (lines 92-135) with `FormatAgentSummary`.

**Step 3: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestFormatStatus|TestFormatOverview' -v`
Expected: PASS (update assertions for new field order)

**Step 4: Commit**

```bash
git add internal/cli/status.go internal/cli/overview.go internal/cli/status_test.go internal/cli/overview_test.go
git commit -m "refactor: status and overview use FormatAgentSummary"
```

---

### Task 8: Refactor thrum init to do full setup

This is the largest task. Refactor `thrum init` to:
1. Create `.thrum/` (existing)
2. Prompt for name, role, module (new interactive flow)
3. Select runtime (existing interactive flow)
4. Compute and present intent (new)
5. Auto-populate identity fields (new)
6. Write v3 identity file (new)
7. Start daemon (new)
8. Register agent, start session, set intent (new)
9. Show `FormatAgentSummary` output (new)

**Files:**
- Modify: `cmd/thrum/main.go:169-339` (init command handler)
- Modify: `internal/cli/init.go` (add InitFull function or extend Init)

**Step 1: Add agent prompting to init command**

In `cmd/thrum/main.go`, after the `.thrum/` creation (Step 1, line ~242) and
before runtime selection (Step 2, line ~244), add the interactive agent setup:

```go
// Step 1.5: Agent setup (interactive or from flags)
agentNameResolved := agentName
agentRoleResolved := agentRole
agentModuleResolved := agentModule

if isInteractive() && !flagQuiet {
    reader := bufio.NewReader(os.Stdin)

    // Role prompt
    defaultRole := agentRoleResolved
    if defaultRole == "" {
        defaultRole = "implementer"
    }
    fmt.Printf("\nAgent setup:\n")
    fmt.Printf("  Role [%s]: ", defaultRole)
    input, _ := reader.ReadString('\n')
    input = strings.TrimSpace(input)
    if input != "" {
        agentRoleResolved = input
    } else {
        agentRoleResolved = defaultRole
    }

    // Name prompt (default from role)
    defaultName := agentNameResolved
    if defaultName == "" {
        defaultName = agentRoleResolved
    }
    fmt.Printf("  Name [%s]: ", defaultName)
    input, _ = reader.ReadString('\n')
    input = strings.TrimSpace(input)
    if input != "" {
        agentNameResolved = input
    } else {
        agentNameResolved = defaultName
    }

    // Module prompt (default from branch)
    defaultModule := agentModuleResolved
    if defaultModule == "" {
        defaultModule = cli.GetCurrentBranch(flagRepo)
        if defaultModule == "" {
            defaultModule = "main"
        }
    }
    fmt.Printf("  Module [%s]: ", defaultModule)
    input, _ = reader.ReadString('\n')
    input = strings.TrimSpace(input)
    if input != "" {
        agentModuleResolved = input
    } else {
        agentModuleResolved = defaultModule
    }
} else {
    // Non-interactive: use defaults
    if agentRoleResolved == "" {
        agentRoleResolved = "implementer"
    }
    if agentNameResolved == "" {
        agentNameResolved = agentRoleResolved
    }
    if agentModuleResolved == "" {
        agentModuleResolved = "main"
    }
}
```

**Step 2: Add intent computation and prompt**

After runtime selection (Step 3, line ~331), add intent:

```go
// Step 4.5: Intent
repoName := cli.GetRepoName(flagRepo)
intent := cli.DefaultIntent(agentRoleResolved, repoName)

if isInteractive() && !flagQuiet {
    fmt.Printf("\nIntent: %s\n", intent)
    fmt.Printf("  Edit? [Y/n]: ")
    reader := bufio.NewReader(os.Stdin)
    input, _ := reader.ReadString('\n')
    input = strings.TrimSpace(input)
    if input != "" && strings.ToLower(input) != "y" && strings.ToLower(input) != "yes" {
        fmt.Printf("  Intent: ")
        newIntent, _ := reader.ReadString('\n')
        newIntent = strings.TrimSpace(newIntent)
        if newIntent != "" {
            intent = newIntent
        }
    }
}
```

**Step 3: Write identity file, start daemon, register**

After intent (still in init command handler):

```go
// Step 5: Write identity file (v3)
thrumDir := filepath.Join(flagRepo, ".thrum")
idFile := &config.IdentityFile{
    Version:  3,
    RepoID:   cli.GetRepoID(flagRepo),
    Agent: config.AgentConfig{
        Kind:    "agent",
        Name:    agentNameResolved,
        Role:    agentRoleResolved,
        Module:  agentModuleResolved,
        Display: cli.AutoDisplay(agentRoleResolved, agentModuleResolved),
    },
    Worktree: cli.GetWorktreeName(flagRepo),
    Branch:   cli.GetCurrentBranch(flagRepo),
    Intent:   intent,
}
if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
    return fmt.Errorf("save identity file: %w", err)
}

// Step 6: Start daemon if not running
if !dryRun {
    // Try to connect; if fails, start daemon
    if client, err := getClient(); err != nil {
        // Daemon not running — start it
        startCmd := exec.Command("thrum", "daemon", "start")
        startCmd.Dir = flagRepo
        if out, err := startCmd.CombinedOutput(); err != nil {
            return fmt.Errorf("start daemon: %w\n%s", err, string(out))
        }
        if !flagQuiet {
            fmt.Println("✓ Daemon started")
        }
    } else {
        _ = client.Close()
        if !flagQuiet {
            fmt.Println("✓ Daemon already running")
        }
    }

    // Step 7: Register, session, intent via quickstart logic
    client, err := getClient()
    if err != nil {
        return fmt.Errorf("connect to daemon after start: %w", err)
    }
    defer func() { _ = client.Close() }()

    qsOpts := cli.QuickstartOptions{
        Name:     agentNameResolved,
        Role:     agentRoleResolved,
        Module:   agentModuleResolved,
        Display:  cli.AutoDisplay(agentRoleResolved, agentModuleResolved),
        Intent:   intent,
        RepoPath: flagRepo,
        NoInit:   true, // We already did init
        Force:    true,
    }
    qsResult, err := cli.Quickstart(client, qsOpts)
    if err != nil {
        return fmt.Errorf("agent setup: %w", err)
    }

    // Update identity file with session ID
    if qsResult.Session != nil {
        idFile.SessionID = qsResult.Session.SessionID
        _ = config.SaveIdentityFile(thrumDir, idFile)
    }

    if !flagQuiet {
        if qsResult.Register != nil {
            fmt.Printf("✓ Agent registered: %s\n", qsResult.Register.AgentID)
        }
        if qsResult.Session != nil {
            fmt.Printf("✓ Session started: %s\n", qsResult.Session.SessionID)
        }
        if qsResult.Intent != nil {
            fmt.Println("✓ Intent set")
        }
    }

    // Step 8: Show whoami-style output
    if !flagQuiet {
        fmt.Println()
        // Reload identity to get updated_at
        reloadedID, idPath, _ := config.LoadIdentityWithPath(flagRepo)
        if reloadedID != nil {
            var daemonInfo *cli.WhoamiResult
            if result, err := cli.AgentWhoami(client, agentNameResolved); err == nil {
                daemonInfo = result
            }
            summary := cli.BuildAgentSummary(reloadedID, idPath, daemonInfo)
            fmt.Print(cli.FormatAgentSummary(summary))
        }
    }
}
```

**Step 4: Build and test**

Run: `cd /Users/leon/dev/opensource/thrum && go build ./cmd/thrum/`
Expected: Build succeeds

**Step 5: Manual integration test**

```bash
# Clean slate
rm -rf /Users/leon/dev/opensource/thrum/.thrum
thrum daemon stop 2>/dev/null

# Test interactive init
thrum init

# Verify output shows full whoami-style summary
# Verify identity file has all v3 fields
cat .thrum/identities/*.json

# Test non-interactive init
rm -rf .thrum
thrum daemon stop 2>/dev/null
thrum init --agent-name coordinator --agent-role coordinator --module main --runtime claude --force

# Verify same output
```

**Step 6: Commit**

```bash
git add cmd/thrum/main.go internal/cli/init.go
git commit -m "feat: thrum init does full setup (prompt, daemon, register, session, intent)"
```

---

### Task 9: Update quickstart to enrich identity file on run

When quickstart runs on an existing `.thrum/`, ensure it updates the identity
file with any missing v3 fields and writes session/intent back to disk.

**Files:**
- Modify: `internal/cli/quickstart.go:40-128` (Quickstart function)
- Modify: `cmd/thrum/main.go` (quickstart command handler, ~line 3498+)

**Step 1: Add identity file enrichment to Quickstart**

After successful registration and session start in `quickstart.go`, add logic
to load, enrich, and save the identity file:

```go
// After session start (line ~116), before intent:
// Enrich identity file with session and auto-populated fields
repoPath := opts.RepoPath
if repoPath == "" {
    repoPath = "."
}
if idFile, _, err := config.LoadIdentityWithPath(repoPath); err == nil {
    thrumDir := filepath.Join(repoPath, ".thrum")
    changed := false

    if idFile.Version < 3 {
        idFile.Version = 3
        changed = true
    }
    if idFile.Branch == "" {
        idFile.Branch = GetCurrentBranch(repoPath)
        changed = true
    }
    if idFile.RepoID == "" {
        if repoID := GetRepoID(repoPath); repoID != "" {
            idFile.RepoID = repoID
            changed = true
        }
    }
    if idFile.Agent.Display == "" {
        idFile.Agent.Display = AutoDisplay(idFile.Agent.Role, idFile.Agent.Module)
        changed = true
    }
    if sessResult != nil && sessResult.SessionID != "" {
        idFile.SessionID = sessResult.SessionID
        changed = true
    }
    if opts.Intent != "" && idFile.Intent != opts.Intent {
        idFile.Intent = opts.Intent
        changed = true
    } else if idFile.Intent == "" {
        repoName := GetRepoName(repoPath)
        idFile.Intent = DefaultIntent(idFile.Agent.Role, repoName)
        changed = true
    }

    if changed {
        _ = config.SaveIdentityFile(thrumDir, idFile)
    }
}
```

**Step 2: Update quickstart to set default intent when none provided**

In the quickstart command handler in `main.go`, before calling
`cli.Quickstart`, if no `--intent` flag was given, compute the default:

```go
if intentFlag == "" {
    repoName := cli.GetRepoName(flagRepo)
    intentFlag = cli.DefaultIntent(flagRole, repoName)
}
```

**Step 3: Run tests**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./internal/cli/ -run 'TestQuickstart' -v && go build ./cmd/thrum/`
Expected: PASS and build succeeds

**Step 4: Commit**

```bash
git add internal/cli/quickstart.go cmd/thrum/main.go
git commit -m "feat: quickstart enriches identity file with v3 fields"
```

---

### Task 10: Update set-intent to write back to identity file

When `thrum agent set-intent` or `thrum session set-intent` is called, write
the new intent to the identity file on disk.

**Files:**
- Modify: `cmd/thrum/main.go` (set-intent command handlers)

**Step 1: Find and update the set-intent handlers**

After the daemon RPC call succeeds, add:

```go
// Write intent back to identity file
if idFile, _, err := config.LoadIdentityWithPath(flagRepo); err == nil {
    thrumDir := filepath.Join(flagRepo, ".thrum")
    idFile.Intent = newIntent
    _ = config.SaveIdentityFile(thrumDir, idFile)
}
```

**Step 2: Build and test**

Run: `cd /Users/leon/dev/opensource/thrum && go build ./cmd/thrum/`

**Step 3: Commit**

```bash
git add cmd/thrum/main.go
git commit -m "feat: set-intent writes back to identity file"
```

---

### Task 11: Clean up dead code

Remove the old `FormatWhoami` function from `agent.go` (the daemon-RPC-based
one that is now replaced by `FormatAgentSummary`). Remove any unused
`WhoamiResult`-only code paths.

**Files:**
- Modify: `internal/cli/agent.go:430-461` (remove old FormatWhoami)
- Modify: `internal/cli/agent_test.go` (remove/update TestFormatWhoami)

**Step 1: Remove FormatWhoami**

Delete the `FormatWhoami` function at `agent.go:430-461`.

**Step 2: Search for remaining callers**

Run: `grep -r "FormatWhoami" --include="*.go"` — should find zero references
after task 4 and 7 replaced all callers.

**Step 3: Run full test suite**

Run: `cd /Users/leon/dev/opensource/thrum && go test ./... 2>&1 | tail -30`
Expected: All tests pass

**Step 4: Commit**

```bash
git add internal/cli/agent.go internal/cli/agent_test.go
git commit -m "chore: remove FormatWhoami replaced by FormatAgentSummary"
```

---

### Task 12: Full integration test

Run the complete test suite and do manual verification.

**Files:** None (testing only)

**Step 1: Run all tests**

Run: `cd /Users/leon/dev/opensource/thrum && make ci`
Expected: All tests pass, linting passes, build succeeds

**Step 2: Manual end-to-end verification**

```bash
# Clean slate
rm -rf .thrum
thrum daemon stop 2>/dev/null

# Test 1: Interactive init
thrum init
# Verify: prompts for role, name, module, runtime, intent
# Verify: shows full whoami-style output at end
# Verify: daemon is running

# Test 2: whoami consistency
thrum whoami
thrum whoami --json
# Verify: both show same fields (agent_id, role, module, display, branch,
# intent, session, worktree, identity_file)

# Test 3: agent list
thrum agent list --context
# Verify: shows branch and intent

# Test 4: team
thrum team --all
# Verify: shows worktree as dedicated field

# Test 5: status
thrum status
# Verify: agent section matches whoami output format

# Test 6: overview
thrum overview
# Verify: identity section matches whoami output format

# Test 7: identity file contents
cat .thrum/identities/*.json | python3 -m json.tool
# Verify: version=3, repo_id populated, display populated,
# branch populated, intent populated, session_id populated
```

**Step 3: Verify non-interactive init**

```bash
rm -rf .thrum
thrum daemon stop 2>/dev/null
thrum init --agent-name coordinator --agent-role coordinator --module main --runtime claude
thrum whoami --json
# Verify: all fields populated
```

**Step 4: Verify quickstart enrichment**

```bash
# With existing .thrum/ (v3), quickstart should re-enrich
thrum quickstart --role coordinator --module main --force
cat .thrum/identities/*.json | python3 -m json.tool
# Verify: session_id updated, all fields present
```

**Step 5: Final commit (if any test fixes needed)**

```bash
git add -A
git commit -m "fix: integration test fixes for init/identity consistency"
```
