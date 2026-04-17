package identity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type bootstrapTestConfig struct {
	Identity struct {
		DaemonID     string `json:"daemon_id"`
		RepoName     string `json:"repo_name"`
		Hostname     string `json:"hostname"`
		RepoPath     string `json:"repo_path"`
		GitOriginURL string `json:"git_origin_url"`
		InitAt       string `json:"init_at"`
	} `json:"identity"`
}

func loadBootstrapConfig(t *testing.T, path string) bootstrapTestConfig {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var c bootstrapTestConfig
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return c
}

func TestBootstrap_FreshRepo(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id, err := Bootstrap(thrumDir, tmp)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if id.DaemonID == "" {
		t.Fatalf("DaemonID empty")
	}
	if id.RepoPath != tmp {
		t.Fatalf("RepoPath = %q, want %q", id.RepoPath, tmp)
	}
	if id.InitAt == "" {
		t.Fatalf("InitAt empty")
	}

	cfg := loadBootstrapConfig(t, filepath.Join(thrumDir, "config.json"))
	if cfg.Identity.DaemonID != id.DaemonID {
		t.Fatalf("persisted daemon_id %q != returned %q", cfg.Identity.DaemonID, id.DaemonID)
	}
}

func TestBootstrap_StableAcrossCalls(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	first, err := Bootstrap(thrumDir, tmp)
	if err != nil {
		t.Fatalf("Bootstrap 1: %v", err)
	}
	second, err := Bootstrap(thrumDir, tmp)
	if err != nil {
		t.Fatalf("Bootstrap 2: %v", err)
	}
	if first.DaemonID != second.DaemonID {
		t.Fatalf("daemon_id changed between calls: %q vs %q", first.DaemonID, second.DaemonID)
	}
	if first.InitAt != second.InitAt {
		t.Fatalf("init_at changed between calls: %q vs %q", first.InitAt, second.InitAt)
	}
}

func TestBootstrap_LegacyIDRotates(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	host, _ := os.Hostname()
	legacy := legacyHostnameDerivedID(host)
	seed := `{"identity":{"daemon_id":"` + legacy + `","init_at":"2026-01-01T00:00:00Z"}}`
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	id, err := Bootstrap(thrumDir, tmp)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if id.DaemonID == legacy {
		t.Fatalf("daemon_id not rotated from legacy %q", legacy)
	}
	if id.DaemonID == "" {
		t.Fatalf("rotated DaemonID empty")
	}
	if id.InitAt == "2026-01-01T00:00:00Z" {
		t.Fatalf("init_at not updated on rotation; still %q", id.InitAt)
	}
}

func TestBootstrap_FreshULIDNotRotated(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	preId := GenerateDaemonID()
	seed := `{"identity":{"daemon_id":"` + preId + `","init_at":"2026-01-01T00:00:00Z"}}`
	_ = os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(seed), 0o600)

	id, err := Bootstrap(thrumDir, tmp)
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if id.DaemonID != preId {
		t.Fatalf("existing ULID rotated unexpectedly: %q -> %q", preId, id.DaemonID)
	}
}

func TestBootstrap_BackupCreatedOnRotation(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	host, _ := os.Hostname()
	legacy := legacyHostnameDerivedID(host)
	seed := `{"identity":{"daemon_id":"` + legacy + `","init_at":"2026-01-01T00:00:00Z"}}`
	cfgPath := filepath.Join(thrumDir, "config.json")
	bakPath := cfgPath + ".pre-identity-bak"
	if err := os.WriteFile(cfgPath, []byte(seed), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// First Bootstrap: triggers rotation — backup should be created.
	if _, err := Bootstrap(thrumDir, tmp); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	bakData, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	// Backup must contain the pre-rotation (legacy) daemon_id.
	if !strings.Contains(string(bakData), legacy) {
		t.Fatalf("backup does not contain legacy daemon_id %q: %s", legacy, bakData)
	}

	// Second Bootstrap: backup must NOT be overwritten.
	if _, err := Bootstrap(thrumDir, tmp); err != nil {
		t.Fatalf("Bootstrap second call: %v", err)
	}
	bakData2, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("backup file disappeared after second Bootstrap: %v", err)
	}
	if string(bakData2) != string(bakData) {
		t.Fatalf("backup overwritten on second Bootstrap; want pre-rotation bytes unchanged")
	}
}

func TestBootstrap_NoBackupWhenStable(t *testing.T) {
	tmp := t.TempDir()
	thrumDir := filepath.Join(tmp, ".thrum")
	_ = os.MkdirAll(thrumDir, 0o750)

	preId := GenerateDaemonID()
	seed := `{"identity":{"daemon_id":"` + preId + `","init_at":"2026-01-01T00:00:00Z"}}`
	_ = os.WriteFile(filepath.Join(thrumDir, "config.json"), []byte(seed), 0o600)

	if _, err := Bootstrap(thrumDir, tmp); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	bakPath := filepath.Join(thrumDir, "config.json.pre-identity-bak")
	if _, err := os.Stat(bakPath); err == nil {
		t.Fatalf("backup should not be created when daemon_id is stable ULID")
	}
}

