package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	syncpkg "github.com/leonletto/thrum/internal/sync"
)

func TestMigrate_OldLayout(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize git repo with initial commit
	initGitRepo(t, tmpDir)

	// Create old-layout .thrum/ files
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatalf("Failed to create .thrum/: %v", err)
	}

	eventsData := `{"event_id":"e1","type":"test"}` + "\n" + `{"event_id":"e2","type":"test"}` + "\n"
	messagesData := `{"msg_id":"m1","text":"hello"}` + "\n" + `{"msg_id":"m2","text":"world"}` + "\n"

	if err := os.WriteFile(filepath.Join(thrumDir, "events.jsonl"), []byte(eventsData), 0600); err != nil {
		t.Fatalf("Failed to write events.jsonl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "messages.jsonl"), []byte(messagesData), 0600); err != nil {
		t.Fatalf("Failed to write messages.jsonl: %v", err)
	}
	if err := os.WriteFile(filepath.Join(thrumDir, "schema_version"), []byte("1\n"), 0600); err != nil {
		t.Fatalf("Failed to write schema_version: %v", err)
	}

	// Add old .gitignore with .thrum/var/
	gitignoreContent := "# Test\nnode_modules/\n.thrum/var/\n.thrum.*.json\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(gitignoreContent), 0600); err != nil {
		t.Fatalf("Failed to write .gitignore: %v", err)
	}

	// Add old .gitattributes with merge=union rules
	gitattrsContent := "\n# Use bd merge for beads JSONL files\n.beads/issues.jsonl merge=beads\n\n# Use union merge for thrum JSONL files (dedup by event_id)\n.thrum/events.jsonl merge=union\n.thrum/messages/*.jsonl merge=union\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitattributes"), []byte(gitattrsContent), 0600); err != nil {
		t.Fatalf("Failed to write .gitattributes: %v", err)
	}

	// Commit old-layout files to git (so they're tracked on main)
	cmd := exec.Command("git", "add", ".thrum/events.jsonl", ".thrum/messages.jsonl", ".thrum/schema_version", ".gitignore", ".gitattributes")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add old files: %v", err)
	}

	cmd = exec.Command("git", "commit", "-m", "Add old thrum layout")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit old layout: %v", err)
	}

	// Verify files are tracked before migration
	cmd = exec.Command("git", "ls-files", ".thrum/events.jsonl")
	cmd.Dir = tmpDir
	output, _ := cmd.Output()
	if strings.TrimSpace(string(output)) == "" {
		t.Fatal("events.jsonl should be tracked by git before migration")
	}

	// Run migration
	err := Migrate(tmpDir)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	// Verify: sync worktree exists at the new location
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")
	gitFile := filepath.Join(syncDir, ".git")
	info, err := os.Stat(gitFile)
	if err != nil {
		t.Errorf("sync worktree .git file does not exist: %v", err)
	} else if info.IsDir() {
		t.Error("sync worktree .git should be a file, not a directory")
	}

	// Verify: worktree is on a-sync branch
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = syncDir
	output, err = cmd.Output()
	if err != nil {
		t.Errorf("Failed to check branch in sync worktree: %v", err)
	} else if strings.TrimSpace(string(output)) != "a-sync" {
		t.Errorf("Sync worktree on wrong branch: got %q, want %q", strings.TrimSpace(string(output)), "a-sync")
	}

	// Verify: events.jsonl was copied to sync worktree
	syncEvents, err := os.ReadFile(filepath.Join(syncDir, "events.jsonl")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Errorf("Failed to read events.jsonl from sync: %v", err)
	} else if string(syncEvents) != eventsData {
		t.Errorf("events.jsonl content mismatch in sync:\ngot:  %q\nwant: %q", string(syncEvents), eventsData)
	}

	// Verify: messages.jsonl was copied to sync worktree
	syncMessages, err := os.ReadFile(filepath.Join(syncDir, "messages.jsonl")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Errorf("Failed to read messages.jsonl from sync: %v", err)
	} else if string(syncMessages) != messagesData {
		t.Errorf("messages.jsonl content mismatch in sync:\ngot:  %q\nwant: %q", string(syncMessages), messagesData)
	}

	// Verify: files are no longer tracked by git on main
	cmd = exec.Command("git", "ls-files", ".thrum/events.jsonl")
	cmd.Dir = tmpDir
	output, _ = cmd.Output()
	if strings.TrimSpace(string(output)) != "" {
		t.Error("events.jsonl should NOT be tracked by git on main after migration")
	}

	cmd = exec.Command("git", "ls-files", ".thrum/messages.jsonl")
	cmd.Dir = tmpDir
	output, _ = cmd.Output()
	if strings.TrimSpace(string(output)) != "" {
		t.Error("messages.jsonl should NOT be tracked by git on main after migration")
	}

	// Verify: .gitignore has .thrum/ (not .thrum/var/)
	gitignore, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}
	gitignoreStr := string(gitignore)
	if !containsLine(gitignoreStr, ".thrum/") {
		t.Error(".gitignore should contain '.thrum/' line")
	}
	if containsLine(gitignoreStr, ".thrum/var/") {
		t.Error(".gitignore should NOT contain '.thrum/var/' after migration")
	}

	// Verify: .gitattributes no longer has thrum merge=union entries
	gitattrs, err := os.ReadFile(filepath.Join(tmpDir, ".gitattributes")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read .gitattributes: %v", err)
	}
	gitattrsStr := string(gitattrs)
	if strings.Contains(gitattrsStr, ".thrum/events.jsonl merge=union") {
		t.Error(".gitattributes should NOT contain .thrum/events.jsonl merge=union after migration")
	}
	if strings.Contains(gitattrsStr, ".thrum/messages/*.jsonl merge=union") {
		t.Error(".gitattributes should NOT contain .thrum/messages/*.jsonl merge=union after migration")
	}
	// Beads entry should be preserved
	if !strings.Contains(gitattrsStr, ".beads/issues.jsonl merge=beads") {
		t.Error(".gitattributes should still contain beads merge entry")
	}
}

