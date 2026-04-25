package roleconfig

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderShipped_Coordinator(t *testing.T) {
	out, err := RenderShipped("coordinator", "autonomous", "cross_worktree", RenderEnv{
		AgentName: "coord_main",
		Module:    "main",
		RepoRoot:  "/tmp/test-repo",
	})
	if err != nil {
		t.Fatalf("RenderShipped: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("rendered output empty")
	}
}

func TestRenderShipped_StripsFrontmatter(t *testing.T) {
	out, err := RenderShipped("coordinator", "autonomous", "cross_worktree", RenderEnv{})
	if err != nil {
		t.Fatalf("RenderShipped: %v", err)
	}
	if bytes.HasPrefix(out, []byte("---\n")) {
		t.Errorf("frontmatter not stripped; output starts with:\n%.120s", out)
	}
	if strings.Contains(string(out[:min(80, len(out))]), "schema_version") {
		t.Errorf("frontmatter remnants present:\n%s", out[:min(120, len(out))])
	}
}

// TestRenderShipped_PreservesAgentNameToken locks Anti-Pattern #2: refresh
// must leave per-agent template tokens literal so the downstream deploy
// pass can substitute them per-agent. Substituting at refresh time kills
// per-agent fidelity.
func TestRenderShipped_PreservesAgentNameToken(t *testing.T) {
	out, err := RenderShipped("coordinator", "autonomous", "cross_worktree", RenderEnv{
		AgentName: "should-not-appear",
	})
	if err != nil {
		t.Fatalf("RenderShipped: %v", err)
	}
	if !bytes.Contains(out, []byte("{{.AgentName}}")) {
		t.Errorf("refresh substituted {{.AgentName}} — must remain literal until deploy")
	}
	if bytes.Contains(out, []byte("should-not-appear")) {
		t.Errorf("refresh leaked RenderEnv value into output")
	}
}

func TestRenderShipped_OrchestratorFallback(t *testing.T) {
	out, err := RenderShipped("orchestrator", "strict", "single_worktree", RenderEnv{})
	if err != nil {
		t.Fatalf("RenderShipped(orchestrator,strict): %v", err)
	}
	if len(out) == 0 {
		t.Fatal("orchestrator fallback returned empty body")
	}
}

func TestRenderShipped_UnknownRole(t *testing.T) {
	_, err := RenderShipped("nonexistent", "autonomous", "scope", RenderEnv{})
	if err == nil {
		t.Error("expected error for unknown role")
	}
}

