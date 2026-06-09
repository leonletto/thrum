package main

import (
	"os"
	"path/filepath"
	"testing"
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