func TestMigrate_WithShardedMessages(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	thrumDir := filepath.Join(tmpDir, ".thrum")
	messagesDir := filepath.Join(thrumDir, "messages")
	if err := os.MkdirAll(messagesDir, 0750); err != nil {
		t.Fatalf("Failed to create messages dir: %v", err)
	}

	// Create sharded message files
	shard1 := `{"msg_id":"s1m1"}` + "\n"
	shard2 := `{"msg_id":"s2m1"}` + "\n"
	if err := os.WriteFile(filepath.Join(messagesDir, "shard-001.jsonl"), []byte(shard1), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(messagesDir, "shard-002.jsonl"), []byte(shard2), 0600); err != nil {
		t.Fatal(err)
	}

	// Also create a backup file that should NOT be copied
	if err := os.WriteFile(filepath.Join(thrumDir, "messages.jsonl.v6.bak"), []byte("backup"), 0600); err != nil {
		t.Fatal(err)
	}

	// Commit the message shards to git
	cmd := exec.Command("git", "add", ".thrum/messages/")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to git add messages: %v", err)
	}
	cmd = exec.Command("git", "commit", "-m", "Add sharded messages")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	// Run migration
	err := Migrate(tmpDir)
	if err != nil {
		t.Fatalf("Migrate failed: %v", err)
	}

	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	// Verify sharded files were copied
	syncShard1, err := os.ReadFile(filepath.Join(syncDir, "messages", "shard-001.jsonl")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Errorf("Failed to read shard-001 from sync: %v", err)
	} else if string(syncShard1) != shard1 {
		t.Errorf("shard-001 content mismatch: got %q, want %q", string(syncShard1), shard1)
	}

	syncShard2, err := os.ReadFile(filepath.Join(syncDir, "messages", "shard-002.jsonl")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Errorf("Failed to read shard-002 from sync: %v", err)
	} else if string(syncShard2) != shard2 {
		t.Errorf("shard-002 content mismatch: got %q, want %q", string(syncShard2), shard2)
	}

	// Verify backup file was NOT copied
	if _, err := os.Stat(filepath.Join(syncDir, "messages.jsonl.v6.bak")); err == nil {
		t.Error("backup file should NOT have been copied to sync worktree")
	}
}

