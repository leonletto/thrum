//go:build unix

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire first lock
	lock1, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer func() { _ = lock1.Release() }()

	// Verify lock file exists
	if _, err := os.Stat(lockPath); os.IsNotExist(err) {
		t.Fatal("lock file was not created")
	}

	// Try to acquire second lock - should fail
	_, err = AcquireLock(lockPath)
	if err == nil {
		t.Fatal("expected error when acquiring already-held lock")
	}

	if !strings.Contains(err.Error(), "lock held") {
		t.Fatalf("expected 'lock held' error, got: %v", err)
	}
}

func TestReleaseLock(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Acquire lock
	lock, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}

	// Release lock
	if err := lock.Release(); err != nil {
		t.Fatalf("failed to release lock: %v", err)
	}

	// Verify lock file was removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatal("lock file was not removed after release")
	}

	// Should be able to acquire lock again
	lock2, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("failed to acquire lock after release: %v", err)
	}
	defer func() { _ = lock2.Release() }()
}

func TestIsLocked(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "test.lock")

	// Check non-existent lock file
	if IsLocked(lockPath) {
		t.Fatal("expected non-existent lock file to not be locked")
	}

	// Acquire lock
	lock, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	// Check locked file
	if !IsLocked(lockPath) {
		t.Fatal("expected lock file to be locked")
	}

	// Release lock
	_ = lock.Release()

	// Check unlocked file
	if IsLocked(lockPath) {
		t.Fatal("expected lock file to not be locked after release")
	}
}

func TestLifecycleFlockPreventsDuplicateStart(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "t.lock")

	// Acquire lock directly (simulating first daemon holding it)
	lock1, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}
	defer func() { _ = lock1.Release() }()

	// Try to start lifecycle with same lock file - should fail immediately
	socketPath := filepath.Join(tmpDir, "t.sock")
	pidPath := filepath.Join(tmpDir, "t.pid")
	server := NewServer(socketPath)
	lifecycle := NewLifecycle(server, pidPath, nil, "")
	lifecycle.SetRepoInfo("/test/repo", socketPath)
	lifecycle.SetLockFile(lockPath) // Same lock file!

	err = lifecycle.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when starting daemon with held lock file")
	}

	if !strings.Contains(err.Error(), "lock held") {
		t.Fatalf("expected 'lock held' error, got: %v", err)
	}
}
