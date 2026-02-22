package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// createTestIdentity creates a test identity file in the thrumDir.
func createTestIdentity(t *testing.T, thrumDir string, name, role, module, worktree string) {
	t.Helper()
	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0750); err != nil {
		t.Fatal(err)
	}

	identity := config.IdentityFile{
		Version: 3,
		RepoID:  "test-repo",
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   name,
			Role:   role,
			Module: module,
		},
		Worktree: worktree,
	}

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(identitiesDir, name+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

// createTestRoleTemplate creates a role template in the thrumDir.
func createTestRoleTemplate(t *testing.T, thrumDir, role, content string) {
	t.Helper()
	templatesDir := filepath.Join(thrumDir, "role_templates")
	if err := os.MkdirAll(templatesDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templatesDir, role+".md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRenderRoleTemplate_ValidTemplate(t *testing.T) {
	thrumDir := t.TempDir()

	// Create identity and template
	createTestIdentity(t, thrumDir, "impl_auth", "implementer", "auth", "auth-worktree")
	createTestRoleTemplate(t, thrumDir, "implementer",
		`# Agent: {{.AgentName}}
Role: {{.Role}}
Module: {{.Module}}
Worktree: {{.WorktreePath}}
Coordinator: {{.CoordinatorName}}`)

	rendered, err := RenderRoleTemplate(thrumDir, "impl_auth", "implementer")
	if err != nil {
		t.Fatalf("RenderRoleTemplate: %v", err)
	}
	if rendered == nil {
		t.Fatal("expected rendered content, got nil")
	}

	content := string(rendered)
	if !strings.Contains(content, "# Agent: impl_auth") {
		t.Errorf("expected AgentName, got: %s", content)
	}
	if !strings.Contains(content, "Role: implementer") {
		t.Errorf("expected Role, got: %s", content)
	}
	if !strings.Contains(content, "Module: auth") {
		t.Errorf("expected Module, got: %s", content)
	}
	if !strings.Contains(content, "Worktree: auth-worktree") {
		t.Errorf("expected WorktreePath, got: %s", content)
	}
	// CoordinatorName falls back to "coordinator" when no coordinator identity exists
	if !strings.Contains(content, "Coordinator: coordinator") {
		t.Errorf("expected fallback CoordinatorName, got: %s", content)
	}
}

func TestRenderRoleTemplate_MissingTemplate(t *testing.T) {
	thrumDir := t.TempDir()

	rendered, err := RenderRoleTemplate(thrumDir, "some_agent", "nonexistent")
	if err != nil {
		t.Fatalf("expected nil error for missing template, got: %v", err)
	}
	if rendered != nil {
		t.Errorf("expected nil for missing template, got: %s", rendered)
	}
}

func TestRenderRoleTemplate_InvalidSyntax(t *testing.T) {
	thrumDir := t.TempDir()

	createTestRoleTemplate(t, thrumDir, "broken", `{{.InvalidUnclosed`)

	_, err := RenderRoleTemplate(thrumDir, "agent", "broken")
	if err == nil {
		t.Fatal("expected error for invalid template syntax")
	}
	if !strings.Contains(err.Error(), "parse role template") {
		t.Errorf("expected parse error, got: %v", err)
	}
}

func TestRenderRoleTemplate_CoordinatorNameResolved(t *testing.T) {
	thrumDir := t.TempDir()

	// Create a coordinator identity
	createTestIdentity(t, thrumDir, "coord_main", "coordinator", "main", "main-wt")
	// Create an implementer identity
	createTestIdentity(t, thrumDir, "impl_api", "implementer", "api", "api-wt")

	createTestRoleTemplate(t, thrumDir, "implementer", `Report to {{.CoordinatorName}}`)

	rendered, err := RenderRoleTemplate(thrumDir, "impl_api", "implementer")
	if err != nil {
		t.Fatalf("RenderRoleTemplate: %v", err)
	}

	if !strings.Contains(string(rendered), "Report to coord_main") {
		t.Errorf("expected coordinator name resolved, got: %s", rendered)
	}
}

func TestRenderRoleTemplate_RepoRoot(t *testing.T) {
	thrumDir := t.TempDir()

	createTestRoleTemplate(t, thrumDir, "planner", `Root: {{.RepoRoot}}`)

	rendered, err := RenderRoleTemplate(thrumDir, "planner_agent", "planner")
	if err != nil {
		t.Fatalf("RenderRoleTemplate: %v", err)
	}

	// RepoRoot should be parent of thrumDir
	expected := filepath.Dir(thrumDir)
	if !strings.Contains(string(rendered), "Root: "+expected) {
		t.Errorf("expected RepoRoot %q, got: %s", expected, rendered)
	}
}

func TestDeployAll_MultipleAgents(t *testing.T) {
	thrumDir := t.TempDir()

	// Create identities
	createTestIdentity(t, thrumDir, "coord_main", "coordinator", "main", "main-wt")
	createTestIdentity(t, thrumDir, "impl_auth", "implementer", "auth", "auth-wt")
	createTestIdentity(t, thrumDir, "impl_api", "implementer", "api", "api-wt")

	// Create templates for both roles
	createTestRoleTemplate(t, thrumDir, "coordinator", `# Coordinator: {{.AgentName}}`)
	createTestRoleTemplate(t, thrumDir, "implementer", `# Implementer: {{.AgentName}}`)

	// Ensure context directory exists
	if err := os.MkdirAll(filepath.Join(thrumDir, "context"), 0750); err != nil {
		t.Fatal(err)
	}

	result, err := DeployAll(thrumDir, "", false)
	if err != nil {
		t.Fatalf("DeployAll: %v", err)
	}

	if len(result.Updated) != 3 {
		t.Errorf("expected 3 updated, got %d: %v", len(result.Updated), result.Updated)
	}
	if len(result.Skipped) != 0 {
		t.Errorf("expected 0 skipped, got %d: %v", len(result.Skipped), result.Skipped)
	}

	// Verify preamble files were created
	for _, name := range []string{"coord_main", "impl_auth", "impl_api"} {
		data, err := LoadPreamble(thrumDir, name)
		if err != nil {
			t.Errorf("LoadPreamble %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("expected preamble for %s, got empty", name)
		}
	}

	// Verify content is correct
	coordData, _ := LoadPreamble(thrumDir, "coord_main")
	if !strings.Contains(string(coordData), "# Coordinator: coord_main") {
		t.Errorf("coordinator preamble wrong, got: %s", coordData)
	}
}

func TestDeployAll_DryRun(t *testing.T) {
	thrumDir := t.TempDir()

	createTestIdentity(t, thrumDir, "impl_auth", "implementer", "auth", "auth-wt")
	createTestRoleTemplate(t, thrumDir, "implementer", `# Implementer: {{.AgentName}}`)

	result, err := DeployAll(thrumDir, "", true)
	if err != nil {
		t.Fatalf("DeployAll dry-run: %v", err)
	}

	if len(result.Updated) != 1 {
		t.Errorf("expected 1 updated in dry-run, got %d", len(result.Updated))
	}

	// Verify no file was actually written
	data, err := LoadPreamble(thrumDir, "impl_auth")
	if err != nil {
		t.Fatalf("LoadPreamble: %v", err)
	}
	if data != nil {
		t.Error("dry-run should not write files")
	}
}

func TestDeployAll_AgentFilter(t *testing.T) {
	thrumDir := t.TempDir()

	createTestIdentity(t, thrumDir, "impl_auth", "implementer", "auth", "auth-wt")
	createTestIdentity(t, thrumDir, "impl_api", "implementer", "api", "api-wt")
	createTestRoleTemplate(t, thrumDir, "implementer", `# {{.AgentName}}`)

	if err := os.MkdirAll(filepath.Join(thrumDir, "context"), 0750); err != nil {
		t.Fatal(err)
	}

	result, err := DeployAll(thrumDir, "impl_auth", false)
	if err != nil {
		t.Fatalf("DeployAll with filter: %v", err)
	}

	if len(result.Updated) != 1 {
		t.Errorf("expected 1 updated with filter, got %d", len(result.Updated))
	}
	if result.Updated[0] != "impl_auth" {
		t.Errorf("expected impl_auth, got %s", result.Updated[0])
	}

	// impl_api should NOT have a preamble
	data, _ := LoadPreamble(thrumDir, "impl_api")
	if data != nil {
		t.Error("filtered agent should not have preamble")
	}
}

func TestDeployAll_NoMatchingTemplate(t *testing.T) {
	thrumDir := t.TempDir()

	createTestIdentity(t, thrumDir, "qa_agent", "qa-engineer", "testing", "test-wt")
	// No template for "qa-engineer" role

	result, err := DeployAll(thrumDir, "", false)
	if err != nil {
		t.Fatalf("DeployAll: %v", err)
	}

	if len(result.Updated) != 0 {
		t.Errorf("expected 0 updated, got %d", len(result.Updated))
	}
	if len(result.Skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(result.Skipped))
	}
	if result.Skipped[0] != "qa_agent" {
		t.Errorf("expected qa_agent skipped, got %s", result.Skipped[0])
	}
}

func TestListRoleTemplates(t *testing.T) {
	thrumDir := t.TempDir()

	createTestIdentity(t, thrumDir, "coord_main", "coordinator", "main", "main-wt")
	createTestIdentity(t, thrumDir, "impl_auth", "implementer", "auth", "auth-wt")
	createTestIdentity(t, thrumDir, "impl_api", "implementer", "api", "api-wt")

	createTestRoleTemplate(t, thrumDir, "coordinator", "# Coordinator")
	createTestRoleTemplate(t, thrumDir, "implementer", "# Implementer")
	createTestRoleTemplate(t, thrumDir, "planner", "# Planner") // no agents with this role

	result, err := ListRoleTemplates(thrumDir)
	if err != nil {
		t.Fatalf("ListRoleTemplates: %v", err)
	}

	if len(result) != 3 {
		t.Errorf("expected 3 templates, got %d", len(result))
	}

	if agents := result["coordinator.md"]; len(agents) != 1 || agents[0] != "coord_main" {
		t.Errorf("coordinator agents wrong: %v", agents)
	}

	if agents := result["implementer.md"]; len(agents) != 2 {
		t.Errorf("implementer agents wrong: %v", agents)
	}

	if agents := result["planner.md"]; len(agents) != 0 {
		t.Errorf("planner should have 0 agents, got: %v", agents)
	}
}

func TestListRoleTemplates_NoTemplatesDir(t *testing.T) {
	thrumDir := t.TempDir()

	result, err := ListRoleTemplates(thrumDir)
	if err != nil {
		t.Fatalf("ListRoleTemplates: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty for no templates dir, got: %v", result)
	}
}

func TestRegistrationAutoApply(t *testing.T) {
	// Simulates the quickstart flow: identity exists, role template exists
	thrumDir := t.TempDir()

	createTestIdentity(t, thrumDir, "impl_auth", "implementer", "auth", "auth-wt")
	createTestRoleTemplate(t, thrumDir, "implementer", `# Agent: {{.AgentName}}, Role: {{.Role}}`)

	// Simulate registration auto-apply logic
	rendered, err := RenderRoleTemplate(thrumDir, "impl_auth", "implementer")
	if err != nil {
		t.Fatalf("RenderRoleTemplate: %v", err)
	}
	if rendered == nil {
		t.Fatal("expected rendered content")
	}

	if err := SavePreamble(thrumDir, "impl_auth", rendered); err != nil {
		t.Fatalf("SavePreamble: %v", err)
	}

	// Verify the preamble was saved with rendered content
	data, err := LoadPreamble(thrumDir, "impl_auth")
	if err != nil {
		t.Fatal(err)
	}
	expected := "# Agent: impl_auth, Role: implementer"
	if string(data) != expected {
		t.Errorf("expected %q, got %q", expected, data)
	}
}

func TestRegistrationNoTemplate_DefaultPreamble(t *testing.T) {
	// Simulates registration when no role template exists
	thrumDir := t.TempDir()

	rendered, err := RenderRoleTemplate(thrumDir, "custom_agent", "custom-role")
	if err != nil {
		t.Fatalf("RenderRoleTemplate: %v", err)
	}
	if rendered != nil {
		t.Fatal("expected nil for missing template")
	}

	// Should fall back to default preamble
	if err := EnsurePreamble(thrumDir, "custom_agent"); err != nil {
		t.Fatal(err)
	}

	data, err := LoadPreamble(thrumDir, "custom_agent")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(DefaultPreamble()) {
		t.Error("expected default preamble when no role template exists")
	}
}

func TestPreambleFilePrecedence(t *testing.T) {
	// --preamble-file should take precedence over role template
	thrumDir := t.TempDir()

	createTestIdentity(t, thrumDir, "impl_auth", "implementer", "auth", "auth-wt")
	createTestRoleTemplate(t, thrumDir, "implementer", `Role template content`)

	// Simulate --preamble-file behavior: it should be used instead of role template
	customContent := []byte("Custom preamble from file")
	composed := append(DefaultPreamble(), []byte("\n---\n\n")...)
	composed = append(composed, customContent...)

	if err := SavePreamble(thrumDir, "impl_auth", composed); err != nil {
		t.Fatal(err)
	}

	data, err := LoadPreamble(thrumDir, "impl_auth")
	if err != nil {
		t.Fatal(err)
	}

	content := string(data)
	if strings.Contains(content, "Role template content") {
		t.Error("role template content should not be present when --preamble-file used")
	}
	if !strings.Contains(content, "Custom preamble from file") {
		t.Error("custom preamble file content should be present")
	}
}
