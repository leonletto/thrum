package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// writeTestLog writes the given lines to .thrum/var/daemon.log under repoDir.
// Each line is newline-terminated.
func writeTestLog(t *testing.T, repoDir string, lines []string) string {
	t.Helper()
	varDir := filepath.Join(repoDir, ".thrum", "var")
	if err := os.MkdirAll(varDir, 0750); err != nil {
		t.Fatalf("mkdir var: %v", err)
	}
	path := filepath.Join(varDir, "daemon.log")
	f, err := os.Create(path) // #nosec G304 -- test file
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer func() { _ = f.Close() }()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			t.Fatalf("write line: %v", err)
		}
	}
	return path
}

func TestDaemonLogs_AllLines(t *testing.T) {
	repo := t.TempDir()
	writeTestLog(t, repo, []string{"line one", "line two", "line three"})

	var buf bytes.Buffer
	if err := DaemonLogs(repo, DaemonLogsOptions{Lines: 0}, &buf); err != nil {
		t.Fatalf("DaemonLogs: %v", err)
	}
	got := buf.String()
	want := "line one\nline two\nline three\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDaemonLogs_LastNLines(t *testing.T) {
	repo := t.TempDir()
	lines := []string{"one", "two", "three", "four", "five"}
	writeTestLog(t, repo, lines)

	var buf bytes.Buffer
	if err := DaemonLogs(repo, DaemonLogsOptions{Lines: 2}, &buf); err != nil {
		t.Fatalf("DaemonLogs: %v", err)
	}
	got := buf.String()
	want := "four\nfive\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDaemonLogs_LastNLines_FewerAvailable(t *testing.T) {
	repo := t.TempDir()
	writeTestLog(t, repo, []string{"a", "b"})

	var buf bytes.Buffer
	if err := DaemonLogs(repo, DaemonLogsOptions{Lines: 10}, &buf); err != nil {
		t.Fatalf("DaemonLogs: %v", err)
	}
	got := buf.String()
	want := "a\nb\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDaemonLogs_Since(t *testing.T) {
	repo := t.TempDir()
	writeTestLog(t, repo, []string{
		"2026/04/09 10:00:00.000000 first",
		"2026/04/09 10:30:00.000000 second",
		"2026/04/09 11:00:00.000000 third",
		"not a timestamped line",
		"2026/04/09 12:00:00.000000 fourth",
	})

	since := time.Date(2026, 4, 9, 11, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	err := DaemonLogs(repo, DaemonLogsOptions{Lines: 0, Since: &since}, &buf)
	if err != nil {
		t.Fatalf("DaemonLogs: %v", err)
	}
	got := buf.String()

	// Expect lines from the 11:00 entry onward, including the non-timestamped
	// line that follows (activated by the previous match).
	if !strings.Contains(got, "third") {
		t.Errorf("expected %q to contain 'third'", got)
	}
	if !strings.Contains(got, "fourth") {
		t.Errorf("expected %q to contain 'fourth'", got)
	}
	if strings.Contains(got, "first") || strings.Contains(got, "second") {
		t.Errorf("expected filter to exclude pre-11:00 lines, got %q", got)
	}
	if !strings.Contains(got, "not a timestamped line") {
		t.Errorf("expected non-timestamped line (after activation) to be included, got %q", got)
	}
}

func TestDaemonLogs_MissingFile(t *testing.T) {
	repo := t.TempDir()
	// Don't create any log file.
	var buf bytes.Buffer
	err := DaemonLogs(repo, DaemonLogsOptions{Lines: 50}, &buf)
	if err == nil {
		t.Fatal("expected error for missing log file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %v", err)
	}
}

func TestDaemonLogs_Follow(t *testing.T) {
	repo := t.TempDir()
	path := writeTestLog(t, repo, []string{"initial-line"})

	// Tailing runs until the process is killed in real use. For the test
	// we run it in a goroutine with a bounded reader that we close to
	// cause follow to return via a read error.
	var (
		buf bytes.Buffer
		mu  sync.Mutex
	)
	writer := &lockedBuffer{mu: &mu, buf: &buf}

	done := make(chan error, 1)
	go func() {
		done <- DaemonLogs(repo, DaemonLogsOptions{Lines: 1, Follow: true}, writer)
	}()

	// Give the follower a moment to read the initial line and start tailing.
	time.Sleep(300 * time.Millisecond)

	// Append new content.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600) // #nosec G304 -- test
	if err != nil {
		t.Fatalf("reopen log: %v", err)
	}
	if _, err := fmt.Fprintln(f, "streamed-line-1"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := fmt.Fprintln(f, "streamed-line-2"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	// Poll until the streamed lines appear (or timeout).
	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		got = buf.String()
		mu.Unlock()
		if strings.Contains(got, "streamed-line-2") {
			break
		}
	}

	if !strings.Contains(got, "initial-line") {
		t.Errorf("expected initial-line in output, got %q", got)
	}
	if !strings.Contains(got, "streamed-line-1") {
		t.Errorf("expected streamed-line-1 in output, got %q", got)
	}
	if !strings.Contains(got, "streamed-line-2") {
		t.Errorf("expected streamed-line-2 in output, got %q", got)
	}

	// Goroutine is still blocked in follow; we leave it to exit at test
	// teardown. t.Cleanup is not needed because the goroutine holds no
	// resources that matter beyond the test process.
	_ = done
}

func TestDaemonLogs_FollowHandlesRotation(t *testing.T) {
	repo := t.TempDir()
	path := writeTestLog(t, repo, []string{"pre-rotation"})

	var (
		buf bytes.Buffer
		mu  sync.Mutex
	)
	writer := &lockedBuffer{mu: &mu, buf: &buf}

	go func() {
		_ = DaemonLogs(repo, DaemonLogsOptions{Lines: 1, Follow: true}, writer)
	}()

	time.Sleep(300 * time.Millisecond)

	// Simulate lumberjack rotation: rename old file and create new one.
	rotatedPath := path + ".1"
	if err := os.Rename(path, rotatedPath); err != nil {
		t.Fatalf("rename: %v", err)
	}
	f, err := os.Create(path) // #nosec G304 -- test
	if err != nil {
		t.Fatalf("create new log: %v", err)
	}
	if _, err := fmt.Fprintln(f, "post-rotation"); err != nil {
		t.Fatalf("write new log: %v", err)
	}
	_ = f.Close()

	deadline := time.Now().Add(3 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		got = buf.String()
		mu.Unlock()
		if strings.Contains(got, "post-rotation") {
			break
		}
	}
	if !strings.Contains(got, "pre-rotation") {
		t.Errorf("expected pre-rotation in output, got %q", got)
	}
	if !strings.Contains(got, "post-rotation") {
		t.Errorf("expected post-rotation in output after rotation, got %q", got)
	}
}

func TestParseLogTimestamp(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"microseconds", "2026/04/09 18:14:56.122848 hello", true},
		{"std flags only", "2026/04/09 18:14:56 hello", true},
		{"no timestamp", "plain log line", false},
		{"empty", "", false},
		{"short", "2026/04/09", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseLogTimestamp(tc.line)
			if ok != tc.want {
				t.Errorf("parseLogTimestamp(%q) ok = %v, want %v", tc.line, ok, tc.want)
			}
		})
	}
}

// lockedBuffer is a goroutine-safe bytes.Buffer wrapper for concurrent tests.
type lockedBuffer struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