func TestMigrate_AlreadyMigrated(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	// First, do a full init (creates worktree at .git/thrum-sync/a-sync)
	opts := InitOptions{
		RepoPath: tmpDir,
		Force:    false,
	}
	if err := Init(opts); err != nil {
		t.Fatalf("Init failed: %v", err)
	}

	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")

	// Write some data into the sync worktree to verify it's preserved
	testData := `{"event_id":"preserved"}` + "\n"
	if err := os.WriteFile(filepath.Join(syncDir, "events.jsonl"), []byte(testData), 0600); err != nil {
		t.Fatalf("Failed to write test data: %v", err)
	}

	// Commit the test data
	cmd := exec.Command("git", "add", ".")
	cmd.Dir = syncDir
	_ = cmd.Run()
	cmd = exec.Command("git", "commit", "-m", "test data")
	cmd.Dir = syncDir
	_ = cmd.Run()

	// Run migrate — should print "No migration needed" and not harm anything
	err := Migrate(tmpDir)
	if err != nil {
		t.Fatalf("Migrate on already-migrated repo failed: %v", err)
	}

	// Verify sync worktree still exists and data is preserved
	events, err := os.ReadFile(filepath.Join(syncDir, "events.jsonl")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Errorf("events.jsonl missing after migrate: %v", err)
	} else if string(events) != testData {
		t.Errorf("events.jsonl content changed after migrate: got %q, want %q", string(events), testData)
	}

	// Verify worktree is still valid
	cmd = exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = syncDir
	output, err := cmd.Output()
	if err != nil {
		t.Errorf("Failed to check branch after migrate: %v", err)
	} else if strings.TrimSpace(string(output)) != "a-sync" {
		t.Errorf("Wrong branch after migrate: got %q, want %q", strings.TrimSpace(string(output)), "a-sync")
	}
}

func TestMigrate_NoMigrationNeeded(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	// Repo has no .thrum/ files at all — migration should be a no-op
	err := Migrate(tmpDir)
	if err != nil {
		t.Fatalf("Migrate on clean repo failed: %v", err)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	eventsData := `{"event_id":"e1"}` + "\n"
	if err := os.WriteFile(filepath.Join(thrumDir, "events.jsonl"), []byte(eventsData), 0600); err != nil {
		t.Fatal(err)
	}

	// Add .gitignore with old entry
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(".thrum/var/\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Commit to git
	cmd := exec.Command("git", "add", ".thrum/events.jsonl", ".gitignore")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("git", "commit", "-m", "old layout")
	cmd.Dir = tmpDir
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	// Run migration twice
	if err := Migrate(tmpDir); err != nil {
		t.Fatalf("First migrate failed: %v", err)
	}

	if err := Migrate(tmpDir); err != nil {
		t.Fatalf("Second migrate failed: %v", err)
	}

	// Verify state is consistent after two runs
	syncDir := filepath.Join(tmpDir, ".git", "thrum-sync", "a-sync")
	syncEvents, err := os.ReadFile(filepath.Join(syncDir, "events.jsonl")) //nolint:gosec // G304 - test fixture path
	if err != nil {
		t.Fatalf("Failed to read events from sync: %v", err)
	}
	if string(syncEvents) != eventsData {
		t.Errorf("events data changed after second migrate: got %q, want %q", string(syncEvents), eventsData)
	}
}

func TestMigrateGitignore(t *testing.T) {
	t.Run("replaces .thrum/var/ with .thrum/", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := "node_modules/\n.thrum/var/\n.thrum.*.json\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}

		updated, err := migrateGitignore(tmpDir)
		if err != nil {
			t.Fatalf("migrateGitignore failed: %v", err)
		}
		if !updated {
			t.Error("expected .gitignore to be updated")
		}

		result, _ := os.ReadFile(filepath.Join(tmpDir, ".gitignore")) //nolint:gosec // G304 - test fixture path
		if !containsLine(string(result), ".thrum/") {
			t.Error("should contain .thrum/")
		}
		if containsLine(string(result), ".thrum/var/") {
			t.Error("should NOT contain .thrum/var/")
		}
	})

	t.Run("no change if .thrum/ already present", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := "node_modules/\n.thrum/\n.thrum.*.json\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}

		updated, err := migrateGitignore(tmpDir)
		if err != nil {
			t.Fatalf("migrateGitignore failed: %v", err)
		}
		if updated {
			t.Error("expected no change when .thrum/ already present")
		}
	})
}

