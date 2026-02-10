package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/daemon"
)

func TestDaemonStatus_NotRunning(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .thrum/var directory structure
	varDir := filepath.Join(tmpDir, ".thrum", "var")
	if err := os.MkdirAll(varDir, 0700); err != nil {
		t.Fatalf("Failed to create var directory: %v", err)
	}

	result, err := DaemonStatus(tmpDir)
	if err != nil {
		t.Fatalf("DaemonStatus failed: %v", err)
	}

	if result.Running {
		t.Error("Expected daemon to not be running")
	}

	if result.PID != 0 {
		t.Errorf("Expected PID to be 0, got %d", result.PID)
	}
}

func TestDaemonStatus_Running(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .thrum/var directory structure
	varDir := filepath.Join(tmpDir, ".thrum", "var")
	if err := os.MkdirAll(varDir, 0700); err != nil {
		t.Fatalf("Failed to create var directory: %v", err)
	}

	// Write PID file with current process (which is running)
	pidPath := filepath.Join(varDir, "thrum.pid")
	if err := daemon.WritePIDFile(pidPath); err != nil {
		t.Fatalf("Failed to write PID file: %v", err)
	}

	result, err := DaemonStatus(tmpDir)
	if err != nil {
		t.Fatalf("DaemonStatus failed: %v", err)
	}

	if !result.Running {
		t.Error("Expected daemon to be running")
	}

	if result.PID != os.Getpid() {
		t.Errorf("Expected PID to be %d, got %d", os.Getpid(), result.PID)
	}
}

func TestDaemonStop_NotRunning(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .thrum/var directory structure
	varDir := filepath.Join(tmpDir, ".thrum", "var")
	if err := os.MkdirAll(varDir, 0700); err != nil {
		t.Fatalf("Failed to create var directory: %v", err)
	}

	// Try to stop daemon when it's not running
	err := DaemonStop(tmpDir)
	if err == nil {
		t.Error("Expected error when stopping non-running daemon")
	}
}

func TestFormatDaemonStatus_NotRunning(t *testing.T) {
	result := &DaemonStatusResult{
		Running: false,
	}

	output := FormatDaemonStatus(result)
	expected := "Daemon:   not running\n"

	if output != expected {
		t.Errorf("Expected:\n%s\nGot:\n%s", expected, output)
	}
}

func TestFormatDaemonStatus_Running(t *testing.T) {
	result := &DaemonStatusResult{
		Running:   true,
		PID:       12345,
		Uptime:    "5m",
		Version:   "1.0.0",
		SyncState: "synced",
	}

	output := FormatDaemonStatus(result)

	// Check that output contains expected information
	if len(output) == 0 {
		t.Error("Expected non-empty output")
	}

	// Verify key fields are present
	expectedFields := []string{
		"running",
		"12345",
		"5m",
		"1.0.0",
		"synced",
	}

	for _, field := range expectedFields {
		if !contains(output, field) {
			t.Errorf("Expected output to contain '%s'", field)
		}
	}
}

func TestFormatDaemonStatus_RunningMinimal(t *testing.T) {
	result := &DaemonStatusResult{
		Running: true,
		PID:     12345,
	}

	output := FormatDaemonStatus(result)

	// Should at least show running status and PID
	if !contains(output, "running") {
		t.Error("Expected output to contain 'running'")
	}

	if !contains(output, "12345") {
		t.Error("Expected output to contain PID '12345'")
	}
}

// helper function to check if string contains substring.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
