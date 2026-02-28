package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

func TestRunPlugins_CommandSuccess(t *testing.T) {
	repoPath := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "current")

	// Create a file that the plugin command will produce
	outFile := filepath.Join(repoPath, "output.txt")
	plugins := []config.PluginConfig{
		{
			Name:    "test-plugin",
			Command: "echo hello > " + outFile,
			Include: []string{"output.txt"},
		},
	}

	results, err := RunPlugins(plugins, repoPath, backupDir)
	if err != nil {
		t.Fatalf("RunPlugins() error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	r := results[0]
	if r.Name != "test-plugin" {
		t.Errorf("expected name=test-plugin, got %q", r.Name)
	}
	if r.CmdError != "" {
		t.Errorf("unexpected command error: %s", r.CmdError)
	}
	if r.Files != 1 {
		t.Errorf("expected 1 file copied, got %d", r.Files)
	}

	// Verify file was copied to backup dir
	copied := filepath.Join(backupDir, "plugins", "test-plugin", "output.txt")
	if _, err := os.Stat(copied); err != nil {
		t.Errorf("expected plugin output at %s: %v", copied, err)
	}
}

func TestRunPlugins_CommandFailure(t *testing.T) {
	repoPath := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "current")

	plugins := []config.PluginConfig{
		{
			Name:    "failing",
			Command: "exit 1",
			Include: []string{"*.txt"},
		},
	}

	results, err := RunPlugins(plugins, repoPath, backupDir)
	if err != nil {
		t.Fatalf("RunPlugins() error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].CmdError == "" {
		t.Error("expected command error for failing plugin")
	}
	if results[0].Files != 0 {
		t.Error("expected 0 files when command fails")
	}
}

func TestRunPlugins_NoCommand(t *testing.T) {
	repoPath := t.TempDir()
	backupDir := filepath.Join(t.TempDir(), "current")

	// Create a file to collect without running a command
	if err := os.WriteFile(filepath.Join(repoPath, "data.txt"), []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	plugins := []config.PluginConfig{
		{
			Name:    "collector",
			Include: []string{"data.txt"},
		},
	}

	results, err := RunPlugins(plugins, repoPath, backupDir)
	if err != nil {
		t.Fatalf("RunPlugins() error: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Files != 1 {
		t.Errorf("expected 1 file, got %d", results[0].Files)
	}
}

func TestPluginNames(t *testing.T) {
	results := []PluginResult{
		{Name: "good", CmdError: ""},
		{Name: "bad", CmdError: "exit 1"},
		{Name: "also-good", CmdError: ""},
	}

	names := PluginNames(results)
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}
	if names[0] != "good" || names[1] != "also-good" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestFormatPluginResults(t *testing.T) {
	results := []PluginResult{
		{Name: "beads", Files: 3},
		{Name: "failing", CmdError: "exit status 1"},
	}

	out := FormatPluginResults(results)
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !contains(out, "beads: 3 files") {
		t.Errorf("expected beads summary, got %q", out)
	}
	if !contains(out, "failing: FAILED") {
		t.Errorf("expected failing summary, got %q", out)
	}
}

func TestFormatPluginResults_Empty(t *testing.T) {
	if out := FormatPluginResults(nil); out != "" {
		t.Errorf("expected empty string for nil results, got %q", out)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && containsStr(s, sub)
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
