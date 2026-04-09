package daemon

import (
	"bytes"
	"log"
	"log/slog"
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

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"", slog.LevelInfo}, // default
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"unknown", slog.LevelInfo}, // fallback
		{"  info  ", slog.LevelInfo},
	}
	for _, tc := range tests {
		if got := ParseLogLevel(tc.in); got != tc.want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestConfigureSlog_LevelFiltering(t *testing.T) {
	// Save and restore default slog logger.
	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	t.Run("info level hides debug", func(t *testing.T) {
		var buf bytes.Buffer
		ConfigureSlog(&buf, "info")
		slog.Debug("hidden debug message")
		slog.Info("visible info message")
		slog.Warn("visible warn message")

		out := buf.String()
		if strings.Contains(out, "hidden debug message") {
			t.Errorf("expected debug to be filtered, got %q", out)
		}
		if !strings.Contains(out, "visible info message") {
			t.Errorf("expected info to be visible, got %q", out)
		}
		if !strings.Contains(out, "visible warn message") {
			t.Errorf("expected warn to be visible, got %q", out)
		}
	})

	t.Run("debug level shows debug", func(t *testing.T) {
		var buf bytes.Buffer
		ConfigureSlog(&buf, "debug")
		slog.Debug("debug message")
		slog.Info("info message")

		out := buf.String()
		if !strings.Contains(out, "debug message") {
			t.Errorf("expected debug to be visible at debug level, got %q", out)
		}
		if !strings.Contains(out, "info message") {
			t.Errorf("expected info to be visible at debug level, got %q", out)
		}
	})

	t.Run("error level hides info and warn", func(t *testing.T) {
		var buf bytes.Buffer
		ConfigureSlog(&buf, "error")
		slog.Debug("debug")
		slog.Info("info")
		slog.Warn("warn")
		slog.Error("error")

		out := buf.String()
		if strings.Contains(out, "debug") || strings.Contains(out, "info") || strings.Contains(out, "warn") {
			t.Errorf("expected only error level to be visible, got %q", out)
		}
		if !strings.Contains(out, "error") {
			t.Errorf("expected error to be visible, got %q", out)
		}
	})
}

func TestConfigureSlog_TimestampFormat(t *testing.T) {
	origDefault := slog.Default()
	t.Cleanup(func() { slog.SetDefault(origDefault) })

	var buf bytes.Buffer
	ConfigureSlog(&buf, "info")
	slog.Info("test message")

	out := buf.String()
	// Expect timestamp in "YYYY/MM/DD HH:MM:SS.ffffff" format matching log pkg,
	// which makes parseLogTimestamp in the daemon logs command work uniformly.
	// Example: time="2026/04/09 18:14:56.123456" level=INFO msg="test message"
	if !strings.Contains(out, `time="`) {
		t.Errorf("expected time= key, got %q", out)
	}
	// Look for the date portion "2026/" or "20" followed by slash-formatted date
	if !strings.Contains(out, "/") {
		t.Errorf("expected slash-formatted date in timestamp, got %q", out)
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
