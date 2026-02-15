package sync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSyncer(t *testing.T) {
	repoPath := "/test/repo"
	s := NewSyncer(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), false)

	if s == nil {
		t.Fatal("NewSyncer returned nil")
	}

	if s.repoPath != repoPath {
		t.Errorf("repoPath = %q, want %q", s.repoPath, repoPath)
	}

	if s.branchManager == nil {
		t.Error("branchManager is nil")
	}

	if s.merger == nil {
		t.Error("merger is nil")
	}
}

func TestSyncer_HasChanges_NoChanges(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	s := NewSyncer(repoPath, syncDir, false)

	// Commit the initial files in the sync worktree
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit", "--allow-empty")
	cmd.Dir = syncDir
	_ = cmd.Run()

	hasChanges, err := s.hasChanges(context.Background())
	if err != nil {
		t.Fatalf("hasChanges failed: %v", err)
	}

	if hasChanges {
		t.Error("hasChanges returned true, want false (no changes)")
	}
}

func TestSyncer_HasChanges_WithChanges(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	s := NewSyncer(repoPath, syncDir, false)

	// Commit the initial state in the worktree
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit", "--allow-empty")
	cmd.Dir = syncDir
	_ = cmd.Run()

	// Make a change to events.jsonl in the worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path from t.TempDir()
	if err != nil {
		t.Fatalf("failed to open events.jsonl: %v", err)
	}
	_, _ = f.WriteString(`{"type":"message.create","timestamp":"2026-02-03T10:00:00Z","message_id":"msg_001"}` + "\n")
	_ = f.Close()

	hasChanges, err := s.hasChanges(context.Background())
	if err != nil {
		t.Fatalf("hasChanges failed: %v", err)
	}

	if !hasChanges {
		t.Error("hasChanges returned false, want true (changes present)")
	}
}

func TestSyncer_StageChanges(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	s := NewSyncer(repoPath, syncDir, false)

	// Commit initial state in worktree
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit", "--allow-empty")
	cmd.Dir = syncDir
	_ = cmd.Run()

	// Make changes to multiple files in the worktree
	// 1. events.jsonl (core events)
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	eventsFile, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path from t.TempDir()
	if err != nil {
		t.Fatalf("failed to open events.jsonl: %v", err)
	}
	_, _ = eventsFile.WriteString(`{"type":"agent.register","timestamp":"2026-02-03T10:00:00Z","agent_id":"agent:test:ABC"}` + "\n")
	_ = eventsFile.Close()

	// 2. messages/alice.jsonl (per-agent message file)
	messagesDir := filepath.Join(syncDir, "messages")
	alicePath := filepath.Join(messagesDir, "alice.jsonl")
	aliceFile, err := os.OpenFile(alicePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path from t.TempDir()
	if err != nil {
		t.Fatalf("failed to open alice.jsonl: %v", err)
	}
	_, _ = aliceFile.WriteString(`{"type":"message.create","timestamp":"2026-02-03T10:01:00Z","message_id":"msg_001"}` + "\n")
	_ = aliceFile.Close()

	// 3. messages/bob.jsonl (another per-agent message file)
	bobPath := filepath.Join(messagesDir, "bob.jsonl")
	bobFile, err := os.OpenFile(bobPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path from t.TempDir()
	if err != nil {
		t.Fatalf("failed to open bob.jsonl: %v", err)
	}
	_, _ = bobFile.WriteString(`{"type":"message.create","timestamp":"2026-02-03T10:02:00Z","message_id":"msg_002"}` + "\n")
	_ = bobFile.Close()

	// Stage changes
	if err := s.stageChanges(context.Background()); err != nil {
		t.Fatalf("stageChanges failed: %v", err)
	}

	// Verify all files are staged
	cmd = exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git diff failed: %v", err)
	}

	stagedFiles := string(output)
	expectedFiles := []string{"events.jsonl", "messages/alice.jsonl", "messages/bob.jsonl"}
	for _, expected := range expectedFiles {
		if !strings.Contains(stagedFiles, expected) {
			t.Errorf("%s not staged; staged files:\n%s", expected, stagedFiles)
		}
	}
}

func TestSyncer_CommitChanges(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	s := NewSyncer(repoPath, syncDir, false)

	// Make a change in the worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path from t.TempDir()
	if err != nil {
		t.Fatalf("failed to open events.jsonl: %v", err)
	}
	_, _ = f.WriteString(`{"type":"message.create","timestamp":"2026-02-03T10:00:00Z","message_id":"msg_001"}` + "\n")
	_ = f.Close()

	_ = s.stageChanges(context.Background())

	// Commit
	commitMsg := "test: commit message"
	if err := s.commitChanges(context.Background(),commitMsg); err != nil {
		t.Fatalf("commitChanges failed: %v", err)
	}

	// Verify commit
	cmd := exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(output), commitMsg) {
		t.Errorf("commit message not found, got: %s", string(output))
	}
}

