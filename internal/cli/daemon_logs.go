package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/leonletto/thrum/internal/daemon"
	"github.com/leonletto/thrum/internal/paths"
)

// DaemonLogsOptions controls the behavior of DaemonLogs.
type DaemonLogsOptions struct {
	Lines  int        // Number of lines to print before tailing (0 = all).
	Follow bool       // If true, keep streaming new lines after initial read.
	Since  *time.Time // If non-nil, only print lines at or after this timestamp.
}

// DaemonLogs reads .thrum/var/daemon.log and writes filtered output to out.
// When opts.Follow is true, it continues streaming until the reader returns
// an unrecoverable error or ctx-less termination (SIGINT handled by caller).
// Rotation is detected by watching inode/size and reopening the file.
func DaemonLogs(repoPath string, opts DaemonLogsOptions, out io.Writer) error {
	thrumDir, err := paths.ResolveThrumDir(repoPath)
	if err != nil {
		thrumDir = filepath.Join(repoPath, ".thrum")
	}
	varDir := filepath.Join(thrumDir, "var")
	logPath := daemon.LogFilePath(varDir)

	if _, err := os.Stat(logPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("daemon log not found at %s (daemon may not have run yet)", logPath)
		}
		return fmt.Errorf("stat daemon log: %w", err)
	}

	// Print the initial lines (last N or all, filtered by --since).
	if err := printInitialLines(logPath, opts, out); err != nil {
		return err
	}

	if !opts.Follow {
		return nil
	}

	return followLogFile(logPath, opts, out)
}

// printInitialLines reads the log file and prints the last N lines matching
// the since filter, or the full content if Lines is 0.
func printInitialLines(logPath string, opts DaemonLogsOptions, out io.Writer) error {
	f, err := os.Open(logPath) // #nosec G304 -- logPath is .thrum/var/daemon.log, internal daemon file
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Use a scanner with a large buffer to handle long log lines.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var lines []string
	if opts.Lines > 0 {
		lines = make([]string, 0, opts.Lines)
	}

	sinceActive := opts.Since == nil // no filter = always active

	for scanner.Scan() {
		line := scanner.Text()
		if !sinceActive {
			if ts, ok := parseLogTimestamp(line); ok && !ts.Before(*opts.Since) {
				sinceActive = true
			}
		}
		if !sinceActive {
			continue
		}

		if opts.Lines > 0 {
			// Ring-buffer keeping the last N lines.
			if len(lines) == opts.Lines {
				lines = append(lines[1:], line)
			} else {
				lines = append(lines, line)
			}
		} else {
			if _, err := fmt.Fprintln(out, line); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read daemon log: %w", err)
	}

	for _, line := range lines {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}

	return nil
}

// followLogFile tails the log file, streaming new content until io.EOF is
// encountered without new data after the poll interval indefinitely. Handles
// lumberjack rotation by detecting inode change / size shrink and reopening.
func followLogFile(logPath string, opts DaemonLogsOptions, out io.Writer) error {
	const pollInterval = 200 * time.Millisecond

	f, err := os.Open(logPath) // #nosec G304 -- logPath is .thrum/var/daemon.log
	if err != nil {
		return fmt.Errorf("reopen daemon log: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Seek to end so we only stream new content.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("seek to end: %w", err)
	}

	currentStat, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat daemon log: %w", err)
	}

	reader := bufio.NewReader(f)
	sinceActive := opts.Since == nil

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			trimmed := strings.TrimRight(line, "\n")
			if !sinceActive {
				if ts, ok := parseLogTimestamp(trimmed); ok && !ts.Before(*opts.Since) {
					sinceActive = true
				}
			}
			if sinceActive {
				if _, werr := fmt.Fprintln(out, trimmed); werr != nil {
					return werr
				}
			}
			continue
		}
		if err != nil && err != io.EOF {
			return fmt.Errorf("read daemon log: %w", err)
		}

		// EOF — wait briefly and check for rotation.
		time.Sleep(pollInterval)

		rotated, newF, newStat, rerr := detectRotation(logPath, currentStat)
		if rerr != nil {
			return rerr
		}
		if rotated {
			_ = f.Close()
			f = newF
			currentStat = newStat
			reader = bufio.NewReader(f)
		}
	}
}

// detectRotation checks whether the log file at path has been replaced
// (different inode) or truncated (smaller size). When rotation is detected,
// it opens the new file and returns it. The caller is responsible for closing
// the returned file on subsequent rotations.
func detectRotation(path string, prev os.FileInfo) (bool, *os.File, os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// File temporarily missing during rotation; keep tailing current fd.
			return false, nil, nil, nil
		}
		return false, nil, nil, fmt.Errorf("stat daemon log: %w", err)
	}

	// Check inode change (Unix) or size shrink (portable fallback).
	if !os.SameFile(prev, info) || info.Size() < prev.Size() {
		newF, err := os.Open(path) // #nosec G304 -- logPath is .thrum/var/daemon.log
		if err != nil {
			return false, nil, nil, fmt.Errorf("reopen daemon log: %w", err)
		}
		return true, newF, info, nil
	}
	return false, nil, nil, nil
}

// parseLogTimestamp extracts a timestamp from a log line written by
// InstallLogWriter. The log package with LstdFlags|Lmicroseconds|LUTC prefixes
// lines with "YYYY/MM/DD HH:MM:SS.ffffff ". Falls back to the without-
// microseconds format. Lines without either prefix (e.g. raw stderr output)
// return ok=false.
func parseLogTimestamp(line string) (time.Time, bool) {
	const (
		microLayout = "2006/01/02 15:04:05.000000"
		microLen    = len(microLayout)
		stdLayout   = "2006/01/02 15:04:05"
		stdLen      = len(stdLayout)
	)

	if len(line) >= microLen {
		if t, err := time.Parse(microLayout, line[:microLen]); err == nil {
			return t.UTC(), true
		}
	}
	if len(line) >= stdLen {
		if t, err := time.Parse(stdLayout, line[:stdLen]); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
