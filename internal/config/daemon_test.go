package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestLoadThrumConfig_NoFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=false when no config file exists")
	}
	// Defaults should be applied; retention/compaction defaults are
	// exercised in T-config-2 / T-config-3.
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		t.Errorf("expected WSPort=%q, got %q", config.DefaultWSPort, cfg.Daemon.WSPort)
	}
	if cfg.Runtime.Primary != "" {
		t.Errorf("expected empty Runtime.Primary, got %q", cfg.Runtime.Primary)
	}
}

func TestLoadThrumConfig_LocalOnlyTrue(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"local_only":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true")
	}
}

func TestLoadThrumConfig_LocalOnlyFalse(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"local_only":false}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=false")
	}
}

func TestLoadThrumConfig_EmptyJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=false for empty config")
	}
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		t.Errorf("expected WSPort=%q, got %q", config.DefaultWSPort, cfg.Daemon.WSPort)
	}
}

func TestLoadThrumConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{invalid`), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := config.LoadThrumConfig(tmpDir)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoadThrumConfig_FullSchema(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	// sync_interval is silently ignored as of v0.10.6 (spec §7.2); verify
	// the config still loads cleanly when legacy configs carry the key.
	data := `{
		"runtime": {"primary": "claude"},
		"daemon": {
			"local_only": true,
			"sync_interval": 30,
			"ws_port": "9999"
		}
	}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Runtime.Primary != "claude" {
		t.Errorf("expected Runtime.Primary=claude, got %q", cfg.Runtime.Primary)
	}
	if !cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true")
	}
	if cfg.Daemon.WSPort != "9999" {
		t.Errorf("expected WSPort=9999, got %q", cfg.Daemon.WSPort)
	}
}

func TestLoadThrumConfig_BackwardsCompat(t *testing.T) {
	// Old config with only daemon.local_only should still work
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"local_only":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true")
	}
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		t.Errorf("expected default WSPort, got %q", cfg.Daemon.WSPort)
	}
	if cfg.Runtime.Primary != "" {
		t.Errorf("expected empty Runtime.Primary, got %q", cfg.Runtime.Primary)
	}
}

// TestLoadThrumConfig_LegacySyncIntervalSilentlyIgnored covers T-config-1
// from the thrum-s6os plan §11 E9 acceptance block. Pre-v0.10.6 user
// configs frequently carry `daemon.sync_interval`; in v0.10.6 the field
// is removed from DaemonConfig and the JSON key is silently dropped by
// json.Unmarshal (unknown fields are ignored). Legacy configs must
// continue to load without error (spec §7.2).
func TestLoadThrumConfig_LegacySyncIntervalSilentlyIgnored(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	// 6000s would have been a meaningful (overly-long) interval pre-rearch
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"sync_interval":6000}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("legacy config with sync_interval should load without error, got: %v", err)
	}
	// The key is silently ignored; other defaults still apply.
	if cfg.Daemon.WSPort != config.DefaultWSPort {
		t.Errorf("expected default WSPort after legacy config load, got %q", cfg.Daemon.WSPort)
	}
}

// TestLoadThrumConfig_EventsRetentionDaysDefault covers T-config-2.
// Absent from JSON, the field must default to DefaultEventsRetentionDays (2).
func TestLoadThrumConfig_EventsRetentionDaysDefault(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.EventsRetentionDays != config.DefaultEventsRetentionDays {
		t.Errorf("expected EventsRetentionDays=%d, got %d",
			config.DefaultEventsRetentionDays, cfg.Daemon.EventsRetentionDays)
	}
}

// TestLoadThrumConfig_EventsRetentionDays_UserValueOverridesDefault confirms
// applyDefaults does not stomp a user-supplied value.
func TestLoadThrumConfig_EventsRetentionDays_UserValueOverridesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"events_retention_days":7}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.EventsRetentionDays != 7 {
		t.Errorf("expected user-supplied EventsRetentionDays=7, got %d", cfg.Daemon.EventsRetentionDays)
	}
}

