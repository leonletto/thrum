package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/rpc"
)

// fakePreambleClient captures preamble.save requests for assertion.
type fakePreambleClient struct {
	captured []byte
	called   string
}

func (f *fakePreambleClient) Call(method string, params, result any) error {
	f.called = method
	if method == "context.preamble.save" {
		req, ok := params.(rpc.PreambleSaveRequest)
		if !ok {
			return nil
		}
		f.captured = append([]byte(nil), req.Content...)
	}
	return nil
}

// setupTempRepoWithRoleTemplate creates a fake repo with a .thrum dir,
// minimal identity for the agent, and a role template containing the
// given marker text. Returns the absolute repo path.
func setupTempRepoWithRoleTemplate(t *testing.T, agentName, role, templateBody string) string {
	t.Helper()
	repo := t.TempDir()
	thrumDir := filepath.Join(repo, ".thrum")

	identitiesDir := filepath.Join(thrumDir, "identities")
	if err := os.MkdirAll(identitiesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	id := config.IdentityFile{
		Version: 3,
		RepoID:  "test-repo",
		Agent: config.AgentConfig{
			Kind: "agent",
			Name: agentName,
			Role: role,
		},
		Worktree: repo,
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(identitiesDir, agentName+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	templatesDir := filepath.Join(thrumDir, "role_templates")
	if err := os.MkdirAll(templatesDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templatesDir, role+".md"), []byte(templateBody), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

// TestPreambleInit_UsesRoleTemplateWhenPresent verifies that
// `thrum context preamble --init` consults RenderRoleTemplate before falling
// back to the generic RoleAwarePreamble. Without this, customized templates
// at .thrum/role_templates/<role>.md are silently overwritten.
//
// Regression spec: thrum-pk2o.
func TestPreambleInit_UsesRoleTemplateWhenPresent(t *testing.T) {
	const marker = "## Custom Coordinator Discipline"
	repo := setupTempRepoWithRoleTemplate(t, "coord_main", "coordinator", marker+"\n\nProject-specific guidance.\n")

	fc := &fakePreambleClient{}
	if err := runPreambleInit(fc, "coord_main", "coordinator", repo, "coord_main"); err != nil {
		t.Fatalf("runPreambleInit: %v", err)
	}

	if fc.called != "context.preamble.save" {
		t.Fatalf("expected preamble.save call, got %q", fc.called)
	}
	if !bytes.Contains(fc.captured, []byte(marker)) {
		t.Fatalf("preamble does not contain role-template marker %q\n--- got ---\n%s", marker, fc.captured)
	}
}

// TestPreambleInit_FallsBackWhenNoRoleTemplate confirms the fallback path:
// when no .thrum/role_templates/<role>.md exists, the generic
// RoleAwarePreambleWithRoot is sent — and the substituted strategies/
// llms.txt paths are ABSOLUTE (rooted at the agent's repo path) so
// worktree agents can Read them directly without traversing
// .thrum/redirect. Pins the thrum-rm4x fix.
func TestPreambleInit_FallsBackWhenNoRoleTemplate(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".thrum"), 0o750); err != nil {
		t.Fatal(err)
	}

	fc := &fakePreambleClient{}
	if err := runPreambleInit(fc, "impl_x", "implementer", repo, "impl_x"); err != nil {
		t.Fatalf("runPreambleInit: %v", err)
	}

	if len(fc.captured) == 0 {
		t.Fatalf("expected fallback content, got empty")
	}
	// The generic RoleAwarePreamble for implementer must include the
	// shared DefaultPreamble reference content.
	if !bytes.Contains(fc.captured, []byte("Thrum Quick Reference")) {
		t.Fatalf("fallback content missing DefaultPreamble shared section:\n%s", fc.captured)
	}

	// thrum-rm4x: strategies + llms.txt paths must be absolute,
	// rooted at the agent's repo path (the runPreambleInit caller's
	// repoPath argument). Without this, worktree-side agents that
	// fall through to this path can't Read the strategies files
	// without first resolving .thrum/redirect.
	for _, needle := range []string{
		filepath.Join(repo, ".thrum/strategies/sub-agent-strategy.md"),
		filepath.Join(repo, ".thrum/strategies/thrum-registration.md"),
		filepath.Join(repo, ".thrum/strategies/resume-after-context-loss.md"),
		filepath.Join(repo, ".thrum/llms.txt"),
	} {
		if !bytes.Contains(fc.captured, []byte(needle)) {
			t.Errorf("fallback preamble missing absolute path %q\n--- got ---\n%s", needle, fc.captured)
		}
	}
	// And the relative-path bullet forms should be absent — they'd
	// mislead a worktree agent into Reading paths that don't exist
	// on its side of the redirect.
	for _, relative := range []string{
		"`.thrum/strategies/sub-agent-strategy.md`",
		"`.thrum/llms.txt`",
	} {
		if bytes.Contains(fc.captured, []byte(relative)) {
			t.Errorf("fallback preamble should not contain relative bullet %q", relative)
		}
	}
}