func TestCleanGitattributes(t *testing.T) {
	t.Run("removes stale thrum entries", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := "\n# Use bd merge for beads JSONL files\n.beads/issues.jsonl merge=beads\n\n# Use union merge for thrum JSONL files (dedup by event_id)\n.thrum/events.jsonl merge=union\n.thrum/messages/*.jsonl merge=union\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".gitattributes"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}

		updated, err := cleanGitattributes(tmpDir)
		if err != nil {
			t.Fatalf("cleanGitattributes failed: %v", err)
		}
		if !updated {
			t.Error("expected .gitattributes to be updated")
		}

		result, _ := os.ReadFile(filepath.Join(tmpDir, ".gitattributes")) //nolint:gosec // G304 - test fixture path
		resultStr := string(result)
		if strings.Contains(resultStr, ".thrum/events.jsonl merge=union") {
			t.Error("should not contain thrum events merge rule")
		}
		if strings.Contains(resultStr, ".thrum/messages/*.jsonl merge=union") {
			t.Error("should not contain thrum messages merge rule")
		}
		if !strings.Contains(resultStr, ".beads/issues.jsonl merge=beads") {
			t.Error("should preserve beads merge rule")
		}
	})

	t.Run("no change when no stale entries", func(t *testing.T) {
		tmpDir := t.TempDir()
		content := "# Beads\n.beads/issues.jsonl merge=beads\n"
		if err := os.WriteFile(filepath.Join(tmpDir, ".gitattributes"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}

		updated, err := cleanGitattributes(tmpDir)
		if err != nil {
			t.Fatalf("cleanGitattributes failed: %v", err)
		}
		if updated {
			t.Error("expected no change")
		}
	})

	t.Run("no error when .gitattributes missing", func(t *testing.T) {
		tmpDir := t.TempDir()
		updated, err := cleanGitattributes(tmpDir)
		if err != nil {
			t.Fatalf("cleanGitattributes failed: %v", err)
		}
		if updated {
			t.Error("expected no change")
		}
	})
}

func TestMigrateSyncWorktreeLocation_OldExists(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	// Create old-style worktree at .thrum/sync/
	bm := newTestBranchManager(t, tmpDir)
	oldSyncDir := filepath.Join(thrumDir, "sync")
	if err := bm.CreateSyncWorktree(oldSyncDir); err != nil {
		t.Fatalf("Failed to create old worktree: %v", err)
	}

	// Verify old worktree exists
	if _, err := os.Stat(filepath.Join(oldSyncDir, ".git")); err != nil {
		t.Fatalf("Old worktree .git file doesn't exist: %v", err)
	}

	// Run migration
	if err := MigrateSyncWorktreeLocation(tmpDir, thrumDir); err != nil {
		t.Fatalf("MigrateSyncWorktreeLocation failed: %v", err)
	}

	// Verify old worktree was removed
	if _, err := os.Stat(oldSyncDir); !os.IsNotExist(err) {
		t.Error("old sync directory should have been removed")
	}
}

func TestMigrateSyncWorktreeLocation_NoOldWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	// No old worktree — should be a no-op
	if err := MigrateSyncWorktreeLocation(tmpDir, thrumDir); err != nil {
		t.Fatalf("MigrateSyncWorktreeLocation failed: %v", err)
	}
}

func TestMigrateSyncWorktreeLocation_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(thrumDir, 0750); err != nil {
		t.Fatal(err)
	}

	// Create old-style worktree
	bm := newTestBranchManager(t, tmpDir)
	oldSyncDir := filepath.Join(thrumDir, "sync")
	if err := bm.CreateSyncWorktree(oldSyncDir); err != nil {
		t.Fatalf("Failed to create old worktree: %v", err)
	}

	// Run migration twice — second should be a no-op
	if err := MigrateSyncWorktreeLocation(tmpDir, thrumDir); err != nil {
		t.Fatalf("First MigrateSyncWorktreeLocation failed: %v", err)
	}
	if err := MigrateSyncWorktreeLocation(tmpDir, thrumDir); err != nil {
		t.Fatalf("Second MigrateSyncWorktreeLocation failed: %v", err)
	}
}

// newTestBranchManager creates a BranchManager for testing, ensuring a-sync branch exists.
func newTestBranchManager(t *testing.T, repoPath string) *syncpkg.BranchManager {
	t.Helper()
	bm := syncpkg.NewBranchManager(repoPath)
	if err := bm.CreateSyncBranch(); err != nil {
		t.Fatalf("CreateSyncBranch failed: %v", err)
	}
	return bm
}

// containsLine checks if a string contains a given line (exact match, trimmed).
func containsLine(content, target string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == target {
			return true
		}
	}
	return false
}