// TestLoadThrumConfig_CompactionSizeThresholdMBDefault covers T-config-3.
// Absent from JSON, the field must default to
// DefaultCompactionSizeThresholdMB (10).
func TestLoadThrumConfig_CompactionSizeThresholdMBDefault(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.CompactionSizeThresholdMB != config.DefaultCompactionSizeThresholdMB {
		t.Errorf("expected CompactionSizeThresholdMB=%d, got %d",
			config.DefaultCompactionSizeThresholdMB, cfg.Daemon.CompactionSizeThresholdMB)
	}
}

// TestLoadThrumConfig_CompactionSizeThresholdMB_UserValueOverridesDefault
// confirms applyDefaults does not stomp a user-supplied value.
func TestLoadThrumConfig_CompactionSizeThresholdMB_UserValueOverridesDefault(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"compaction_size_threshold_mb":25}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.CompactionSizeThresholdMB != 25 {
		t.Errorf("expected user-supplied CompactionSizeThresholdMB=25, got %d", cfg.Daemon.CompactionSizeThresholdMB)
	}
}

func TestSaveThrumConfig_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read back and verify
	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if !loaded.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true after save")
	}
}

func TestSaveThrumConfig_WithRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{
		Runtime: config.RuntimeConfig{Primary: "claude"},
		Daemon:  config.DaemonConfig{LocalOnly: true, WSPort: "9999"},
	}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if loaded.Runtime.Primary != "claude" {
		t.Errorf("expected Runtime.Primary=claude, got %q", loaded.Runtime.Primary)
	}
	if loaded.Daemon.WSPort != "9999" {
		t.Errorf("expected WSPort=9999, got %q", loaded.Daemon.WSPort)
	}
}

func TestSaveThrumConfig_OmitsEmptyRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read raw file — should not have a "runtime" key
	data, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["runtime"]; exists {
		t.Error("expected runtime key to be omitted when Primary is empty")
	}
}

func TestLoadThrumConfig_BackupDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Backup.Dir != "" {
		t.Errorf("expected empty backup dir, got %q", cfg.Backup.Dir)
	}
	if cfg.Backup.Retention.RetentionDaily() != config.DefaultRetentionDaily {
		t.Errorf("expected Daily=%d, got %d", config.DefaultRetentionDaily, cfg.Backup.Retention.RetentionDaily())
	}
	if cfg.Backup.Retention.RetentionWeekly() != config.DefaultRetentionWeekly {
		t.Errorf("expected Weekly=%d, got %d", config.DefaultRetentionWeekly, cfg.Backup.Retention.RetentionWeekly())
	}
	if cfg.Backup.Retention.RetentionMonthly() != config.DefaultRetentionMonthly {
		t.Errorf("expected Monthly=%d, got %d", config.DefaultRetentionMonthly, cfg.Backup.Retention.RetentionMonthly())
	}
	if len(cfg.Backup.Plugins) != 0 {
		t.Errorf("expected no plugins, got %d", len(cfg.Backup.Plugins))
	}
}

