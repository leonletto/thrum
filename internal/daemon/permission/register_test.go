package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestResolveProjectName_PreferredConfigField(t *testing.T) {
	cfg := &config.ThrumConfig{ProjectName: "awesome-thrum"}
	got := ResolveProjectName(cfg, "/tmp/some-path")
	if got != "awesome-thrum" {
		t.Errorf("got %q, want awesome-thrum", got)
	}
}

func TestResolveProjectName_FallbackToBasename(t *testing.T) {
	cfg := &config.ThrumConfig{}
	got := ResolveProjectName(cfg, "/Users/leon/dev/opensource/thrum")
	if got != "thrum" {
		t.Errorf("got %q, want thrum", got)
	}
}

func TestResolveProjectName_NilConfigFallsBack(t *testing.T) {
	got := ResolveProjectName(nil, "/Users/leon/dev/opensource/thrum")
	if got != "thrum" {
		t.Errorf("got %q, want thrum", got)
	}
}

func TestResolveProjectName_UltimateFallback(t *testing.T) {
	if got := ResolveProjectName(nil, ""); got != "project" {
		t.Errorf("got %q, want project", got)
	}
	if got := ResolveProjectName(nil, "/"); got != "project" {
		t.Errorf("got %q for /, want project", got)
	}
	if got := ResolveProjectName(nil, "."); got != "project" {
		t.Errorf("got %q for ., want project", got)
	}
}

func TestSupervisorAgentID(t *testing.T) {
	if got := SupervisorAgentID("thrum"); got != "supervisor_thrum" {
		t.Errorf("got %q, want supervisor_thrum", got)
	}
}

func TestRegisterSupervisor_WritesIdentityFile(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	cfg := &config.ThrumConfig{ProjectName: "thrum"}

	id, err := RegisterSupervisor(context.Background(), cfg, thrumDir, "")
	if err != nil {
		t.Fatalf("RegisterSupervisor: %v", err)
	}
	if id != "supervisor_thrum" {
		t.Errorf("agent_id = %q, want supervisor_thrum", id)
	}

	// Verify the file exists at the expected path with the expected fields.
	// Parse the JSON directly rather than going through LoadIdentityWithPath —
	// that function honors THRUM_HOME, which the ambient agent session sets
	// and which would otherwise redirect the test away from tmp.
	idPath := filepath.Join(thrumDir, "identities", "supervisor_thrum.json")
	data, err := os.ReadFile(idPath) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("read identity file: %v", err)
	}
	var idFile config.IdentityFile
	if err := json.Unmarshal(data, &idFile); err != nil {
		t.Fatalf("parse identity file: %v", err)
	}
	if idFile.Agent.Name != "supervisor_thrum" {
		t.Errorf("Agent.Name = %q, want supervisor_thrum", idFile.Agent.Name)
	}
	if !idFile.Reserved {
		t.Error("Reserved should be true")
	}
	if idFile.Agent.Role != "supervisor" {
		t.Errorf("Agent.Role = %q, want supervisor", idFile.Agent.Role)
	}
	if idFile.Agent.Kind != "agent" {
		t.Errorf("Agent.Kind = %q, want agent", idFile.Agent.Kind)
	}
	if idFile.Agent.Module != "daemon" {
		t.Errorf("Agent.Module = %q, want daemon", idFile.Agent.Module)
	}
}

func TestRegisterSupervisor_FallbackToBasename(t *testing.T) {
	tmp := t.TempDir()
	// Mimic a repo named after the tmp dir's basename.
	thrumDir := filepath.Join(tmp, ".thrum")
	// With nil ProjectName, ResolveProjectName uses filepath.Base(repoPath).
	cfg := &config.ThrumConfig{}

	id, err := RegisterSupervisor(context.Background(), cfg, thrumDir, "/workspace/my-project")
	if err != nil {
		t.Fatalf("RegisterSupervisor: %v", err)
	}
	if id != "supervisor_my-project" {
		t.Errorf("agent_id = %q, want supervisor_my-project", id)
	}
	idPath := filepath.Join(thrumDir, "identities", "supervisor_my-project.json")
	if _, err := os.Stat(idPath); err != nil {
		t.Fatalf("identity file not at %s: %v", idPath, err)
	}
}

func TestRegisterSupervisor_Idempotent(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	cfg := &config.ThrumConfig{ProjectName: "thrum"}

	id1, err := RegisterSupervisor(context.Background(), cfg, thrumDir, "")
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	id2, err := RegisterSupervisor(context.Background(), cfg, thrumDir, "")
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if id1 != id2 {
		t.Errorf("id drift: %q vs %q", id1, id2)
	}
	// Only one identity file should exist.
	entries, err := os.ReadDir(filepath.Join(thrumDir, "identities"))
	if err != nil {
		t.Fatalf("read identities: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected 1 identity file, got %d: %v", len(entries), names)
	}
}
