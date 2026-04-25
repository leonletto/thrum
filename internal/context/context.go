// Package context provides per-agent context storage for Thrum.
// Context files are markdown files stored in .thrum/context/{agent-name}.md.
// They allow agents to persist volatile project state across sessions.
package context

import (
	"bytes"
	gocontext "context"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/leonletto/thrum/internal/daemon/safecmd"

	"github.com/leonletto/thrum/internal/config"
)

//go:embed strategies/*.md
var strategyFS embed.FS

//go:embed reference/llms.txt
var referenceFS embed.FS

// WriteStrategies writes embedded reference files to .thrum/:
//   - Strategy markdown files at .thrum/strategies/*.md
//   - Full CLI/config/RPC reference at .thrum/llms.txt
//
// Overwrites existing files on every call (keeps reference content in sync
// with the installed binary version). The .thrum/llms.txt file is written
// here rather than hand-edited; user edits will be overwritten on next
// daemon start or 'thrum init'.
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

	llmsData, err := referenceFS.ReadFile("reference/llms.txt")
	if err != nil {
		return fmt.Errorf("read embedded llms.txt: %w", err)
	}
	llmsPath := filepath.Join(thrumDir, "llms.txt")
	if err := os.WriteFile(llmsPath, llmsData, 0644); err != nil { //#nosec G306 -- reference content file, not sensitive data
		return fmt.Errorf("write llms.txt: %w", err)
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

// ProjectStateOpts controls what gets auto-filled in the skeleton.
type ProjectStateOpts struct {
	RepoName string
	Language string // Auto-detected: "Go", "Python", "Node.js", etc.
	Version  string // From latest git tag
	Branch   string
	Beads    string // e.g. "32 open, 245 closed" or empty
}

// GenerateProjectState creates the project_state.md skeleton with auto-detected fields.
func GenerateProjectState(opts *ProjectStateOpts) []byte {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# Project State — %s\n\n", opts.RepoName)
	fmt.Fprintf(&buf, "**Last Updated:** %s\n", time.Now().Format("2006-01-02"))
	buf.WriteString("**Phase:**\n\n---\n\n")
	buf.WriteString("## Current State Summary\n\n")
	if opts.Language != "" {
		fmt.Fprintf(&buf, "**Codebase:** %s\n", opts.Language)
	} else {
		buf.WriteString("**Codebase:**\n")
	}
	if opts.Version != "" {
		fmt.Fprintf(&buf, "**Version:** %s\n", opts.Version)
	} else {
		buf.WriteString("**Version:**\n")
	}
	fmt.Fprintf(&buf, "**Branch:** %s\n", opts.Branch)
	if opts.Beads != "" {
		fmt.Fprintf(&buf, "**Beads:** %s\n", opts.Beads)
	}
	buf.WriteString("\n### Architecture Health\n\n")
	buf.WriteString("| Component | Status | Details |\n")
	buf.WriteString("|-----------|--------|--------|\n")
	buf.WriteString("| | | |\n")
	buf.WriteString("\n### Key Architecture Decisions\n\n-\n")
	buf.WriteString("\n---\n\n## Recent Sessions\n")
	buf.WriteString("\n---\n\n## Open Epics / Active Work\n")
	buf.WriteString("\n---\n\n## What's Queued / Next Steps\n\n1.\n")
	buf.WriteString("\n---\n\n## Key Architecture Files\n\n")
	buf.WriteString("| File/Dir | Purpose |\n")
	buf.WriteString("|----------|---------|\n")
	buf.WriteString("| | |\n")
	return buf.Bytes()
}

// DetectLanguage checks for common project files and returns detected languages.
func DetectLanguage(repoRoot string) string {
	checks := []struct {
		file string
		lang string
	}{
		{"go.mod", "Go"},
		{"Cargo.toml", "Rust"},
		{"package.json", "Node.js"},
		{"pyproject.toml", "Python"},
		{"setup.py", "Python"},
		{"pom.xml", "Java"},
		{"build.gradle", "Java"},
	}
	var found []string
	seen := map[string]bool{}
	for _, c := range checks {
		if _, err := os.Stat(filepath.Join(repoRoot, c.file)); err == nil {
			if !seen[c.lang] {
				found = append(found, c.lang)
				seen[c.lang] = true
			}
		}
	}
	if len(found) == 0 {
		return ""
	}
	return strings.Join(found, " + ")
}

// DefaultPreamble returns the default mode-independent preamble.
// Messaging and listener content lives in FormatPrimeContext() section 5 (multi-agent only).
func DefaultPreamble() []byte {
	return []byte(`## Thrum Quick Reference

**Update project state:** ` + "`/thrum:update-project`" + ` — updates durable project state
**Load full briefing:** ` + "`thrum prime`" + ` — identity, preamble, project state, session context
**Show context:** ` + "`thrum context show`" + ` — both project state + session context
**Show project only:** ` + "`thrum context show --project`" + `
**Show session only:** ` + "`thrum context show --session`" + `

## Tmux Session Management

` + "`thrum tmux start`" + ` — Launch an agent in the current directory (create + launch + prime + attach)
` + "`thrum tmux create <name> --cwd <path>`" + ` — Create a detached tmux session
` + "`thrum tmux launch <name>`" + ` — Start the configured runtime in a session
` + "`thrum tmux status`" + ` — Show all managed sessions with state
` + "`thrum tmux connect [name]`" + ` — Attach to a running session (interactive picker if no name)
` + "`thrum tmux kill <name>`" + ` — Tear down a session
` + "`thrum tmux restart <name>`" + ` — Restart with conversation snapshot preserved

Tmux-managed agents receive message notifications instantly via daemon nudge —
no background listener needed. This is the most token-efficient way to run agents.

## Operating Principles

1. **Save context before compaction.**
   Use ` + "`/thrum:update-project`" + ` skill for durable project state.
2. **Run ` + "`thrum prime`" + ` on session start or after compaction** — it loads everything you need.
3. **Keep project_state.md current** — update it at session end so the next session starts informed.
4. **Prefer tmux sessions for agents** — ` + "`thrum tmux start`" + ` eliminates background
   listeners entirely. Messages arrive instantly via daemon nudge at zero token cost.

## Anti-Patterns

` + "❌" + ` **Context Hog** — Reads entire files into context. Use Grep, Glob, and
Explore sub-agents for code research instead.
` + "❌" + ` **Sub-Agent Dispatcher** — Spawning sub-agents into worktrees where Thrum agents
are running. Use ` + "`thrum send`" + ` to dispatch work via messaging instead.

## Agent Strategies

Read these strategy files for operational patterns. They are in ` + "`.thrum/strategies/`" + `:

- ` + "`.thrum/strategies/sub-agent-strategy.md`" + ` — When and how to delegate work to sub-agents
- ` + "`.thrum/strategies/thrum-registration.md`" + ` — Registration, messaging, and coordination patterns
- ` + "`.thrum/strategies/resume-after-context-loss.md`" + ` — How to resume work after compaction or session restart
- ` + "`.thrum/llms.txt`" + ` — Full CLI/config/RPC reference. Grep this before asking about thrum commands, config keys, or RPC methods. Version-locked to your installed binary (do not WebFetch the website copy — it may drift).
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
			"Reply to every message. Silence stalls your team.\n\n" +
			"### CRITICAL: Use Thrum Messaging to Dispatch Work\n\n" +
			"Dispatch work to implementer agents via `thrum send \"...\" --to @agent_name`.\n" +
			"**NEVER** spawn sub-agents (Agent tool) into worktrees where Thrum agents are\n" +
			"running. If an agent is registered in a worktree (`thrum team` shows them),\n" +
			"communicate with it through Thrum — that IS the coordination mechanism.\n\n" +
			"The correct flow:\n" +
			"1. `thrum tmux start` or `thrum tmux create` + `launch` to start an agent\n" +
			"2. Agent runs `/thrum:prime` to self-identify\n" +
			"3. Send work via `thrum send` — daemon nudges tmux pane instantly\n" +
			"4. Monitor progress via `thrum inbox --unread`\n\n" +
			"Sub-agents are for: message listeners, code reviewers, research/explore —\n" +
			"never for implementation work in another agent's worktree."
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
	case "orchestrator":
		return "## Your Role: Orchestrator\n\n" +
			"You are the execution engine. You receive validated plans, launch agents in\n" +
			"tmux sessions, manage epic-by-epic execution with review gates, and present\n" +
			"results for human-controlled merging. You NEVER write code or investigate\n" +
			"codebases — delegate everything. Your value is throughput: agents working,\n" +
			"epics closing, branches landing. Invoke /thrum:orchestrate when a plan arrives."
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

// RenderRoleTemplate renders the role template for the given agent and
// composes it with DefaultPreamble. The role template carries role-specific
// discipline; DefaultPreamble carries shared operational reference (Thrum
// Quick Reference, Tmux Session Management, Operating Principles, shared
// Anti-Patterns, Agent Strategies). The composed output is the rendered
// role-template content, a horizontal-rule separator, then DefaultPreamble —
// matching RoleAwarePreamble's compose pattern (role-specific first, default
// floor second).
//
// Returns nil, nil if no template exists at .thrum/role_templates/{role}.md.
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

	// Compose: rendered role template + separator + DefaultPreamble.
	// One newline pad between the rendered content and the separator so the
	// "---" sits on its own line regardless of whether the role template
	// ended with a trailing newline.
	composed := buf.Bytes()
	if !bytes.HasSuffix(composed, []byte("\n")) {
		composed = append(composed, '\n')
	}
	composed = append(composed, []byte("\n---\n\n")...)
	composed = append(composed, DefaultPreamble()...)

	// Compose user overlay: .thrum/context/<agentName>.md is auto-created
	// empty by quickstart and intended for hand-written customization. When
	// non-empty, append after DefaultPreamble with a separator so user
	// content sits at the very end (highest precedence in agent reading).
	overlayPath := filepath.Join(thrumDir, "context", agentName+".md")
	if overlay, err := os.ReadFile(overlayPath); err == nil && len(bytes.TrimSpace(overlay)) > 0 { // #nosec G304 -- overlayPath is .thrum/context/<agent>.md, an internal directory
		if !bytes.HasSuffix(composed, []byte("\n")) {
			composed = append(composed, '\n')
		}
		composed = append(composed, []byte("\n---\n\n")...)
		composed = append(composed, overlay...)
	}

	return composed, nil
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
	// Collect identity directories: main repo + all worktrees.
	repoDir := filepath.Dir(thrumDir)
	dirs := []string{filepath.Join(thrumDir, "identities")}
	for _, wtPath := range safecmd.WorktreePaths(gocontext.Background(), repoDir) {
		wtIDDir := filepath.Join(wtPath, ".thrum", "identities")
		if info, err := os.Stat(wtIDDir); err == nil && info.IsDir() {
			dirs = append(dirs, wtIDDir)
		}
	}

	seen := map[string]bool{} // deduplicate by agent name
	var identities []*config.IdentityFile
	for _, identitiesDir := range dirs {
		entries, err := os.ReadDir(identitiesDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("read identities directory %s: %w", identitiesDir, err)
		}
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
			if seen[id.Agent.Name] {
				continue
			}
			seen[id.Agent.Name] = true
			identities = append(identities, &id)
		}
	}

	return identities, nil
}