func TestLoadThrumConfig_BackupFromJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	data := `{
		"backup": {
			"dir": "/tmp/backups",
			"retention": {"daily": 3, "weekly": 2, "monthly": 6},
			"plugins": [
				{"name": "beads", "command": "bd backup --force", "include": [".beads/backup/*"]}
			],
			"post_backup": "echo done"
		}
	}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Backup.Dir != "/tmp/backups" {
		t.Errorf("expected dir=/tmp/backups, got %q", cfg.Backup.Dir)
	}
	if cfg.Backup.Retention.RetentionDaily() != 3 {
		t.Errorf("expected Daily=3, got %d", cfg.Backup.Retention.RetentionDaily())
	}
	if cfg.Backup.Retention.RetentionWeekly() != 2 {
		t.Errorf("expected Weekly=2, got %d", cfg.Backup.Retention.RetentionWeekly())
	}
	if cfg.Backup.Retention.RetentionMonthly() != 6 {
		t.Errorf("expected Monthly=6, got %d", cfg.Backup.Retention.RetentionMonthly())
	}
	if len(cfg.Backup.Plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(cfg.Backup.Plugins))
	}
	p := cfg.Backup.Plugins[0]
	if p.Name != "beads" || p.Command != "bd backup --force" {
		t.Errorf("unexpected plugin: %+v", p)
	}
	if len(p.Include) != 1 || p.Include[0] != ".beads/backup/*" {
		t.Errorf("unexpected plugin include: %v", p.Include)
	}
	if cfg.Backup.PostBackup != "echo done" {
		t.Errorf("expected post_backup='echo done', got %q", cfg.Backup.PostBackup)
	}
}

func TestSaveThrumConfig_BackupSection(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{
		Daemon: config.DaemonConfig{LocalOnly: true},
		Backup: config.BackupConfig{
			Dir: "/custom/backup",
			Retention: config.RetentionConfig{
				Daily:   config.IntPtr(3),
				Weekly:  config.IntPtr(2),
				Monthly: config.IntPtr(12),
			},
			Plugins: []config.PluginConfig{
				{Name: "beads", Command: "bd backup", Include: []string{".beads/*"}},
			},
			PostBackup: "sync.sh",
		},
	}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}
	if loaded.Backup.Dir != "/custom/backup" {
		t.Errorf("expected backup dir=/custom/backup, got %q", loaded.Backup.Dir)
	}
	if loaded.Backup.Retention.RetentionDaily() != 3 {
		t.Errorf("expected Daily=3, got %d", loaded.Backup.Retention.RetentionDaily())
	}
	if len(loaded.Backup.Plugins) != 1 {
		t.Errorf("expected 1 plugin, got %d", len(loaded.Backup.Plugins))
	}
	if loaded.Backup.PostBackup != "sync.sh" {
		t.Errorf("expected post_backup=sync.sh, got %q", loaded.Backup.PostBackup)
	}
}

func TestSaveThrumConfig_OmitsDefaultBackup(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read raw file — should not have a "backup" key when all defaults
	data, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["backup"]; exists {
		t.Error("expected backup key to be omitted when all defaults")
	}
}

func TestSaveThrumConfig_BackupScheduleRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{
		Daemon: config.DaemonConfig{LocalOnly: true},
		Backup: config.BackupConfig{
			Schedule: "24h",
			Dir:      "/tmp/scheduled-backups",
		},
	}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Backup.Schedule != "24h" {
		t.Errorf("expected schedule=24h, got %q", loaded.Backup.Schedule)
	}
	if loaded.Backup.Dir != "/tmp/scheduled-backups" {
		t.Errorf("expected dir=/tmp/scheduled-backups, got %q", loaded.Backup.Dir)
	}

	// Verify schedule alone triggers backup section in JSON
	data, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["backup"]; !exists {
		t.Error("expected backup section to be written when schedule is set")
	}
}

func TestLoadThrumConfig_ScheduleFromJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	data := `{"backup": {"schedule": "12h"}}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Backup.Schedule != "12h" {
		t.Errorf("expected schedule=12h, got %q", cfg.Backup.Schedule)
	}
}

func TestSingleAgentModeConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Write config with single_agent_mode
	cfgJSON := `{"daemon":{"single_agent_mode":true,"local_only":true}}`
	if err := os.WriteFile(configPath, []byte(cfgJSON), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.Daemon.SingleAgentMode {
		t.Error("expected SingleAgentMode=true")
	}
	if !cfg.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true")
	}
}

func TestSingleAgentModeConfig_DefaultFalse(t *testing.T) {
	tmpDir := t.TempDir()
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Daemon.SingleAgentMode {
		t.Error("expected SingleAgentMode=false when no config file exists")
	}
}

func TestSingleAgentModeConfig_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{
		Daemon: config.DaemonConfig{
			LocalOnly:       true,
			SingleAgentMode: true,
		},
	}
	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !loaded.Daemon.SingleAgentMode {
		t.Error("expected SingleAgentMode=true after round-trip")
	}
}

