// Package context provides per-agent context storage for Thrum.
// Context files are markdown files stored in .thrum/context/{agent-name}.md.
// They allow agents to persist volatile project state across sessions.
package context

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/leonletto/thrum/internal/config"
)

//go:embed strategies/*.md
var strategyFS embed.FS

// WriteStrategies writes embedded strategy files to .thrum/strategies/.
// Overwrites existing files (keeps strategies in sync with binary version).
func WriteStrategies(thrumDir string) error {
	strategiesDir := filepath.Join(thrumDir, "strategies")
	if err := os.MkdirAll(strategiesDir, 0750); err != nil {
		return fmt.Errorf("create strategies directory: %w", err)
	}

	entries, err := strategyFS.ReadDir("strategies")
	if err != nil {
		return fmt.Errorf("read embedded strategies: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := strategyFS.ReadFile("strategies/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read embedded strategy %s: %w", entry.Name(), err)
		}
		outPath := filepath.Join(strategiesDir, entry.Name())
		if err := os.WriteFile(outPath, data, 0644); err != nil { //#nosec G306 -- markdown strategy file, not sensitive data
			return fmt.Errorf("write strategy %s: %w", entry.Name(), err)
		}
	}

	return nil
}

// Save writes context content for the named agent.
// Creates the context directory if it doesn't exist.
func Save(thrumDir, agentName string, content []byte) error {
	dir := filepath.Join(thrumDir, "context")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}

	path := filepath.Join(dir, agentName+".md")
	if err := os.WriteFile(path, content, 0644); err != nil { //#nosec G306 -- markdown context file, not sensitive data
		return fmt.Errorf("write context file: %w", err)
	}

	return nil
}

// Load reads context content for the named agent.
// Returns nil, nil if the context file doesn't exist.
func Load(thrumDir, agentName string) ([]byte, error) {
	path := filepath.Join(thrumDir, "context", agentName+".md")
	data, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/context/<agentName>.md, an internal context file
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read context file: %w", err)
	}
	return data, nil
}

// Clear removes the context file for the named agent.
// Returns nil if the file doesn't exist (idempotent).
func Clear(thrumDir, agentName string) error {
	path := filepath.Join(thrumDir, "context", agentName+".md")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove context file: %w", err)
	}
	return nil
}

// ContextPath returns the absolute path to the context file for the named agent.
func ContextPath(thrumDir, agentName string) string {
	return filepath.Join(thrumDir, "context", agentName+".md")
}

// PreamblePath returns the absolute path to the preamble file for the named agent.
func PreamblePath(thrumDir, agentName string) string {
	return filepath.Join(thrumDir, "context", agentName+"_preamble.md")
}

// LoadPreamble reads the preamble content for the named agent.
// Returns nil, nil if the preamble file doesn't exist.
func LoadPreamble(thrumDir, agentName string) ([]byte, error) {
	path := filepath.Join(thrumDir, "context", agentName+"_preamble.md")
	data, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/context/<agentName>_preamble.md, an internal context file
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read preamble file: %w", err)
	}
	return data, nil
}

// SavePreamble writes preamble content for the named agent.
// Creates the context directory if it doesn't exist.
func SavePreamble(thrumDir, agentName string, content []byte) error {
	dir := filepath.Join(thrumDir, "context")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}

	path := filepath.Join(dir, agentName+"_preamble.md")
	if err := os.WriteFile(path, content, 0644); err != nil { //#nosec G306 -- markdown preamble file, not sensitive data
		return fmt.Errorf("write preamble file: %w", err)
	}

	return nil
}

