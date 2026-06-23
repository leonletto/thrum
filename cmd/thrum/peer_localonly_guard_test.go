package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// writeTestThrumConfig writes a minimal .thrum/config.json containing the
// given JSON string into a fresh temp directory and returns the .thrum path.
func writeTestThrumConfig(t *testing.T, jsonContent string) string {
	t.Helper()
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0700); err != nil {
		t.Fatalf("failed to create .thrum dir: %v", err)
	}
	cfgPath := filepath.Join(thrumDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(jsonContent), 0600); err != nil {
		t.Fatalf("failed to write config.json: %v", err)
	}
	return thrumDir
}

func TestGuardLocalOnlyPairing_RefusesWhenSyncOff(t *testing.T) {
	// sync disabled ⇒ refuse
	dir := writeTestThrumConfig(t, `{"daemon":{"sync":{"enabled":false}}}`)
	if err := guardLocalOnlyPairing(dir); err == nil {
		t.Fatal("sync disabled must refuse peer pairing")
	}

	// sync enabled ⇒ allow
	dir = writeTestThrumConfig(t, `{"daemon":{"sync":{"enabled":true}}}`)
	if err := guardLocalOnlyPairing(dir); err != nil {
		t.Fatalf("sync enabled must allow pairing, got %v", err)
	}

	// absent config ⇒ allow (matches the thrum-agents guard's non-local default)
	if err := guardLocalOnlyPairing(t.TempDir()); err != nil {
		t.Fatalf("absent config must not block pairing, got %v", err)
	}
}

func TestGuardLocalOnlyPairing_DefaultsAllow(t *testing.T) {
	// A fresh install with no sync stanza → applyDefaults picks sync:on a-sync(full).
	// Guard must allow.
	dir := writeTestThrumConfig(t, `{}`)
	if err := guardLocalOnlyPairing(dir); err != nil {
		t.Fatalf("default config (sync enabled) must allow pairing, got %v", err)
	}
}

// TestGuardLocalOnlyPairing_FreshInitAllowsAfterSaveReload is the BLOCKING
// end-to-end regression: a freshly-constructed config (Sync nil, as init builds
// it) persisted via SaveThrumConfig must reload with sync ON so the guard ALLOWS
// pairing. Pre-fix, the zero-value Sync struct was marshaled as
// sync:{enabled:false} (omitempty is a no-op on a value struct), reload skipped
// migration, and the guard refused — fresh nodes could not pair. The *SyncConfig
// pointer makes the nil omit the stanza on save, so reload migrates to the
// D9 default (enabled:true, a-sync full).
func TestGuardLocalOnlyPairing_FreshInitAllowsAfterSaveReload(t *testing.T) {
	dir := t.TempDir()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0700); err != nil {
		t.Fatal(err)
	}
	// As a fresh init constructs it: daemon settings, no explicit sync stanza.
	fresh := &config.ThrumConfig{Daemon: config.DaemonConfig{WSPort: "auto"}}
	if err := config.SaveThrumConfig(thrumDir, fresh); err != nil {
		t.Fatalf("save fresh config: %v", err)
	}
	if err := guardLocalOnlyPairing(thrumDir); err != nil {
		t.Fatalf("fresh-init node must allow pairing after save+reload, got %v", err)
	}
	// And confirm the reloaded config is genuinely sync-on a-sync(full).
	reloaded, err := config.LoadThrumConfig(thrumDir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Daemon.Sync == nil || !reloaded.Daemon.Sync.Enabled {
		t.Fatalf("fresh-init reload must be sync-enabled, got %+v", reloaded.Daemon.Sync)
	}
}
