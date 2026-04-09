// Package daemon provides daemon lifecycle and logging utilities.
package daemon

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Log rotation defaults.
const (
	// LogMaxSizeMB is the max log file size before rotation.
	LogMaxSizeMB = 10
	// LogMaxBackups is the number of rotated files to keep.
	LogMaxBackups = 4
	// LogMaxAgeDays is the max age (in days) to retain rotated files.
	LogMaxAgeDays = 28
	// LogFileName is the daemon log file name inside .thrum/var/.
	LogFileName = "daemon.log"
)

// LogFilePath returns the absolute path to the daemon log file inside the
// given .thrum/var directory.
func LogFilePath(varDir string) string {
	return filepath.Join(varDir, LogFileName)
}

// NewLogWriter returns a lumberjack.Logger configured for daemon log rotation.
// Rotation: 10MB files, 4 backups, 28 day retention, gzip compressed.
func NewLogWriter(varDir string) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   LogFilePath(varDir),
		MaxSize:    LogMaxSizeMB,
		MaxBackups: LogMaxBackups,
		MaxAge:     LogMaxAgeDays,
		Compress:   true,
	}
}

// OpenRawLogFile opens the daemon log file for append-only writes without
// going through lumberjack. Used by the parent process in DaemonStart so the
// forked daemon inherits a valid fd for os.Stdout/os.Stderr before lumberjack
// is initialized inside the child. Callers must Close the returned file after
// handing it to exec.Cmd; the child retains its own duplicated fd.
func OpenRawLogFile(varDir string) (*os.File, error) {
	if err := os.MkdirAll(varDir, 0750); err != nil {
		return nil, fmt.Errorf("create var dir: %w", err)
	}
	path := LogFilePath(varDir)
	// #nosec G302 G304 -- path is an internal .thrum/var/ log file, 0600 is
	// stricter than typical log files but preserves user isolation.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// InstallLogWriter installs w as the destination for the standard log package
// and returns the writer. Callers typically pass the result of NewLogWriter.
// Standard flags include microseconds and UTC for consistent daemon timestamps.
func InstallLogWriter(w io.Writer) {
	log.SetOutput(w)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds | log.LUTC)
}

// ParseLogLevel converts a string log level ("debug", "info", "warn",
// "error") to a slog.Level. Unknown values return slog.LevelInfo.
func ParseLogLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// ConfigureSlog installs a slog.Logger writing to w at the given level and
// sets it as the package-default slog logger. The handler format matches the
// standard log package prefix so `thrum daemon logs` parses timestamps
// consistently regardless of whether a line came from log.Printf or slog.
// Returns the configured logger for callers that want to hold their own
// reference (e.g. to add attributes).
func ConfigureSlog(w io.Writer, level string) *slog.Logger {
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{
		Level:     ParseLogLevel(level),
		AddSource: false,
		// Replace the default "time" key format with one that matches the
		// log package's "2006/01/02 15:04:05.000000" prefix so daemon logs
		// --since parsing works uniformly.
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.String(slog.TimeKey, a.Value.Time().UTC().Format("2006/01/02 15:04:05.000000"))
			}
			return a
		},
	})
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}