func TestSyncer_CommitChanges_NothingToCommit(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	s := NewSyncer(repoPath, syncDir, false)

	// Commit initial state so worktree is clean
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit", "--allow-empty")
	cmd.Dir = syncDir
	_ = cmd.Run()

	// Commit without staging anything — should not error
	err := s.commitChanges(context.Background(),"test: empty commit")
	if err != nil {
		t.Errorf("commitChanges should not fail when nothing to commit: %v", err)
	}
}

func TestSyncer_Push_NoRemote(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	s := NewSyncer(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), false)

	// Should succeed (no-op) when no remote
	if err := s.push(context.Background()); err != nil {
		t.Errorf("push failed with no remote: %v", err)
	}
}

func TestSyncer_Push_WithRemote(t *testing.T) {
	// Create a bare remote repository
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create bare remote: %v", err)
	}

	// Create local repository with worktree
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Add remote to main repo
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add remote: %v", err)
	}

	// Make a commit in the worktree
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "test commit")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	s := NewSyncer(repoPath, syncDir, false)

	// Push should succeed
	if err := s.push(context.Background()); err != nil {
		t.Errorf("push failed: %v", err)
	}
}

func TestIsPushRejected(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "rejected",
			err:      &PushError{Output: "rejected"},
			expected: true,
		},
		{
			name:     "non-fast-forward",
			err:      &PushError{Output: "non-fast-forward"},
			expected: true,
		},
		{
			name:     "fetch first",
			err:      &PushError{Output: "fetch first"},
			expected: true,
		},
		{
			name:     "updates were rejected",
			err:      &PushError{Output: "updates were rejected"},
			expected: true,
		},
		{
			name:     "other error",
			err:      &PushError{Output: "some other error"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isPushRejected(tt.err)
			if result != tt.expected {
				t.Errorf("isPushRejected() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestSyncer_EnsureOnSyncBranch - REMOVED: Method no longer exists with worktree architecture.
// Worktree is always checked out on a-sync branch at .git/thrum-sync/a-sync/, no branch switching needed.
//
// func TestSyncer_EnsureOnSyncBranch(t *testing.T) { ... }

func TestSyncer_CommitAndPush_NoChanges(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	s := NewSyncer(repoPath, syncDir, false)

	// Commit initial state in worktree
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit", "--allow-empty")
	cmd.Dir = syncDir
	_ = cmd.Run()

	// CommitAndPush should succeed with no changes
	if err := s.CommitAndPush(context.Background()); err != nil {
		t.Errorf("CommitAndPush failed with no changes: %v", err)
	}
}

func TestSyncer_CommitAndPush_WithChanges(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")
	s := NewSyncer(repoPath, syncDir, false)

	// Commit initial state in worktree
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit", "--allow-empty")
	cmd.Dir = syncDir
	_ = cmd.Run()

	// Make a change in the worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path from t.TempDir()
	if err != nil {
		t.Fatalf("failed to open events.jsonl: %v", err)
	}
	_, _ = f.WriteString(`{"type":"message.create","timestamp":"2026-02-03T10:00:00Z","message_id":"msg_001"}` + "\n")
	_ = f.Close()

	// CommitAndPush should succeed
	if err := s.CommitAndPush(context.Background()); err != nil {
		t.Errorf("CommitAndPush failed: %v", err)
	}

	// Verify commit was created
	cmd = exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !strings.Contains(string(output), "sync:") {
		t.Errorf("commit message doesn't contain 'sync:', got: %s", string(output))
	}
}

// TestSyncer_SwitchToMainBranch - REMOVED: Method no longer exists with worktree architecture.
// No branch switching is needed; main repo stays on its branch, sync happens in .git/thrum-sync/a-sync/ worktree.
//
// func TestSyncer_SwitchToMainBranch(t *testing.T) { ... }

func TestSyncer_GetSyncBranchRef(t *testing.T) {
	repoPath := setupMergeTestRepo(t)
	s := NewSyncer(repoPath, filepath.Join(repoPath, ".git", "thrum-sync", "a-sync"), false)

	ref, err := s.GetSyncBranchRef()
	if err != nil {
		t.Fatalf("GetSyncBranchRef failed: %v", err)
	}

	if ref == "" {
		t.Error("GetSyncBranchRef returned empty ref")
	}

	if len(ref) != 40 {
		t.Errorf("ref length = %d, want 40 (git commit hash)", len(ref))
	}
}

func TestPushError_Error(t *testing.T) {
	err := &PushError{
		Err:    os.ErrNotExist,
		Output: "test output",
	}

	errStr := err.Error()
	if !strings.Contains(errStr, "push failed") {
		t.Errorf("error string should contain 'push failed', got: %s", errStr)
	}

	if !strings.Contains(errStr, "test output") {
		t.Errorf("error string should contain output, got: %s", errStr)
	}
}

func TestPushError_Unwrap(t *testing.T) {
	innerErr := os.ErrNotExist
	err := &PushError{
		Err:    innerErr,
		Output: "test",
	}

	unwrapped := err.Unwrap()
	if unwrapped != innerErr {
		t.Errorf("Unwrap() returned %v, want %v", unwrapped, innerErr)
	}
}

func TestSyncer_Push_LocalOnly(t *testing.T) {
	// Create a repo with a remote configured
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create bare remote: %v", err)
	}

	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add remote: %v", err)
	}

	// Make a commit so there's something to push
	cmd = exec.Command("git", "commit", "--allow-empty", "-m", "test commit")
	cmd.Dir = syncDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("commit failed: %v", err)
	}

	// Create syncer with localOnly=true
	s := NewSyncer(repoPath, syncDir, true)

	// push should return nil immediately (skip) even though remote exists
	if err := s.push(context.Background()); err != nil {
		t.Errorf("push should succeed (no-op) in local-only mode: %v", err)
	}

	// Verify nothing was actually pushed to remote
	cmd = exec.Command("git", "branch", "-r")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git branch -r failed: %v", err)
	}
	if strings.Contains(string(output), SyncBranchName) {
		t.Error("a-sync should NOT have been pushed to remote in local-only mode")
	}
}

