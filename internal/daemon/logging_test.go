package daemon

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogFilePath(t *testing.T) {
	varDir := "/tmp/thrum-test/var"
	got := LogFilePath(varDir)
	want := filepath.Join(varDir, "daemon.log")
	if got != want {
		t.Errorf("LogFilePath = %q, want %q", got, want)
	}
}

func TestNewLogWriter(t *testing.T) {
	varDir := t.TempDir()
	lj := NewLogWriter(varDir)
	if lj == nil {
		t.Fatal("NewLogWriter returned nil")
	}
	defer func() { _ = lj.Close() }()

	if lj.Filename != filepath.Join(varDir, "daemon.log") {
		t.Errorf("Filename = %q, want daemon.log in %q", lj.Filename, varDir)
	}
	if lj.MaxSize != LogMaxSizeMB {
		t.Errorf("MaxSize = %d, want %d", lj.MaxSize, LogMaxSizeMB)
	}
	if lj.MaxBackups != LogMaxBackups {
		t.Errorf("MaxBackups = %d, want %d", lj.MaxBackups, LogMaxBackups)
	}
	if lj.MaxAge != LogMaxAgeDays {
		t.Errorf("MaxAge = %d, want %d", lj.MaxAge, LogMaxAgeDays)
	}
	if !lj.Compress {
		t.Error("Compress = false, want true")
	}

	// Write something and verify it lands in the log file.
	if _, err := lj.Write([]byte("hello lumberjack\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	data, err := os.ReadFile(lj.Filename) // #nosec G304 -- test file
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "hello lumberjack") {
		t.Errorf("log file missing expected content, got %q", string(data))
	}
}

func TestOpenRawLogFile(t *testing.T) {
	varDir := filepath.Join(t.TempDir(), "var")
	f, err := OpenRawLogFile(varDir)
	if err != nil {
		t.Fatalf("OpenRawLogFile: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })

	if _, err := f.WriteString("raw write\n"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}
	if err := f.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	data, err := os.ReadFile(LogFilePath(varDir)) // #nosec G304 -- test file
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "raw write\n" {
		t.Errorf("log file content = %q, want %q", string(data), "raw write\n")
	}

	// Verify OpenRawLogFile creates parent directory when missing.
	nested := filepath.Join(t.TempDir(), "a", "b", "c")
	f2, err := OpenRawLogFile(nested)
	if err != nil {
		t.Fatalf("OpenRawLogFile nested: %v", err)
	}
	_ = f2.Close()
	if _, err := os.Stat(LogFilePath(nested)); err != nil {
		t.Errorf("expected log file at %s: %v", LogFilePath(nested), err)
	}
}

func TestInstallLogWriter(t *testing.T) {
	// Save and restore log package state.
	origFlags := log.Flags()
	origPrefix := log.Prefix()
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(origFlags)
		log.SetPrefix(origPrefix)
	})

	varDir := t.TempDir()
	lj := NewLogWriter(varDir)
	defer func() { _ = lj.Close() }()

	InstallLogWriter(lj)
	log.Printf("test message via log.Printf")

	data, err := os.ReadFile(lj.Filename) // #nosec G304 -- test file
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "test message via log.Printf") {
		t.Errorf("log file missing expected content, got %q", string(data))
	}
}