// TestSingleAgentModeConfig_LoadModifyOtherSavePreserves locks the
// load-modify-save invariant that init code paths must preserve
// SingleAgentMode when only modifying unrelated fields.
//
// Regression test for the upgrade footgun: `thrum init --force` re-runs
// after an upgrade and used to destructively overwrite SingleAgentMode
// to true, silently breaking messaging for any user who had previously
// set it to false (or omitted it entirely). The fix removed the
// destructive assignment; this test pins that the surface contract
// is now: callers must not touch SingleAgentMode unless that's
// explicitly the field they're changing.
func TestSingleAgentModeConfig_LoadModifyOtherSavePreserves(t *testing.T) {
	tmpDir := t.TempDir()

	// Case A: existing config has single_agent_mode:false → must stay false
	// after a load + unrelated-field modification + save cycle.
	cfgJSON := `{"daemon":{"single_agent_mode":false,"local_only":true}}`
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(cfgJSON), 0600); err != nil {
		t.Fatal(err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Daemon.SingleAgentMode {
		t.Fatal("setup: expected SingleAgentMode=false from initial load")
	}

	// Simulate the init code path: modify an unrelated field, then save.
	// The fix removed the line that destructively set SingleAgentMode=true here.
	loaded.Runtime.Primary = "claude"

	if err := config.SaveThrumConfig(tmpDir, loaded); err != nil {
		t.Fatalf("save: %v", err)
	}

	reloaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Daemon.SingleAgentMode {
		t.Error("SingleAgentMode flipped to true after load+unrelated-modify+save (upgrade footgun)")
	}

	// Case B: fresh install (no config file) → modify + save must
	// produce single_agent_mode:false (zero value), not true.
	freshDir := t.TempDir()
	freshCfg, err := config.LoadThrumConfig(freshDir)
	if err != nil {
		t.Fatalf("load fresh: %v", err)
	}
	freshCfg.Runtime.Primary = "claude"
	if err := config.SaveThrumConfig(freshDir, freshCfg); err != nil {
		t.Fatalf("save fresh: %v", err)
	}
	reloadFresh, err := config.LoadThrumConfig(freshDir)
	if err != nil {
		t.Fatalf("reload fresh: %v", err)
	}
	if reloadFresh.Daemon.SingleAgentMode {
		t.Error("SingleAgentMode true on fresh install — should default false")
	}
}

func TestTelegramConfig_FindGroup(t *testing.T) {
	cfg := config.TelegramConfig{
		Groups: []config.TelegramGroup{
			{ChatID: -100123, Name: "cross-repo", TrustedBots: []int64{111, 222}},
			{ChatID: -100456, Name: "other-group", TrustedBots: []int64{333}},
		},
	}
	g := cfg.FindGroup(-100123)
	if g == nil || g.Name != "cross-repo" {
		t.Errorf("FindGroup(-100123) = %v, want cross-repo", g)
	}
	if cfg.FindGroup(-999) != nil {
		t.Error("FindGroup(-999) should return nil")
	}
}

func TestTelegramConfig_IsTrustedBot(t *testing.T) {
	cfg := config.TelegramConfig{
		Groups: []config.TelegramGroup{
			{ChatID: -100123, Name: "cross-repo", TrustedBots: []int64{111, 222}},
		},
	}
	if !cfg.IsTrustedBot(-100123, 111) {
		t.Error("bot 111 should be trusted in group -100123")
	}
	if cfg.IsTrustedBot(-100123, 999) {
		t.Error("bot 999 should not be trusted")
	}
	if cfg.IsTrustedBot(-999, 111) {
		t.Error("bot 111 should not be trusted in unknown group")
	}
}

func TestTelegramConfig_GroupNames(t *testing.T) {
	cfg := config.TelegramConfig{
		Groups: []config.TelegramGroup{
			{ChatID: -100123, Name: "cross-repo"},
			{ChatID: -100456, Name: "other"},
		},
	}
	names := cfg.GroupNames()
	if len(names) != 2 || names[0] != "cross-repo" {
		t.Errorf("GroupNames() = %v, want [cross-repo, other]", names)
	}
}

func TestSaveThrumConfig_PersistsGroups(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	os.MkdirAll(thrumDir, 0o750)

	cfg := config.ThrumConfig{
		Telegram: config.TelegramConfig{
			Token: "test-token",
			Groups: []config.TelegramGroup{
				{ChatID: -100123, Name: "cross-repo", TrustedBots: []int64{111},
					RemoteAgents: []config.RemoteAgent{{Name: "coord", Prefix: "falcon", Bot: "@falcon_bot"}}},
			},
		},
	}

	err := config.SaveThrumConfig(thrumDir, &cfg)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if len(loaded.Telegram.Groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(loaded.Telegram.Groups))
	}
	g := loaded.Telegram.Groups[0]
	if g.Name != "cross-repo" || g.ChatID != -100123 {
		t.Errorf("group mismatch: %+v", g)
	}
	if len(g.RemoteAgents) != 1 || g.RemoteAgents[0].Prefix != "falcon" {
		t.Errorf("remote agents mismatch: %+v", g.RemoteAgents)
	}
}

