package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/identity/guard"
)

func TestGuardDaemonBootstrap_NonGitStrictRefuses(t *testing.T) {
	dir := t.TempDir()
	err := guardDaemonBootstrap(dir, false, nil)
	if err == nil {
		t.Fatal("want error in non-git dir with strict mode")
	}
	var gErr *guard.Error
	if !errors.As(err, &gErr) {
		t.Fatalf("want *guard.Error, got %T: %v", err, err)
	}
	if gErr.Guard != "non_git_bootstrap" {
		t.Errorf("guard = %q, want non_git_bootstrap", gErr.Guard)
	}
}

func TestGuardDaemonBootstrap_ForceAllows(t *testing.T) {
	dir := t.TempDir()
	if err := guardDaemonBootstrap(dir, true, nil); err != nil {
		t.Fatalf("force=true should proceed in non-git dir, got %v", err)
	}
}

func TestGuardDaemonBootstrap_GitRepoAllows(t *testing.T) {
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil { //nolint:gosec // test fixture
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := guardDaemonBootstrap(dir, false, nil); err != nil {
		t.Fatalf("git-anchored dir should proceed, got %v", err)
	}
}

func TestGuardDaemonBootstrap_ConfigOffAllows(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapConfig(t, dir, "off")
	if err := guardDaemonBootstrap(dir, false, nil); err != nil {
		t.Fatalf("non_git_bootstrap=off should proceed, got %v", err)
	}
}

func TestGuardDaemonBootstrap_ConfigWarnLogsAndAllows(t *testing.T) {
	dir := t.TempDir()
	writeBootstrapConfig(t, dir, "warn")
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	if err := guardDaemonBootstrap(dir, false, logger); err != nil {
		t.Fatalf("warn mode should not return error, got %v", err)
	}
	if !strings.Contains(buf.String(), "non_git_bootstrap") {
		t.Errorf("warn mode should emit guard slog event, got %q", buf.String())
	}
}

func writeBootstrapConfig(t *testing.T, dir, mode string) {
	t.Helper()
	thrumDir := filepath.Join(dir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0o750); err != nil {
		t.Fatalf("mkdir .thrum: %v", err)
	}
	cfg := map[string]any{
		"identity_guard": map[string]any{
			"non_git_bootstrap": mode,
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(thrumDir, "config.json"), data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
