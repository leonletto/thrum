package roleconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeRoleConfigJSON writes a config.json with the given role_config block.
func writeRoleConfigJSON(t *testing.T, thrumDir string, cfg *RoleConfig) {
	t.Helper()
	top := map[string]any{}
	if cfg != nil {
		raw, err := json.MarshalIndent(cfg, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		top["role_config"] = json.RawMessage(raw)
	}
	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDriftStatus_FullyCurrentNoHints(t *testing.T) {
	thrumDir := t.TempDir()
	_, shippedHash, err := ShippedTemplateInfo("coordinator", "autonomous")
	if err != nil {
		t.Fatalf("ShippedTemplateInfo: %v", err)
	}
	writeRoleConfigJSON(t, thrumDir, &RoleConfig{
		SchemaVersion: 1,
		PluginVersion: "0.9.2",
		ConfiguredAt:  time.Now(),
		Roles: map[string]RoleSettings{
			"coordinator": {Autonomy: "autonomous", Scope: "cross_worktree", RenderedHash: shippedHash},
		},
	})

	report, err := DriftStatus(thrumDir)
	if err != nil {
		t.Fatalf("DriftStatus: %v", err)
	}
	if len(report.Hints) != 0 {
		t.Errorf("expected 0 hints, got %d: %+v", len(report.Hints), report.Hints)
	}
}

func TestDriftStatus_MigrationHint_RoleTemplatesPresentNoConfig(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(thrumDir, "role_templates"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "role_templates", "coordinator.md"), []byte("# coord\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := DriftStatus(thrumDir)
	if err != nil {
		t.Fatalf("DriftStatus: %v", err)
	}
	if len(report.Hints) != 1 || report.Hints[0].Code != HintCodeRolesConfigMigration {
		t.Fatalf("expected single migration hint, got %+v", report.Hints)
	}
}

func TestDriftStatus_FreshRepoNoHints(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := DriftStatus(thrumDir)
	if err != nil {
		t.Fatalf("DriftStatus: %v", err)
	}
	if len(report.Hints) != 0 {
		t.Errorf("fresh repo should have no hints, got %+v", report.Hints)
	}
}

func TestDriftStatus_SchemaBumpHint(t *testing.T) {
	thrumDir := t.TempDir()
	// Saved schema is 0; shipped templates declare schema_version 1.
	writeRoleConfigJSON(t, thrumDir, &RoleConfig{
		SchemaVersion: 0,
		PluginVersion: "0.9.0",
		Roles: map[string]RoleSettings{
			"coordinator": {Autonomy: "autonomous", Scope: "cross_worktree", RenderedHash: "stale"},
		},
	})

	report, err := DriftStatus(thrumDir)
	if err != nil {
		t.Fatalf("DriftStatus: %v", err)
	}
	if len(report.Hints) != 1 || report.Hints[0].Code != HintCodeRolesConfigSchemaBump {
		t.Fatalf("expected schema-bump hint, got %+v", report.Hints)
	}
}

func TestDriftStatus_BodyDiffHint(t *testing.T) {
	thrumDir := t.TempDir()
	// Schema matches shipped (1) but rendered_hash doesn't.
	writeRoleConfigJSON(t, thrumDir, &RoleConfig{
		SchemaVersion: 1,
		PluginVersion: "0.9.1",
		Roles: map[string]RoleSettings{
			"coordinator": {Autonomy: "autonomous", Scope: "cross_worktree", RenderedHash: "deadbeef-not-the-shipped-hash"},
		},
	})

	report, err := DriftStatus(thrumDir)
	if err != nil {
		t.Fatalf("DriftStatus: %v", err)
	}
	if len(report.Hints) != 1 || report.Hints[0].Code != HintCodeRolesConfigBodyDiff {
		t.Fatalf("expected body-diff hint, got %+v", report.Hints)
	}
}

// TestDriftStatus_PrecedenceMigrationOverSchemaBump locks in that when the
// migration condition holds, no schema/body hints fire (early return).
func TestDriftStatus_PrecedenceMigrationOverSchemaBump(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(thrumDir, "role_templates"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "role_templates", "coordinator.md"), []byte("# coord\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := DriftStatus(thrumDir)
	if err != nil {
		t.Fatalf("DriftStatus: %v", err)
	}
	for _, h := range report.Hints {
		if h.Code != HintCodeRolesConfigMigration {
			t.Errorf("unexpected non-migration hint when migration condition holds: %v", h)
		}
	}
}