// DefaultPreamble returns the default preamble template with basic thrum commands.
func DefaultPreamble() []byte {
	return []byte(`## Thrum Quick Reference

## Operating Principles

1. **ALWAYS keep a background message listener running.** [LISTENER RULE #1]
   Missing messages = broken coordination. Spawn on start, re-arm on every return.
2. **Check inbox before starting work and at every breakpoint.**
   ` + "`thrum inbox --unread`" + ` — never assume you have the full picture.
3. **Send to agent NAMES, never role names.**
   ` + "`thrum send \"msg\" --to @coordinator_main`" + ` not ` + "`--to @coordinator`" + `.
   Role names fan out to ALL agents with that role. Run ` + "`thrum team`" + ` first.
4. **Save context before compaction.**
   Use ` + "`/thrum:update-context`" + ` skill. **NEVER run ` + "`thrum context save`" + ` manually** — it overwrites accumulated session state.

## Startup Protocol

1. Run ` + "`thrum prime`" + ` — get spawn command with correct repo path pre-filled
2. Spawn background listener (re-arm every return — see below) [LISTENER RULE #2]
3. Check inbox: ` + "`thrum inbox --unread`" + `
4. Check team: ` + "`thrum team`" + `

## Anti-Patterns

` + "❌" + ` **Deaf Agent** — No listener running. You miss messages, block coordination, leave teammates waiting.
` + "❌" + ` **Silent Agent** — Never sends status updates. Coordinator cannot track progress or unblock dependencies.
` + "❌" + ` **Context Hog** — Reads entire files into context. Use ` + "`auggie-mcp codebase-retrieval`" + ` instead.

## Background Message Listener [LISTENER RULE #2]

ALWAYS keep a background listener running. Spawn on session start, re-arm every
time it returns (both MESSAGES_RECEIVED and timeout). Run ` + "`thrum prime`" + ` to get the
spawn command with the correct repo path pre-filled. The pattern:

` + "```text" + `
Task(subagent_type="message-listener", model="haiku", run_in_background=true,
  prompt="Listen for Thrum messages.\nSTEP_1: <repo>/scripts/thrum-startup.sh --listener-heartbeat\nSTEP_2: thrum wait --timeout 8m --after -15s")
` + "```" + `

Replace ` + "`<repo>`" + ` with the actual repo path (shown in ` + "`thrum prime`" + ` output).
` + "`--after -15s`" + ` = include messages sent up to 15s ago, covers re-arm gap between
listener cycles.

## Agent Strategies

Read these strategy files for operational patterns. They are in ` + "`.thrum/strategies/`" + `:

- ` + "`.thrum/strategies/sub-agent-strategy.md`" + ` — When and how to delegate work to sub-agents
- ` + "`.thrum/strategies/thrum-registration.md`" + ` — Registration, messaging, and coordination patterns
- ` + "`.thrum/strategies/resume-after-context-loss.md`" + ` — How to resume work after compaction or session restart

## Command Reference

**Check messages:** ` + "`thrum inbox --unread`" + ` (does not mark as read)
**Check sent status:** ` + "`thrum sent --unread`" + ` (messages with unread recipients)
**Mark all read:** ` + "`thrum message read --all`" + `
**Send message:** ` + "`thrum send \"message\" --to @<agent_name>`" + ` — ALWAYS use the specific agent name (e.g., ` + "`@coordinator_main`" + `), NEVER the role (e.g., ` + "`@coordinator`" + `). Role names fan out to ALL agents with that role. Run ` + "`thrum team`" + ` to find exact names.
**Reply:** ` + "`thrum reply <MSG_ID> \"response\"`" + `
**Status:** ` + "`thrum status`" + `
**Who's online:** ` + "`thrum team`" + `

` + "⚠" + ` **REMINDER: Is your listener running? If not, spawn it now.** [LISTENER RULE #3]
`)
}

// RoleAwarePreamble returns a preamble with a role-specific behavioral header
// prepended to the default preamble. For unknown roles, returns the default.
func RoleAwarePreamble(role string) []byte {
	header := roleHeader(role)
	if header == "" {
		return DefaultPreamble()
	}
	base := DefaultPreamble()
	return append([]byte(header+"\n---\n\n"), base...)
}