func TestSaveThrumConfig_PreservesUnknownKeys(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Write a config with an extra top-level key
	if err := os.WriteFile(configPath, []byte(`{"custom":"keep_me","daemon":{"local_only":false}}`), 0600); err != nil {
		t.Fatal(err)
	}

	// Save with local_only=true
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}
	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Read raw file and check "custom" key survived
	data, err := os.ReadFile(configPath) //nolint:gosec // test file, path from t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "keep_me") {
		t.Errorf("unknown key was lost after save, got:\n%s", content)
	}

	// Verify daemon section was updated
	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Daemon.LocalOnly {
		t.Error("expected LocalOnly=true after save")
	}
}

func TestSaveThrumConfig_RestartRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{
		Restart: config.RestartConfig{
			MaxLines:        500,
			AutoThreshold:   80,
			GracefulTimeout: 45,
		},
	}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Restart.MaxLines != 500 {
		t.Errorf("expected MaxLines=500, got %d", loaded.Restart.MaxLines)
	}
	if loaded.Restart.AutoThreshold != 80 {
		t.Errorf("expected AutoThreshold=80, got %d", loaded.Restart.AutoThreshold)
	}
	if loaded.Restart.GracefulTimeout != 45 {
		t.Errorf("expected GracefulTimeout=45, got %d", loaded.Restart.GracefulTimeout)
	}
}

func TestSaveThrumConfig_OmitsDefaultRestart(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["restart"]; exists {
		t.Error("restart section should not be written when all fields are zero")
	}
}

func TestThrumConfig_PermissionSupervisors_Default(t *testing.T) {
	var cfg config.ThrumConfig
	raw := []byte(`{}`)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg.PermissionSupervisors != nil {
		t.Errorf("PermissionSupervisors should be nil when absent, got %v", cfg.PermissionSupervisors)
	}
	if cfg.ProjectName != "" {
		t.Errorf("ProjectName should be empty when absent, got %q", cfg.ProjectName)
	}
}

func TestThrumConfig_PermissionSupervisors_Roundtrip(t *testing.T) {
	in := config.ThrumConfig{
		PermissionSupervisors: []string{"coordinator", "@user:leon-letto"},
		ProjectName:           "thrum",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out config.ThrumConfig
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.PermissionSupervisors) != 2 {
		t.Errorf("expected 2 supervisors, got %d", len(out.PermissionSupervisors))
	}
	if out.PermissionSupervisors[0] != "coordinator" || out.PermissionSupervisors[1] != "@user:leon-letto" {
		t.Errorf("unexpected supervisors: %v", out.PermissionSupervisors)
	}
	if out.ProjectName != "thrum" {
		t.Errorf("ProjectName = %q, want %q", out.ProjectName, "thrum")
	}
}

