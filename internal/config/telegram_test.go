package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestTelegramConfig_Enabled(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.TelegramConfig
		want    bool
	}{
		{"empty token", config.TelegramConfig{}, false},
		{"token set", config.TelegramConfig{Token: "123:AAH"}, true},
		{"token set, explicitly enabled", config.TelegramConfig{Token: "123:AAH", Enabled: config.BoolPtr(true)}, true},
		{"token set, explicitly disabled", config.TelegramConfig{Token: "123:AAH", Enabled: config.BoolPtr(false)}, false},
		{"no token, explicitly enabled", config.TelegramConfig{Enabled: config.BoolPtr(true)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.TelegramEnabled(); got != tt.want {
				t.Errorf("TelegramEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTelegramConfig_AllowFrom_FailClosed(t *testing.T) {
	// Empty AllowFrom with AllowAll=false should block all
	cfg := config.TelegramConfig{
		Token:    "123:AAH",
		AllowAll: false,
	}
	if cfg.IsAllowed(12345) {
		t.Error("empty AllowFrom with AllowAll=false should block all")
	}
}

func TestTelegramConfig_AllowFrom_AllowAll(t *testing.T) {
	cfg := config.TelegramConfig{
		Token:    "123:AAH",
		AllowAll: true,
	}
	if !cfg.IsAllowed(12345) {
		t.Error("AllowAll=true should allow any user")
	}
}

func TestTelegramConfig_AllowFrom_Explicit(t *testing.T) {
	cfg := config.TelegramConfig{
		Token:     "123:AAH",
		AllowFrom: []int64{111, 222},
	}
	if !cfg.IsAllowed(111) {
		t.Error("user 111 should be allowed")
	}
	if !cfg.IsAllowed(222) {
		t.Error("user 222 should be allowed")
	}
	if cfg.IsAllowed(333) {
		t.Error("user 333 should be blocked")
	}
}

func TestLoadThrumConfig_TelegramSection(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	data := `{
		"telegram": {
			"token": "123456789:AAHxxxxxxx",
			"target": "@coordinator_main",
			"user_id": "leon-letto",
			"chat_id": -100123456,
			"allow_from": [111, 222],
			"allow_all": false
		}
	}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "123456789:AAHxxxxxxx" {
		t.Errorf("expected token, got %q", cfg.Telegram.Token)
	}
	if cfg.Telegram.Target != "@coordinator_main" {
		t.Errorf("expected target=@coordinator_main, got %q", cfg.Telegram.Target)
	}
	if cfg.Telegram.UserID != "leon-letto" {
		t.Errorf("expected user_id=leon-letto, got %q", cfg.Telegram.UserID)
	}
	if cfg.Telegram.ChatID != -100123456 {
		t.Errorf("expected chat_id=-100123456, got %d", cfg.Telegram.ChatID)
	}
	if len(cfg.Telegram.AllowFrom) != 2 || cfg.Telegram.AllowFrom[0] != 111 {
		t.Errorf("expected allow_from=[111,222], got %v", cfg.Telegram.AllowFrom)
	}
	if cfg.Telegram.AllowAll {
		t.Error("expected allow_all=false")
	}
}

func TestLoadThrumConfig_NoTelegramSection(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"daemon":{"local_only":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Telegram.Token != "" {
		t.Errorf("expected empty telegram token, got %q", cfg.Telegram.Token)
	}
	if cfg.Telegram.TelegramEnabled() {
		t.Error("expected telegram disabled when no section present")
	}
}

func TestSaveThrumConfig_TelegramSection(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{
		Daemon: config.DaemonConfig{LocalOnly: true},
		Telegram: config.TelegramConfig{
			Token:     "123456789:AAHxxxxxxx",
			Target:    "@coordinator_main",
			UserID:    "leon-letto",
			ChatID:    -100123456,
			AllowFrom: []int64{111, 222},
		},
	}

	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := config.LoadThrumConfig(tmpDir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Telegram.Token != "123456789:AAHxxxxxxx" {
		t.Errorf("expected token round-trip, got %q", loaded.Telegram.Token)
	}
	if loaded.Telegram.Target != "@coordinator_main" {
		t.Errorf("expected target round-trip, got %q", loaded.Telegram.Target)
	}
	if loaded.Telegram.ChatID != -100123456 {
		t.Errorf("expected chat_id round-trip, got %d", loaded.Telegram.ChatID)
	}
	if len(loaded.Telegram.AllowFrom) != 2 {
		t.Errorf("expected allow_from round-trip, got %v", loaded.Telegram.AllowFrom)
	}
}

func TestSaveThrumConfig_OmitsEmptyTelegram(t *testing.T) {
	tmpDir := t.TempDir()
	cfg := &config.ThrumConfig{Daemon: config.DaemonConfig{LocalOnly: true}}

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
	if _, exists := raw["telegram"]; exists {
		t.Error("expected telegram key to be omitted when empty")
	}
}

func TestSaveThrumConfig_TelegramPreservesOtherKeys(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Write config with an unknown key
	if err := os.WriteFile(configPath, []byte(`{"custom":"preserve","daemon":{"local_only":true}}`), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.ThrumConfig{
		Daemon: config.DaemonConfig{LocalOnly: true},
		Telegram: config.TelegramConfig{
			Token:  "123:AAH",
			Target: "@coordinator_main",
		},
	}
	if err := config.SaveThrumConfig(tmpDir, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["custom"] != "preserve" {
		t.Error("unknown key was lost after saving with telegram section")
	}
	if _, exists := raw["telegram"]; !exists {
		t.Error("expected telegram section to be present")
	}
}

func TestTelegramConfig_MaskedToken(t *testing.T) {
	cfg := config.TelegramConfig{Token: "123456789:AAHxxxxxxxxxxxxxxx"}
	masked := cfg.MaskedToken()
	if masked != "123456789:" {
		t.Errorf("expected first 10 chars, got %q", masked)
	}

	short := config.TelegramConfig{Token: "short"}
	if short.MaskedToken() != "short" {
		t.Errorf("expected full short token, got %q", short.MaskedToken())
	}

	empty := config.TelegramConfig{}
	if empty.MaskedToken() != "" {
		t.Errorf("expected empty, got %q", empty.MaskedToken())
	}
}