// roleHeader returns a brief role-specific behavioral header for known roles,
// or an empty string for unknown roles.
func roleHeader(role string) string {
	switch strings.ToLower(role) {
	case "coordinator":
		return "## Your Role: Coordinator\n\n" +
			"You orchestrate the team. You dispatch tasks, review completions, and make\n" +
			"decisions. You do NOT implement features — delegate to implementers. Your\n" +
			"value is fast decisions that unblock agents, not perfect code written yourself.\n" +
			"Reply to every message. Silence stalls your team."
	case "implementer":
		return "## Your Role: Implementer\n\n" +
			"You build what you're assigned. Wait for tasks from your coordinator — do not\n" +
			"self-assign work. Implement exactly what the task description says, test it,\n" +
			"commit it, and report completion. Stay in your worktree. Do not touch files\n" +
			"outside your scope."
	case "planner":
		return "## Your Role: Planner\n\n" +
			"You design and plan. You create implementation plans, break epics into tasks,\n" +
			"and write design documents. You do NOT write implementation code. Your output\n" +
			"is plans and task descriptions detailed enough for implementers to execute\n" +
			"without ambiguity."
	case "researcher":
		return "## Your Role: Researcher\n\n" +
			"You investigate and report. When asked a question, you find the answer with\n" +
			"evidence — file paths, line numbers, concrete data. You do NOT modify code.\n" +
			"Your findings must be specific enough that the requester can act on them\n" +
			"without re-investigating."
	case "reviewer":
		return "## Your Role: Reviewer\n\n" +
			"You review code for correctness, security, and quality. You do NOT implement\n" +
			"fixes — you identify issues and suggest solutions. Your findings must include\n" +
			"file:line references and severity ratings. Be thorough but fair — push back\n" +
			"on false positives."
	case "tester":
		return "## Your Role: Tester\n\n" +
			"You write and run tests. You design test cases, implement test code, and\n" +
			"verify that implementations meet their acceptance criteria. Report test\n" +
			"results with specific pass/fail details and reproduction steps for failures."
	case "deployer":
		return "## Your Role: Deployer\n\n" +
			"You handle deployment operations. You run builds, manage releases, and\n" +
			"monitor deployment health. Follow runbooks exactly. Report deployment status\n" +
			"and any issues immediately. Do not make ad-hoc changes — follow the process."
	case "documenter":
		return "## Your Role: Documenter\n\n" +
			"You write documentation. You create, update, and organize docs based on\n" +
			"the current state of the codebase. Your docs must be accurate, concise, and\n" +
			"actionable. Verify code references are correct before writing about them."
	case "monitor":
		return "## Your Role: Monitor\n\n" +
			"You watch system health and report anomalies. You check logs, metrics, and\n" +
			"status endpoints. Report issues immediately with evidence. Do not attempt\n" +
			"fixes — escalate to the coordinator with enough context for them to decide."
	default:
		return ""
	}
}

// EnsurePreamble creates the default preamble file if it doesn't exist.
// No-op if the file already exists.
func EnsurePreamble(thrumDir, agentName string) error {
	path := filepath.Join(thrumDir, "context", agentName+"_preamble.md")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	return SavePreamble(thrumDir, agentName, DefaultPreamble())
}

// RoleTemplateData holds variables available to role templates.
type RoleTemplateData struct {
	AgentName       string
	Role            string
	Module          string
	WorktreePath    string
	RepoRoot        string
	CoordinatorName string
}

// RenderRoleTemplate renders the role template for the given agent.
// It checks for .thrum/role_templates/{role}.md, and if found, renders it
// with the agent's identity data. Returns nil, nil if no template exists.
func RenderRoleTemplate(thrumDir, agentName, role string) ([]byte, error) {
	templatePath := filepath.Join(thrumDir, "role_templates", role+".md")
	tmplContent, err := os.ReadFile(templatePath) // #nosec G304 -- templatePath is .thrum/role_templates/<role>.md, an internal directory
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read role template: %w", err)
	}

	// Load identity data for template variables
	data := buildTemplateData(thrumDir, agentName, role)

	tmpl, err := template.New(role + ".md").Parse(string(tmplContent))
	if err != nil {
		return nil, fmt.Errorf("parse role template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render role template: %w", err)
	}

	return buf.Bytes(), nil
}

// DeployAll re-renders preambles for all registered agents that have matching
// role templates. Returns a summary of what was updated.
type DeployResult struct {
	Updated []string // agent names that were updated
	Skipped []string // agent names with no matching template
}

