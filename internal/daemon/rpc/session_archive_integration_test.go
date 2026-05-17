package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSessionArchiveIntegration_RestartArchiveNextPrime_FullFlow is the
// E2.4 dual-purpose test (A1 amend): exercises the full restart →
// archive → next-prime → second-archive lifecycle in a single test
// and doubles as the E5 acceptance gate signal.
//
// What it covers (beyond the per-task tests in session_archive_test.go):
//
//  1. Multi-call lifecycle: two consecutive archive operations across
//     two distinct snapshots, ensuring sessions/ accumulates rather
//     than overwrites.
//  2. Discovery-hint counting: N=1 after first archive, N=2 after
//     second — exercises listSessions across an evolving folder.
//  3. Idempotent third call with no snapshot: returns null fields for
//     ArchivedPath/Content/BigPicture but still surfaces a non-nil
//     DiscoveryHint reflecting the prior archives.
//  4. Permission invariant on the destination tree across multiple
//     archives (0600 file, 0700 dir).
//  5. End-to-end frontmatter pipeline: written snapshot → parseSavedAt
//     → FormatTimestamp filename → mtime preservation → bigPicture
//     extracted from the body.
//
// Re-runs as the E5 acceptance gate per plan A1 amend.
func TestSessionArchiveIntegration_RestartArchiveNextPrime_FullFlow(t *testing.T) {
	h, agentID, srcPath := newArchiveTestHandler(t, "persistent")

	// === Call 1: first snapshot → first archive ===
	writeArchiveSnapshot(t, srcPath, "2026-05-17T15:32:18.421Z", "First session: locked the substrate spec.")

	r1 := callArchive(t, h, agentID)
	if r1.ArchivedPath == nil {
		t.Fatal("call 1: expected ArchivedPath, got nil")
	}
	if r1.BigPicture == nil || *r1.BigPicture != "First session: locked the substrate spec." {
		t.Errorf("call 1: BigPicture mismatch: got %v", r1.BigPicture)
	}
	if r1.Content == nil {
		t.Fatal("call 1: expected Content, got nil")
	}
	if !strings.Contains(*r1.Content, "First session:") {
		t.Errorf("call 1: Content missing body: %q", *r1.Content)
	}
	if r1.DiscoveryHint == nil || !strings.Contains(*r1.DiscoveryHint, "Past sessions: 1 saved") {
		t.Errorf("call 1: DiscoveryHint should reflect N=1: %v", r1.DiscoveryHint)
	}
	if !strings.Contains(*r1.DiscoveryHint, "Last big picture: First session: locked the substrate spec.") {
		t.Errorf("call 1: DiscoveryHint missing line 2 with §1: %v", r1.DiscoveryHint)
	}

	// Permission invariants on first archive
	assertFilePerm0600(t, *r1.ArchivedPath)
	assertDirPerm0700(t, filepath.Dir(*r1.ArchivedPath))

	// === Call 2: second snapshot at a later saved_at → second archive ===
	writeArchiveSnapshot(t, srcPath, "2026-05-17T16:45:00.123Z", "Second session: wired RPC + discovery hint.")

	r2 := callArchive(t, h, agentID)
	if r2.ArchivedPath == nil {
		t.Fatal("call 2: expected ArchivedPath, got nil")
	}
	if *r2.ArchivedPath == *r1.ArchivedPath {
		t.Errorf("call 2: ArchivedPath duplicated call 1: %q", *r2.ArchivedPath)
	}
	if r2.BigPicture == nil || *r2.BigPicture != "Second session: wired RPC + discovery hint." {
		t.Errorf("call 2: BigPicture mismatch: got %v", r2.BigPicture)
	}
	if r2.DiscoveryHint == nil || !strings.Contains(*r2.DiscoveryHint, "Past sessions: 2 saved") {
		t.Errorf("call 2: DiscoveryHint should reflect N=2: %v", r2.DiscoveryHint)
	}

	// Permission invariants still hold
	assertFilePerm0600(t, *r2.ArchivedPath)
	assertDirPerm0700(t, filepath.Dir(*r2.ArchivedPath))

	// === Call 3: no snapshot → idempotency contract; DiscoveryHint still surfaces N=2 ===
	r3 := callArchive(t, h, agentID)
	if r3.ArchivedPath != nil {
		t.Errorf("call 3: expected nil ArchivedPath, got %v", *r3.ArchivedPath)
	}
	if r3.Content != nil {
		t.Errorf("call 3: expected nil Content, got %v", *r3.Content)
	}
	if r3.BigPicture != nil {
		t.Errorf("call 3: expected nil BigPicture, got %v", *r3.BigPicture)
	}
	if r3.DiscoveryHint == nil || !strings.Contains(*r3.DiscoveryHint, "Past sessions: 2 saved") {
		t.Errorf("call 3: DiscoveryHint should still reflect prior N=2: %v", r3.DiscoveryHint)
	}

	// === Verify sessions/ folder accumulated both archives ===
	sessionsDir := filepath.Dir(*r1.ArchivedPath)
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	var restartCount int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-restart.md") {
			restartCount++
		}
	}
	if restartCount != 2 {
		t.Errorf("sessions/ should hold both archives, got %d -restart.md files", restartCount)
	}

	// Filename ≡ mtime invariant (Q-Spec-4) end-to-end: the second
	// archive's filename should start with "20260517T164500" (the
	// saved_at frontmatter, stripped of colons + dots, in UTC).
	base2 := filepath.Base(*r2.ArchivedPath)
	if !strings.HasPrefix(base2, "20260517T164500") {
		t.Errorf("call 2: filename should start with saved_at timestamp, got %q", base2)
	}
}

// callArchive is a one-line wrapper around HandleArchive's JSON-RPC
// surface for less noise in the integration test body.
func callArchive(t *testing.T, h *SessionArchiveHandler, agentID string) *SessionArchiveResponse {
	t.Helper()
	params, _ := json.Marshal(SessionArchiveRequest{AgentID: agentID})
	resp, err := h.HandleArchive(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleArchive: %v", err)
	}
	r, ok := resp.(*SessionArchiveResponse)
	if !ok {
		t.Fatalf("expected *SessionArchiveResponse, got %T", resp)
	}
	return r
}

func assertFilePerm0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file %s mode = %o, want 0600", path, got)
	}
}

func assertDirPerm0700(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("dir %s mode = %o, want 0700", path, got)
	}
}
