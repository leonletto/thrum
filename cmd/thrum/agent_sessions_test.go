package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// writeTestSnapshot writes a frontmatter+body snapshot file at path
// for fixture setup.
func writeTestSnapshot(t *testing.T, path, savedAt, reason, bigPicture string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	content := "---\n" +
		"agent: x\n" +
		"session_id: ses_test\n" +
		"saved_at: " + savedAt + "\n" +
		"reason: " + reason + "\n" +
		"machine_id: test-host\n" +
		"---\n\n" +
		"## 1. Big picture — what shipped this session\n\n" +
		bigPicture + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// writeTestIdentity writes a minimal valid identity file for agentID
// under thrumRoot/identities/. loadSessionsFromThrumRoot's
// errAgentNotRegistered probe stat()s this file.
func writeTestIdentity(t *testing.T, thrumRoot, agentID string) {
	t.Helper()
	idDir := filepath.Join(thrumRoot, "identities")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatalf("mkdir identities: %v", err)
	}
	body := fmt.Sprintf(`{"version":5,"repo_id":"test","agent":{"name":%q,"role":"implementer","module":"test"},"worktree":%q,"updated_at":"2026-05-17T00:00:00Z"}`,
		agentID, thrumRoot)
	if err := os.WriteFile(filepath.Join(idDir, agentID+".json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write identity: %v", err)
	}
}

// mustParseTime keeps the test bodies tidy when constructing
// SessionEntry fixtures with explicit timestamps.
func mustParseTime(t *testing.T, s string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return parsed
}

// renderToString runs a renderer with a fresh *cobra.Command whose
// stdout is wired to a *bytes.Buffer, and returns the captured output.
func renderToString(t *testing.T, fn func(*cobra.Command) error) string {
	t.Helper()
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := fn(cmd); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestLoadSessionsFromThrumRoot_UnregisteredAgent_ReturnsSentinel(t *testing.T) {
	thrumRoot := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, err := loadSessionsFromThrumRoot(thrumRoot, "ghost-agent")
	if !errors.Is(err, errAgentNotRegistered) {
		t.Errorf("expected errAgentNotRegistered, got %v", err)
	}
}

func TestLoadSessionsFromThrumRoot_NoSessionsFolder_ReturnsEmpty(t *testing.T) {
	thrumRoot := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestIdentity(t, thrumRoot, "alpha")

	sessions, err := loadSessionsFromThrumRoot(thrumRoot, "alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions for missing folder, got %d", len(sessions))
	}
}

func TestLoadSessionsFromThrumRoot_OneSession_PopulatesFields(t *testing.T) {
	thrumRoot := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestIdentity(t, thrumRoot, "alpha")

	sessionsDir := filepath.Join(thrumRoot, "agents", "alpha", "sessions")
	writeTestSnapshot(t,
		filepath.Join(sessionsDir, "20260517T153218421Z-restart.md"),
		"2026-05-17T15:32:18.421Z",
		"external",
		"Locked the spec.")

	sessions, err := loadSessionsFromThrumRoot(thrumRoot, "alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.AgentID != "alpha" {
		t.Errorf("AgentID: got %q, want alpha", s.AgentID)
	}
	if s.Reason != "external" {
		t.Errorf("Reason: got %q, want external", s.Reason)
	}
	if s.BigPictureNormalized != "Locked the spec." {
		t.Errorf("BigPictureNormalized: got %q, want 'Locked the spec.'", s.BigPictureNormalized)
	}
	if !strings.HasSuffix(s.Path, "-restart.md") {
		t.Errorf("Path should end in -restart.md: %q", s.Path)
	}
	if s.Size == 0 {
		t.Error("Size should be > 0 for a written file")
	}
}

// TestLoadSessionsFromThrumRoot_SortsDescendingByTimestamp covers the
// §6.2 ordering invariant: most recent first.
func TestLoadSessionsFromThrumRoot_SortsDescendingByTimestamp(t *testing.T) {
	thrumRoot := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestIdentity(t, thrumRoot, "alpha")

	sessionsDir := filepath.Join(thrumRoot, "agents", "alpha", "sessions")
	writeTestSnapshot(t,
		filepath.Join(sessionsDir, "older-restart.md"),
		"2026-05-15T10:00:00.000Z", "manual", "old work")
	writeTestSnapshot(t,
		filepath.Join(sessionsDir, "middle-restart.md"),
		"2026-05-16T10:00:00.000Z", "manual", "middle work")
	writeTestSnapshot(t,
		filepath.Join(sessionsDir, "newest-restart.md"),
		"2026-05-17T10:00:00.000Z", "manual", "newest work")

	sessions, err := loadSessionsFromThrumRoot(thrumRoot, "alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(sessions))
	}
	if sessions[0].BigPictureNormalized != "newest work" {
		t.Errorf("first entry should be newest: %q", sessions[0].BigPictureNormalized)
	}
	if sessions[2].BigPictureNormalized != "old work" {
		t.Errorf("last entry should be oldest: %q", sessions[2].BigPictureNormalized)
	}
}

