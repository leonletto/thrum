package rpc

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// These tests cover the synchronous identity-file wait that HandleCreate
// uses to block until the inline quickstart has written the identity
// file. Prior to thrum-ns0b the wait was async (goroutine), which races
// with a back-to-back `thrum tmux launch` call and causes
// writeTmuxToIdentity to find zero identity files.

func TestWaitForIdentityFile_ExistsImmediately_ReturnsNil(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")
	if err := os.WriteFile(path, []byte(`{"version":4}`), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	resendCount := int32(0)
	resend := func() error { atomic.AddInt32(&resendCount, 1); return nil }

	start := time.Now()
	if err := waitForIdentityFile(path, 500*time.Millisecond, 500*time.Millisecond, resend); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if atomic.LoadInt32(&resendCount) != 0 {
		t.Errorf("expected no resend when file already exists, got %d", resendCount)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Errorf("expected near-instant return, took %v", elapsed)
	}
}

func TestWaitForIdentityFile_CreatedDuringInitialWindow_NoResend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")

	resendCount := int32(0)
	resend := func() error { atomic.AddInt32(&resendCount, 1); return nil }

	// Create the file ~200ms into the initial wait.
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{"version":4}`), 0o600)
	}()

	if err := waitForIdentityFile(path, 1*time.Second, 1*time.Second, resend); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if atomic.LoadInt32(&resendCount) != 0 {
		t.Errorf("expected no resend (file appeared in initial window), got %d", resendCount)
	}
}

func TestWaitForIdentityFile_MissingInitial_ResendAndSucceed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")

	resendCount := int32(0)
	resend := func() error {
		atomic.AddInt32(&resendCount, 1)
		// On resend, create the file (simulates shell swallowing the
		// first send and picking up the second).
		go func() {
			time.Sleep(50 * time.Millisecond)
			_ = os.WriteFile(path, []byte(`{"version":4}`), 0o600)
		}()
		return nil
	}

	if err := waitForIdentityFile(path, 200*time.Millisecond, 500*time.Millisecond, resend); err != nil {
		t.Fatalf("expected nil error after resend, got: %v", err)
	}
	if got := atomic.LoadInt32(&resendCount); got != 1 {
		t.Errorf("expected exactly one resend, got %d", got)
	}
}

func TestWaitForIdentityFile_NeverAppears_ReturnsTimeoutError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")

	resendCount := int32(0)
	resend := func() error { atomic.AddInt32(&resendCount, 1); return nil }

	start := time.Now()
	err := waitForIdentityFile(path, 100*time.Millisecond, 100*time.Millisecond, resend)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if atomic.LoadInt32(&resendCount) != 1 {
		t.Errorf("expected exactly one resend attempt, got %d", resendCount)
	}
	// Should take at least the full budget (initial + retry).
	if elapsed < 150*time.Millisecond {
		t.Errorf("expected wait to consume full budget (~200ms), took %v", elapsed)
	}
}

func TestWaitForIdentityFile_ResendError_Propagates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")
	resendErr := errors.New("send failed")
	resend := func() error { return resendErr }

	err := waitForIdentityFile(path, 50*time.Millisecond, 50*time.Millisecond, resend)
	if err == nil {
		t.Fatal("expected resend error to propagate, got nil")
	}
	if !errors.Is(err, resendErr) {
		t.Errorf("expected error to wrap %v, got: %v", resendErr, err)
	}
}
