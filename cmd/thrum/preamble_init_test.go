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

// TestPreambleInit_WorktreeWithRedirect pins the thrum-5hhx fix: when
// runPreambleInit is invoked from a worktree (.thrum carries a
// `redirect` file pointing at the main repo's .thrum/), the rendered
// strategies/llms.txt paths must point at the MAIN repo, not the
// worktree itself. Without the redirect-follow, the fallback path
// would substitute the worktree's repoPath and produce paths that
// don't resolve (worktree .thrum carries only redirect + identities/
// + context/ + restart/, never strategies/ or llms.txt).
func TestPreambleInit_WorktreeWithRedirect(t *testing.T) {
	mainRepo := t.TempDir()
	mainThrum := filepath.Join(mainRepo, ".thrum")
	if err := os.MkdirAll(filepath.Join(mainThrum, "strategies"), 0o750); err != nil {
		t.Fatal(err)
	}
	// Placeholder files so a future caller could actually Read them;
	// runPreambleInit doesn't open them, but creating them mirrors
	// the real-world shape and guards against any future "stat the
	// path before substituting" check.
	for _, name := range []string{
		"sub-agent-strategy.md",
		"thrum-registration.md",
		"resume-after-context-loss.md",
	} {
		if err := os.WriteFile(filepath.Join(mainThrum, "strategies", name), []byte("placeholder\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(mainThrum, "llms.txt"), []byte("placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Worktree-shape .thrum at a separate path with only a redirect
	// file pointing at the main repo's .thrum.
	worktree := t.TempDir()
	worktreeThrum := filepath.Join(worktree, ".thrum")
	if err := os.MkdirAll(worktreeThrum, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeThrum, "redirect"), []byte(mainThrum), 0o600); err != nil {
		t.Fatal(err)
	}

	fc := &fakePreambleClient{}
	if err := runPreambleInit(fc, "wt_impl", "implementer", worktree, "wt_impl"); err != nil {
		t.Fatalf("runPreambleInit: %v", err)
	}

	if len(fc.captured) == 0 {
		t.Fatalf("expected fallback content, got empty")
	}

	// Strategies/llms.txt paths must resolve under the MAIN repo, not
	// the worktree. We use HasPrefix-style filepath joins so the
	// macOS /private/var symlink resolution doesn't break the match
	// (t.TempDir() returns a /private/var-prefixed path on macOS).
	for _, needle := range []string{
		filepath.Join(mainRepo, ".thrum/strategies/sub-agent-strategy.md"),
		filepath.Join(mainRepo, ".thrum/strategies/thrum-registration.md"),
		filepath.Join(mainRepo, ".thrum/strategies/resume-after-context-loss.md"),
		filepath.Join(mainRepo, ".thrum/llms.txt"),
	} {
		if !bytes.Contains(fc.captured, []byte(needle)) {
			t.Errorf("rendered preamble missing main-repo absolute path %q\n--- got ---\n%s", needle, fc.captured)
		}
	}

	// Worktree-rooted strategies paths must NOT appear (they don't
	// exist on disk and would mislead a fast-reading agent).
	for _, antiNeedle := range []string{
		filepath.Join(worktree, ".thrum/strategies/sub-agent-strategy.md"),
		filepath.Join(worktree, ".thrum/llms.txt"),
	} {
		if bytes.Contains(fc.captured, []byte(antiNeedle)) {
			t.Errorf("rendered preamble contains worktree-rooted path %q (should be main-repo path)", antiNeedle)
		}
	}
}
