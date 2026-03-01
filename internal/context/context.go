// Package context provides per-agent context storage for Thrum.
// Context files are markdown files stored in .thrum/context/{agent-name}.md.
// They allow agents to persist volatile project state across sessions.
package context

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/leonletto/thrum/internal/config"
)

// Save writes context content for the named agent.
// Creates the context directory if it doesn't exist.
func Save(thrumDir, agentName string, content []byte) error {
	dir := filepath.Join(thrumDir, "context")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return fmt.Errorf("create context directory: %w", err)
	}

	path := filepath.Join(dir, agentName+".md")
	if err := os.WriteFile(path, content, 0644); err != nil { //nolint:gosec // G306 - markdown files, not secrets
		return fmt.Errorf("write context file: %w", err)
	}

	return nil
}

// Load reads context content for the named agent.
// Returns nil, nil if the context file doesn't exist.
func Load(thrumDir, agentName string) ([]byte, error) {
	path := filepath.Join(thrumDir, "context", agentName+".md")
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal context directory
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
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal context directory
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
	if err := os.WriteFile(path, content, 0644); err != nil { //nolint:gosec // G306 - markdown files, not secrets
		return fmt.Errorf("write preamble file: %w", err)
	}

	return nil
}

// DefaultPreamble returns the default preamble template with basic thrum commands.
func DefaultPreamble() []byte {
	return []byte(`## Thrum Quick Reference

**Check messages:** ` + "`thrum inbox --unread`" + `
**Send message:** ` + "`thrum send \"message\" --to @role`" + `
**Reply:** ` + "`thrum reply <MSG_ID> \"response\"`" + `
**Status:** ` + "`thrum status`" + `
**Who's online:** ` + "`thrum agent list --context`" + `
**Save context:** ` + "`thrum context save`" + `
**Wait for messages:** ` + "`thrum wait --after -30s --timeout 5m`" + ` (` + "`--after -30s`" + ` = include messages sent up to 30s ago)
`)
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
	tmplContent, err := os.ReadFile(templatePath) //nolint:gosec // G304 - path from internal thrum directory
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
	idData, err := os.ReadFile(identityPath) //nolint:gosec // G304 - path from internal thrum directory
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
		data, err := os.ReadFile(path) //nolint:gosec // G304 - path from internal thrum directory
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