// DeployAll iterates all identities and renders role templates for each.
// If agentFilter is non-empty, only that agent is processed.
// If dryRun is true, no files are written.
func DeployAll(thrumDir string, agentFilter string, dryRun bool) (*DeployResult, error) {
	identities, err := loadAllIdentities(thrumDir)
	if err != nil {
		return nil, fmt.Errorf("load identities: %w", err)
	}

	result := &DeployResult{}
	for _, id := range identities {
		name := id.Agent.Name
		if agentFilter != "" && name != agentFilter {
			continue
		}

		rendered, err := RenderRoleTemplate(thrumDir, name, id.Agent.Role)
		if err != nil {
			return nil, fmt.Errorf("render template for %s: %w", name, err)
		}
		if rendered == nil {
			result.Skipped = append(result.Skipped, name)
			continue
		}

		if !dryRun {
			if err := SavePreamble(thrumDir, name, rendered); err != nil {
				return nil, fmt.Errorf("save preamble for %s: %w", name, err)
			}
		}
		result.Updated = append(result.Updated, name)
	}

	return result, nil
}

// ListRoleTemplates returns a map of template name -> list of agents with that role.
func ListRoleTemplates(thrumDir string) (map[string][]string, error) {
	templatesDir := filepath.Join(thrumDir, "role_templates")
	entries, err := os.ReadDir(templatesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read role_templates directory: %w", err)
	}

	identities, err := loadAllIdentities(thrumDir)
	if err != nil {
		return nil, fmt.Errorf("load identities: %w", err)
	}

	// Build role -> agents map
	roleAgents := make(map[string][]string)
	for _, id := range identities {
		roleAgents[id.Agent.Role] = append(roleAgents[id.Agent.Role], id.Agent.Name)
	}

	result := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		role := strings.TrimSuffix(entry.Name(), ".md")
		result[entry.Name()] = roleAgents[role]
	}

	return result, nil
}

// buildTemplateData constructs the template data for a given agent.
func buildTemplateData(thrumDir, agentName, role string) *RoleTemplateData {
	data := &RoleTemplateData{
		AgentName: agentName,
		Role:      role,
	}

	// Load the agent's identity file for module/worktree info
	identityPath := filepath.Join(thrumDir, "identities", agentName+".json")
	idData, err := os.ReadFile(identityPath) // #nosec G304 -- identityPath is .thrum/identities/<agentName>.json, an internal directory
	if err == nil {
		var id config.IdentityFile
		if jsonErr := json.Unmarshal(idData, &id); jsonErr == nil {
			data.Module = id.Agent.Module
			data.WorktreePath = id.Worktree
		}
	}

	// Resolve RepoRoot from thrumDir (thrumDir is .thrum/, parent is repo root)
	data.RepoRoot = filepath.Dir(thrumDir)

	// Find coordinator name by scanning identities
	data.CoordinatorName = findCoordinatorName(thrumDir)

	return data
}

// findCoordinatorName scans identities for the first agent with role=coordinator.
func findCoordinatorName(thrumDir string) string {
	identities, err := loadAllIdentities(thrumDir)
	if err != nil {
		return "coordinator"
	}
	for _, id := range identities {
		if id.Agent.Role == "coordinator" {
			return id.Agent.Name
		}
	}
	return "coordinator" // fallback
}

// loadAllIdentities loads all identity files from .thrum/identities/.
func loadAllIdentities(thrumDir string) ([]*config.IdentityFile, error) {
	identitiesDir := filepath.Join(thrumDir, "identities")
	entries, err := os.ReadDir(identitiesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read identities directory: %w", err)
	}

	var identities []*config.IdentityFile
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(identitiesDir, entry.Name())
		data, err := os.ReadFile(path) // #nosec G304 -- path is .thrum/identities/<name>.json from directory listing
		if err != nil {
			continue
		}
		var id config.IdentityFile
		if err := json.Unmarshal(data, &id); err != nil {
			continue
		}
		identities = append(identities, &id)
	}

	return identities, nil
}
