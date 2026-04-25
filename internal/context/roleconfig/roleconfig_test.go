package roleconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_AbsentReturnsNilNoError(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"),
		[]byte(`{"daemon": {"log_level": "info"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(thrumDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil cfg when role_config absent, got %+v", cfg)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	in := &RoleConfig{
		SchemaVersion: 1,
		PluginVersion: "0.9.2",
		ConfiguredAt:  time.Date(2026, 4, 25, 15, 0, 0, 0, time.UTC),
		Roles: map[string]RoleSettings{
			"coordinator": {Autonomy: "autonomous", Scope: "cross_worktree", RenderedHash: "abc123"},
			"implementer": {Autonomy: "strict", Scope: "worktree_only", RenderedHash: "def456"},
		},
	}
	if err := Save(thrumDir, in); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out, err := Load(thrumDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out == nil {
		t.Fatal("Load returned nil after Save")
	}
	if out.SchemaVersion != 1 || out.PluginVersion != "0.9.2" {
		t.Errorf("scalar fields not preserved: %+v", out)
	}
	if !out.ConfiguredAt.Equal(in.ConfiguredAt) {
		t.Errorf("ConfiguredAt not preserved: got %v, want %v", out.ConfiguredAt, in.ConfiguredAt)
	}
	if len(out.Roles) != 2 {
		t.Errorf("expected 2 roles, got %d", len(out.Roles))
	}
	if out.Roles["coordinator"].RenderedHash != "abc123" {
		t.Errorf("RenderedHash not preserved (coordinator)")
	}
	if out.Roles["implementer"].Autonomy != "strict" {
		t.Errorf("implementer.Autonomy not preserved")
	}
}

// TestSave_PreservesUnknownTopLevelKeys is the load-bearing guarantee: writing
// role_config MUST NOT touch backup/daemon/identity/telegram or any other
// top-level key. We round-trip via map[string]json.RawMessage so unknown keys
// pass through byte-identically.
func TestSave_PreservesUnknownTopLevelKeys(t *testing.T) {
	thrumDir := t.TempDir()
	original := `{
  "backup": {"dir": "dev-docs/backup"},
  "daemon": {"log_level": "info"},
  "identity": {"daemon_id": "d_01ABC"},
  "telegram": {"token": "secret-do-not-touch"}
}`
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &RoleConfig{
		SchemaVersion: 1,
		PluginVersion: "0.9.2",
		ConfiguredAt:  time.Now(),
		Roles:         map[string]RoleSettings{"coordinator": {Autonomy: "autonomous", Scope: "cross_worktree"}},
	}
	if err := Save(thrumDir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(thrumDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("re-parse: %v", err)
	}

	for _, key := range []string{"backup", "daemon", "identity", "telegram"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("top-level key %q lost on save", key)
		}
	}
	if _, ok := parsed["role_config"]; !ok {
		t.Errorf("role_config not written")
	}

	var tg map[string]any
	if err := json.Unmarshal(parsed["telegram"], &tg); err != nil {
		t.Fatalf("re-parse telegram: %v", err)
	}
	if tg["token"] != "secret-do-not-touch" {
		t.Errorf("telegram.token corrupted: %v", tg["token"])
	}
}

// TestSave_AtomicWrite confirms the temp+rename pattern leaves no .tmp
// artifact after success — required so a partial write on crash never
// truncates the shared config.json.
func TestSave_AtomicWrite(t *testing.T) {
	thrumDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"),
		[]byte(`{"daemon":{"log_level":"info"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &RoleConfig{SchemaVersion: 1, Roles: map[string]RoleSettings{}}
	if err := Save(thrumDir, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(thrumDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

func TestLoad_MissingConfigFile(t *testing.T) {
	thrumDir := t.TempDir()
	_, err := Load(thrumDir)
	if err == nil {
		t.Fatal("expected error when config.json missing, got nil")
	}
}
