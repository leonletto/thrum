package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/context/roleconfig"
)

func TestRolesRefresh_FailsLoudWhenNoRoleConfig(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runRolesRefresh(thrumDir)
	if err == nil {
		t.Fatal("expected error when role_config absent")
	}
	if !strings.Contains(err.Error(), "configure-roles") {
		t.Errorf("error should mention configure-roles, got: %v", err)
	}
}

// TestRolesRefresh_RegeneratesAndUpdatesRenderedHash exercises the round-trip:
// stale rendered_hash → run refresh → rendered file written, hash updated to
// match shipped, and per-agent template tokens preserved verbatim (Anti-
// Pattern #2: substituting at refresh kills per-agent fidelity).
func TestRolesRefresh_RegeneratesAndUpdatesRenderedHash(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &roleconfig.RoleConfig{
		SchemaVersion: 1,
		PluginVersion: "0.9.0",
		Roles: map[string]roleconfig.RoleSettings{
			"coordinator": {
				Autonomy:     "autonomous",
				Scope:        "cross_worktree",
				RenderedHash: "stale-hash",
			},
		},
	}
	if err := roleconfig.Save(thrumDir, cfg); err != nil {
		t.Fatal(err)
	}

	if err := runRolesRefresh(thrumDir); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	rendered, err := os.ReadFile(filepath.Join(thrumDir, "role_templates", "coordinator.md"))
	if err != nil {
		t.Fatalf("rendered file missing: %v", err)
	}
	if len(rendered) == 0 {
		t.Fatal("rendered file empty")
	}

	after, err := roleconfig.Load(thrumDir)
	if err != nil {
		t.Fatalf("Load after refresh: %v", err)
	}
	_, shippedHash, err := roleconfig.ShippedTemplateInfo("coordinator", "autonomous")
	if err != nil {
		t.Fatalf("ShippedTemplateInfo: %v", err)
	}
	if after.Roles["coordinator"].RenderedHash != shippedHash {
		t.Errorf("rendered_hash not updated: got %q, want %q",
			after.Roles["coordinator"].RenderedHash, shippedHash)
	}

	if !bytes.Contains(rendered, []byte("{{.AgentName}}")) {
		t.Errorf("refresh substituted {{.AgentName}} — must remain literal until deploy")
	}
}

// TestRolesRefresh_Idempotent confirms two consecutive refreshes against
// unchanged shipped templates yield byte-identical rendered output.
func TestRolesRefresh_Idempotent(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &roleconfig.RoleConfig{
		SchemaVersion: 1,
		PluginVersion: "0.9.0",
		Roles: map[string]roleconfig.RoleSettings{
			"implementer": {Autonomy: "strict", Scope: "single_worktree"},
		},
	}
	if err := roleconfig.Save(thrumDir, cfg); err != nil {
		t.Fatal(err)
	}

	if err := runRolesRefresh(thrumDir); err != nil {
		t.Fatalf("refresh 1: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(thrumDir, "role_templates", "implementer.md"))
	if err != nil {
		t.Fatal(err)
	}

	if err := runRolesRefresh(thrumDir); err != nil {
		t.Fatalf("refresh 2: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(thrumDir, "role_templates", "implementer.md"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("refresh not idempotent: rendered output changed between runs")
	}
}