func TestSaveThrumConfig_OmitsEmptyPermissionFields(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{}
	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(tmpDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["permission_supervisors"]; exists {
		t.Error("permission_supervisors should be omitted when nil")
	}
	if _, exists := raw["project_name"]; exists {
		t.Error("project_name should be omitted when empty")
	}
}

func TestThrumConfig_IdentityRoundTrip(t *testing.T) {
	original := config.ThrumConfig{
		Identity: config.IdentityConfig{
			DaemonID:     "d_01HYC7K9ABCDEFGHJKMNPQRSTV",
			RepoName:     "thrum",
			Hostname:     "leonsmacm1pro",
			RepoPath:     "/Users/leon/dev/opensource/thrum",
			GitOriginURL: "https://github.com/leonletto/thrum",
			InitAt:       "2026-04-17T05:30:00Z",
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round config.ThrumConfig
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round.Identity != original.Identity {
		t.Fatalf("identity mismatch:\n  got  = %+v\n  want = %+v", round.Identity, original.Identity)
	}
}

func TestValidatePermissionSupervisors(t *testing.T) {
	tests := []struct {
		name    string
		entries []string
		wantOK  bool
	}{
		{"empty list is valid (resolver defaults to coordinator)", nil, true},
		{"zero-length list is valid", []string{}, true},
		{"bare coordinator role is valid", []string{"coordinator"}, true},
		{"coordinator alongside user is valid", []string{"coordinator", "@user:leon-letto"}, true},
		{"@coordinator_main named agent is valid", []string{"@coordinator_main"}, true},
		{"@coordinator-main hyphen variant is valid", []string{"@coordinator-main"}, true},
		{"mixed entries with a coordinator are valid", []string{"@user:leon-letto", "@coordinator_main"}, true},
		{"only user (no coordinator) is invalid", []string{"@user:leon-letto"}, false},
		{"only non-coordinator agent is invalid", []string{"@impl_x"}, false},
		{"orchestrator role alone is invalid", []string{"orchestrator"}, false},
		{"multiple non-coordinator entries are invalid", []string{"@impl_x", "@user:leon-letto"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := config.ValidatePermissionSupervisors(tt.entries)
			ok := got == ""
			if ok != tt.wantOK {
				t.Errorf("ValidatePermissionSupervisors(%v): got warning=%q, wantOK=%v",
					tt.entries, got, tt.wantOK)
			}
		})
	}
}

// TestDaemonConfig_MaxMessageBodyBytesEffective — thrum-mhwt: 0 →
// package default; positive → caller value; negative → caller value
// (call-site interprets negative as "disabled" by gating on > 0).
func TestDaemonConfig_MaxMessageBodyBytesEffective(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want int
	}{
		{"zero_returns_default", 0, config.DefaultMaxMessageBodyBytes},
		{"positive_returns_self", 2 * 1024 * 1024, 2 * 1024 * 1024},
		{"negative_returns_self", -1, -1},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			d := config.DaemonConfig{MaxMessageBodyBytes: tt.in}
			if got := d.MaxMessageBodyBytesEffective(); got != tt.want {
				t.Errorf("MaxMessageBodyBytesEffective() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNudgeConfig_SilenceGate(t *testing.T) {
	cases := []struct {
		name        string
		cfg         config.NudgeConfig
		wantSil     int
		wantDl      int
		wantEnabled bool
	}{
		{"zero-uses-defaults", config.NudgeConfig{}, 10, 60, true},
		{"explicit-values", config.NudgeConfig{ChromeQuietSeconds: 5, DispatchDeadlineSeconds: 30}, 5, 30, true},
		{"negative-silence-disables", config.NudgeConfig{ChromeQuietSeconds: -1}, 0, 0, false},
		{"negative-deadline-disables", config.NudgeConfig{DispatchDeadlineSeconds: -1}, 0, 0, false},
		{"silence-set-deadline-default", config.NudgeConfig{ChromeQuietSeconds: 8}, 8, 60, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sil, dl, enabled := tc.cfg.SilenceGate()
			if sil != tc.wantSil || dl != tc.wantDl || enabled != tc.wantEnabled {
				t.Fatalf("SilenceGate() = (%d, %d, %v), want (%d, %d, %v)",
					sil, dl, enabled, tc.wantSil, tc.wantDl, tc.wantEnabled)
			}
		})
	}
}

// ── A3 / B4+B5 back-port: SyncConfig, validateSync, migrateLegacySync ─────────

// syncHasMechanism returns true when cfg.Daemon.Sync.Mechanisms contains an
// entry with the given mechanism name (any scope).
func syncHasMechanism(cfg config.ThrumConfig, mech string) bool {
	if cfg.Daemon.Sync == nil {
		return false
	}
	for _, m := range cfg.Daemon.Sync.Mechanisms {
		if m.Mechanism == mech {
			return true
		}
	}
	return false
}

// syncHasMechanismScope returns true when cfg contains an entry matching both
// mechanism and scope.
func syncHasMechanismScope(cfg config.ThrumConfig, mech, scope string) bool {
	if cfg.Daemon.Sync == nil {
		return false
	}
	for _, m := range cfg.Daemon.Sync.Mechanisms {
		if m.Mechanism == mech && m.Scope == scope {
			return true
		}
	}
	return false
}

func TestValidateSync(t *testing.T) {
	cases := []struct {
		name    string
		mechs   []config.SyncMechanism
		wantErr string
	}{
		{"a-sync directed rejected", []config.SyncMechanism{{Mechanism: "a-sync", Scope: "directed"}}, "only valid for peer/email"},
		{"unknown mechanism directed rejected", []config.SyncMechanism{{Mechanism: "carrier-pigeon", Scope: "directed"}}, "only valid for peer/email"},
		{"directed alongside full rejected", []config.SyncMechanism{{Mechanism: "a-sync", Scope: "full"}, {Mechanism: "peer", Scope: "directed"}}, "directed cannot coexist"},
		{"peer directed ok", []config.SyncMechanism{{Mechanism: "peer", Scope: "directed"}}, ""},
		{"email directed ok", []config.SyncMechanism{{Mechanism: "email", Scope: "directed"}}, ""},
		{"a-sync full ok", []config.SyncMechanism{{Mechanism: "a-sync", Scope: "full"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := config.ValidateSync(&config.SyncConfig{Enabled: true, Mechanisms: tc.mechs})
			if tc.wantErr == "" && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("want err %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestMigrateLegacySync(t *testing.T) {
	// legacy local_only=true gated only a-sync → a-sync removed, peer kept implicitly
	got := config.MigrateLegacySync(config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}})
	if syncHasMechanism(got, "a-sync") {
		t.Fatal("local_only=true must map to a-sync OFF")
	}
	if !got.Daemon.Sync.Enabled {
		t.Fatal("local_only=true must NOT blanket-disable sync")
	}
	// legacy false (today's real default) → a-sync full present
	got = config.MigrateLegacySync(config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: false}})
	if !syncHasMechanismScope(got, "a-sync", "full") {
		t.Fatal("local_only=false must map to a-sync(full)")
	}
}

func TestApplyDefaults_FreshInstallIsASyncFull(t *testing.T) {
	cfg := config.ThrumConfig{} // no sync stanza, no legacy bool set
	config.ApplyDefaultsForTest(&cfg)
	if !syncHasMechanismScope(cfg, "a-sync", "full") || !cfg.Daemon.Sync.Enabled {
		t.Fatalf("fresh default must be sync:on a-sync(full), got %+v", cfg.Daemon.Sync)
	}
}

func TestValidateSync_DisabledSkipsValidation(t *testing.T) {
	// A disabled sync with bad mechanisms must be accepted (enabled=false short-circuits)
	err := config.ValidateSync(&config.SyncConfig{
		Enabled:    false,
		Mechanisms: []config.SyncMechanism{{Mechanism: "a-sync", Scope: "directed"}},
	})
	if err != nil {
		t.Fatalf("disabled sync must bypass validation, got %v", err)
	}
	// nil (absent stanza, pre-default) must also pass validation.
	if err := config.ValidateSync(nil); err != nil {
		t.Fatalf("nil sync must bypass validation, got %v", err)
	}
}

func TestLoadThrumConfig_RejectsBadSyncCombo(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	// a-sync + directed is invalid (a-sync is full-only)
	data := `{"daemon":{"sync":{"enabled":true,"mechanisms":[{"mechanism":"a-sync","scope":"directed"}]}}}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := config.LoadThrumConfig(tmpDir)
	if err == nil {
		t.Fatal("expected error for a-sync+directed combo, got nil")
	}
	if !strings.Contains(err.Error(), "only valid for peer/email") {
		t.Fatalf("expected 'only valid for peer/email' in error, got %v", err)
	}
}

// TestLoadThrumConfig_ExplicitSyncDisabledIsPreserved guards the D7/D9
// no-silent-flip invariant: an explicitly present sync stanza with
// enabled:false must NOT be migrated back to enabled:true. With Sync as a
// *SyncConfig, a present stanza makes the pointer non-nil so MigrateLegacySync
// preserves it; an absent stanza leaves it nil so migration applies the default.
func TestLoadThrumConfig_ExplicitSyncDisabledIsPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	data := `{"daemon":{"sync":{"enabled":false}}}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Daemon.Sync == nil || cfg.Daemon.Sync.Enabled {
		t.Fatal("explicit sync.enabled=false must be preserved, not migrated back to enabled:true")
	}
}

// TestLoadThrumConfig_FreshInstallSyncOnViaLoad is BLOCKING regression #1: a
// freshly-inited node (config.json with NO sync stanza) must load with sync ON
// and a-sync(full) — the fresh-init pairing fix. The nil pointer (absent stanza)
// triggers MigrateLegacySync to allocate the D9 default.
func TestLoadThrumConfig_FreshInstallSyncOnViaLoad(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	// A fresh init writes daemon settings but no sync stanza.
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"ws_port":"auto"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Daemon.Sync == nil || !cfg.Daemon.Sync.Enabled {
		t.Fatalf("fresh install must load with sync enabled, got %+v", cfg.Daemon.Sync)
	}
	if !syncHasMechanismScope(*cfg, "a-sync", "full") {
		t.Fatalf("fresh install must default to a-sync(full), got %+v", cfg.Daemon.Sync)
	}
}

// TestSaveThrumConfig_HandEditedDisableSurvivesUnrelatedSave is BLOCKING
// regression #2 (no-flip across save): a hand-edited sync:{enabled:false} must
// survive an unrelated SaveThrumConfig + reload, NOT flip back on. With the
// pointer this holds because the explicit &{false} marshals back as a present
// stanza (omitempty only omits nil), so reload sees it non-nil and preserves it.
func TestSaveThrumConfig_HandEditedDisableSurvivesUnrelatedSave(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"sync":{"enabled":false}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Daemon.Sync == nil || cfg.Daemon.Sync.Enabled {
		t.Fatalf("precondition: explicit disable must load as disabled, got %+v", cfg.Daemon.Sync)
	}
	// Make an unrelated change and save (simulates any unrelated command).
	cfg.Daemon.LogLevel = "debug"
	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Daemon.Sync == nil || reloaded.Daemon.Sync.Enabled {
		t.Fatalf("hand-edited sync.enabled=false must survive an unrelated save (no silent flip), got %+v", reloaded.Daemon.Sync)
	}
}

func TestSyncConfig_ExposureFields_RoundTrip(t *testing.T) {
	in := config.SyncConfig{
		Enabled:                true,
		DetectedVisibility:     "private",
		DetectedRemote:         "github.com/owner/repo",
		PublicExposureOverride: "github.com/owner/repo",
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out config.SyncConfig
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.DetectedVisibility != "private" || out.DetectedRemote != "github.com/owner/repo" ||
		out.PublicExposureOverride != "github.com/owner/repo" {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
}