func TestSyncer_CommitAndPush_LocalOnly_CommitsButDoesNotPush(t *testing.T) {
	// Create a repo with a remote configured
	remoteDir := t.TempDir()
	cmd := exec.Command("git", "init", "--bare")
	cmd.Dir = remoteDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to create bare remote: %v", err)
	}

	repoPath := setupMergeTestRepo(t)
	syncDir := filepath.Join(repoPath, ".git", "thrum-sync", "a-sync")

	// Add remote
	cmd = exec.Command("git", "remote", "add", "origin", remoteDir) //nolint:gosec // G204 test uses controlled paths
	cmd.Dir = repoPath
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to add remote: %v", err)
	}

	// Commit initial state in worktree
	cmd = exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()

	cmd = exec.Command("git", "commit", "-m", "Initial commit", "--allow-empty")
	cmd.Dir = syncDir
	_ = cmd.Run()

	// Make a change in the worktree
	eventsPath := filepath.Join(syncDir, "events.jsonl")
	f, err := os.OpenFile(eventsPath, os.O_APPEND|os.O_WRONLY, 0600) //nolint:gosec // G304 - test path from t.TempDir()
	if err != nil {
		t.Fatalf("failed to open events.jsonl: %v", err)
	}
	_, _ = f.WriteString(`{"type":"message.create","timestamp":"2026-02-10T10:00:00Z","event_id":"evt_LOCAL","message_id":"msg_001"}` + "\n")
	_ = f.Close()

	// Create syncer with localOnly=true
	s := NewSyncer(repoPath, syncDir, true)

	// CommitAndPush should succeed — commits locally, skips push
	if err := s.CommitAndPush(context.Background()); err != nil {
		t.Fatalf("CommitAndPush failed in local-only mode: %v", err)
	}

	// Verify commit was created locally
	cmd = exec.Command("git", "log", "-1", "--format=%s")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}
	if !strings.Contains(string(output), "sync:") {
		t.Errorf("expected local commit with 'sync:' prefix, got: %s", string(output))
	}

	// Verify nothing was pushed to remote
	cmd = exec.Command("git", "branch", "-r")
	cmd.Dir = repoPath
	output, err = cmd.Output()
	if err != nil {
		t.Fatalf("git branch -r failed: %v", err)
	}
	if strings.Contains(string(output), SyncBranchName) {
		t.Error("a-sync should NOT have been pushed to remote in local-only mode")
	}
}

func TestNewSyncer_LocalOnly(t *testing.T) {
	s := NewSyncer("/test/repo", "/test/repo/.git/thrum-sync/a-sync", true)
	if !s.localOnly {
		t.Error("expected localOnly=true")
	}
	if !s.merger.localOnly {
		t.Error("expected merger.localOnly=true")
	}
	if !s.branchManager.localOnly {
		t.Error("expected branchManager.localOnly=true")
	}
}

// TestSyncer_WriteMessageToJSONL - REMOVED: Method removed as it was a stub.
// Message writing should use internal/jsonl Writer directly.
//
// func TestSyncer_WriteMessageToJSONL(t *testing.T) { ... }