// TestLoadSessionsFromThrumRoot_NonRestartFilesIgnored confirms only
// *-restart.md files contribute to the listing.
func TestLoadSessionsFromThrumRoot_NonRestartFilesIgnored(t *testing.T) {
	thrumRoot := filepath.Join(t.TempDir(), ".thrum")
	if err := os.MkdirAll(thrumRoot, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeTestIdentity(t, thrumRoot, "alpha")

	sessionsDir := filepath.Join(thrumRoot, "agents", "alpha", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "random.md"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	sessions, err := loadSessionsFromThrumRoot(thrumRoot, "alpha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Errorf("non-restart files should be ignored; got %d sessions", len(sessions))
	}
}

// === renderDefault tests (Task 10 default mode) ===

func TestRenderDefault_TableShape(t *testing.T) {
	sessions := []SessionEntry{
		{
			Timestamp:            mustParseTime(t, "2026-05-17T15:32:18Z"),
			Size:                 1024,
			Reason:               "external",
			BigPictureNormalized: "Locked the spec.",
		},
	}

	out := renderToString(t, func(cmd *cobra.Command) error {
		return renderDefault(cmd, "alpha", sessions)
	})

	if !strings.Contains(out, "Sessions for alpha (1 total") {
		t.Errorf("header missing or wrong: %q", out)
	}
	if !strings.Contains(out, "TIMESTAMP") || !strings.Contains(out, "SIZE") || !strings.Contains(out, "REASON") || !strings.Contains(out, "SUMMARY") {
		t.Errorf("missing column header: %q", out)
	}
	if !strings.Contains(out, "Locked the spec.") {
		t.Errorf("missing SUMMARY value: %q", out)
	}
	if !strings.Contains(out, "external") {
		t.Errorf("missing REASON value: %q", out)
	}
	if !strings.Contains(out, "1K") {
		t.Errorf("size should render as 1K: %q", out)
	}
}

// TestRenderDefault_NoBigPicturePlaceholder confirms the "(no
// big-picture summary)" fallback for sessions whose §1 parse yielded
// an empty string.
func TestRenderDefault_NoBigPicturePlaceholder(t *testing.T) {
	sessions := []SessionEntry{
		{
			Timestamp:            mustParseTime(t, "2026-05-17T15:32:18Z"),
			Size:                 512,
			Reason:               "external",
			BigPictureNormalized: "",
		},
	}

	out := renderToString(t, func(cmd *cobra.Command) error {
		return renderDefault(cmd, "alpha", sessions)
	})
	if !strings.Contains(out, "(no big-picture summary)") {
		t.Errorf("missing placeholder: %q", out)
	}
}

// === Flag-validation tests (Task 10 acceptance criteria) ===

func TestAgentSessionsList_VerboseAndJSON_Rejected(t *testing.T) {
	cmd := agentSessionsCmd()
	cmd.SetArgs([]string{"list", "alpha", "--verbose", "--json"})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&stderr) // suppress noise

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --verbose --json combo, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutual exclusion: %v", err)
	}
}

func TestAgentSessionsList_AllAndExplicitAgent_Rejected(t *testing.T) {
	cmd := agentSessionsCmd()
	cmd.SetArgs([]string{"list", "alpha", "--all"})
	var stderr bytes.Buffer
	cmd.SetErr(&stderr)
	cmd.SetOut(&stderr)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for --all + agent-id, got nil")
	}
	if !strings.Contains(err.Error(), "--all cannot be combined") {
		t.Errorf("error should mention --all conflict: %v", err)
	}
}

// === humanSize tests ===

func TestHumanSize_Thresholds(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{500, "500B"},
		{1024, "1K"},
		{1024 * 1024, "1.0M"},
		{1024 * 1024 * 1024, "1.0G"},
	}
	for _, tc := range cases {
		if got := humanSize(tc.in); got != tc.want {
			t.Errorf("humanSize(%d): got %q, want %q", tc.in, got, tc.want)
		}
	}
}

// === firstLine tests ===

func TestFirstLine(t *testing.T) {
	if got := firstLine("hello\nworld"); got != "hello" {
		t.Errorf("got %q, want 'hello'", got)
	}
	if got := firstLine("no newline"); got != "no newline" {
		t.Errorf("got %q, want unchanged", got)
	}
	if got := firstLine(""); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}
