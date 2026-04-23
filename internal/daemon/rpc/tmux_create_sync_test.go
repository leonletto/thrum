package rpc

import (
	"context"
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

// shrinkPollInterval sets identityPollInterval to a small value for
// the duration of a single test so the wait loop actually exercises
// multiple iterations under short budgets. Restores the production
// default via t.Cleanup.
func shrinkPollInterval(t *testing.T) {
	t.Helper()
	prev := identityPollInterval
	identityPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { identityPollInterval = prev })
}

func TestWaitForIdentityFile_ExistsImmediately_ReturnsNil(t *testing.T) {
	shrinkPollInterval(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")
	if err := os.WriteFile(path, []byte(`{"version":4}`), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	resendCount := int32(0)
	resend := func() error { atomic.AddInt32(&resendCount, 1); return nil }

	start := time.Now()
	if err := waitForIdentityFile(context.Background(), path, 500*time.Millisecond, 500*time.Millisecond, resend); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if atomic.LoadInt32(&resendCount) != 0 {
		t.Errorf("expected no resend when file already exists, got %d", resendCount)
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("expected near-instant return, took %v", elapsed)
	}
}

func TestWaitForIdentityFile_CreatedDuringInitialWindow_NoResend(t *testing.T) {
	shrinkPollInterval(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")

	resendCount := int32(0)
	resend := func() error { atomic.AddInt32(&resendCount, 1); return nil }

	// Create the file ~30ms into the initial wait — well within the
	// 200ms initial window and past the first poll tick.
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(path, []byte(`{"version":4}`), 0o600)
	}()

	if err := waitForIdentityFile(context.Background(), path, 200*time.Millisecond, 200*time.Millisecond, resend); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if atomic.LoadInt32(&resendCount) != 0 {
		t.Errorf("expected no resend (file appeared in initial window), got %d", resendCount)
	}
}

func TestWaitForIdentityFile_MissingInitial_ResendAndSucceed(t *testing.T) {
	shrinkPollInterval(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")

	resendCount := int32(0)
	resend := func() error {
		atomic.AddInt32(&resendCount, 1)
		// On resend, create the file — simulates shell swallowing
		// the first send and picking up the second.
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = os.WriteFile(path, []byte(`{"version":4}`), 0o600)
		}()
		return nil
	}

	if err := waitForIdentityFile(context.Background(), path, 100*time.Millisecond, 200*time.Millisecond, resend); err != nil {
		t.Fatalf("expected nil error after resend, got: %v", err)
	}
	if got := atomic.LoadInt32(&resendCount); got != 1 {
		t.Errorf("expected exactly one resend, got %d", got)
	}
}

func TestWaitForIdentityFile_NeverAppears_ReturnsTimeoutError(t *testing.T) {
	shrinkPollInterval(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")

	resendCount := int32(0)
	resend := func() error { atomic.AddInt32(&resendCount, 1); return nil }

	start := time.Now()
	err := waitForIdentityFile(context.Background(), path, 100*time.Millisecond, 100*time.Millisecond, resend)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if atomic.LoadInt32(&resendCount) != 1 {
		t.Errorf("expected exactly one resend attempt, got %d", resendCount)
	}
	// Full budget (100+100=200ms); allow modest slack. Upper bound
	// catches the bug where we overshoot by a full poll interval.
	if elapsed < 180*time.Millisecond {
		t.Errorf("expected wait to consume full budget, took %v", elapsed)
	}
	if elapsed > 300*time.Millisecond {
		t.Errorf("expected wait to stay close to full budget (200ms), overshot to %v", elapsed)
	}
}

func TestWaitForIdentityFile_ResendError_Propagates(t *testing.T) {
	shrinkPollInterval(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")
	resendErr := errors.New("send failed")
	resend := func() error { return resendErr }

	err := waitForIdentityFile(context.Background(), path, 50*time.Millisecond, 50*time.Millisecond, resend)
	if err == nil {
		t.Fatal("expected resend error to propagate, got nil")
	}
	if !errors.Is(err, resendErr) {
		t.Errorf("expected error to wrap %v, got: %v", resendErr, err)
	}
}

// TestRunInlineQuickstart_BlocksUntilIdentityFileExists is the
// RPC-boundary regression test. A future refactor that moves the
// identity wait back into a background goroutine would pass every
// waitForIdentityFile unit test while silently re-introducing the
// thrum-ns0b race. This test pins the invariant at the method that
// HandleCreate actually calls: the send-keys side-effect must have
// written the file before runInlineQuickstart returns.
//
// Uses the sendKeysFn / sendSpecialKeyFn package-var seams to
// simulate a shell that creates the identity file ~20ms after the
// quickstart command is received — a real shell runs much slower,
// but the shape is identical.
func TestRunInlineQuickstart_BlocksUntilIdentityFileExists(t *testing.T) {
	shrinkPollInterval(t)

	cwd := t.TempDir()
	idDir := filepath.Join(cwd, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	idPath := filepath.Join(idDir, "impl_x.json")

	// Swap in fakes that simulate the pane's shell running quickstart.
	// sendKeys writes the identity file on a delay, mimicking the
	// real async-shell behavior.
	prevSend := sendKeysFn
	prevEnter := sendSpecialKeyFn
	prevKill := killSessionFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevEnter
		killSessionFn = prevKill
	})

	var fileWrittenAtNS atomic.Int64 // unix nanos
	sendKeysFn = func(_, _ string) error {
		go func() {
			time.Sleep(30 * time.Millisecond)
			fileWrittenAtNS.Store(time.Now().UnixNano())
			_ = os.WriteFile(idPath, []byte(`{"version":4}`), 0o600)
		}()
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }
	killSessionFn = func(_ string) error {
		t.Error("killSessionFn must not be called on the happy path")
		return nil
	}

	h := NewTmuxHandler(t.TempDir(), nil)
	req := TmuxCreateRequest{
		AgentName: "impl_x",
		Role:      "implementer",
		Module:    "testing",
		Cwd:       cwd,
	}

	if err := h.runInlineQuickstart(context.Background(), req, "sess"); err != nil {
		t.Fatalf("runInlineQuickstart: %v", err)
	}
	runReturnedAtNS := time.Now().UnixNano()

	writtenNS := fileWrittenAtNS.Load()
	if writtenNS == 0 {
		t.Fatal("file was never written (the fake send-keys closure didn't run)")
	}
	if runReturnedAtNS < writtenNS {
		t.Errorf("regression: runInlineQuickstart returned at %d BEFORE the identity file appeared at %d — "+
			"this means the wait was async again (thrum-ns0b regression)",
			runReturnedAtNS, writtenNS)
	}
	if _, err := os.Stat(idPath); err != nil {
		t.Errorf("identity file should exist after runInlineQuickstart returns, got: %v", err)
	}
}

// TestRunInlineQuickstart_TimeoutKillsSessionAndErrors verifies the
// failure path: when the shell never writes the identity file, the
// method kills the session (so callers don't see a half-initialized
// pane) and returns a structured error.
func TestRunInlineQuickstart_TimeoutKillsSessionAndErrors(t *testing.T) {
	shrinkPollInterval(t)

	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".thrum", "identities"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	prevSend := sendKeysFn
	prevEnter := sendSpecialKeyFn
	prevKill := killSessionFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevEnter
		killSessionFn = prevKill
	})

	// Shrink the production 5s+5s budget to 30ms+30ms via a dedicated
	// test seam: use very small identityPollInterval + the
	// waitForIdentityFile timing is gated by the method's hardcoded
	// 5s values, so this test would take 10s without further factoring.
	// Instead: swap in a no-op sendKeysFn (file never appears) AND
	// cancel via ctx after 50ms.
	killedSession := ""
	sendKeysFn = func(_, _ string) error { return nil }
	sendSpecialKeyFn = func(_, _ string) error { return nil }
	killSessionFn = func(name string) error {
		killedSession = name
		return nil
	}

	h := NewTmuxHandler(t.TempDir(), nil)
	req := TmuxCreateRequest{
		AgentName: "impl_x", Role: "implementer", Module: "testing", Cwd: cwd,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := h.runInlineQuickstart(ctx, req, "sess-fail")
	if err == nil {
		t.Fatal("expected error when identity file never appears")
	}
	if killedSession != "sess-fail" {
		t.Errorf("expected session 'sess-fail' to be killed on timeout, killedSession=%q", killedSession)
	}
}

// TestWaitForIdentityFile_ContextCancelled_ReturnsPromptly verifies
// that a client disconnect / daemon shutdown mid-wait returns ctx.Err()
// instead of burning the full budget. Without ctx-awareness the whole
// 10s production deadline would have to elapse before the RPC returns.
func TestWaitForIdentityFile_ContextCancelled_ReturnsPromptly(t *testing.T) {
	shrinkPollInterval(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "impl_test.json")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	err := waitForIdentityFile(ctx, path, 5*time.Second, 5*time.Second, func() error {
		t.Error("resend should not be called when ctx cancels during initial wait")
		return nil
	})
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("expected prompt return on cancel, took %v (would have taken 10s without ctx-awareness)", elapsed)
	}
}
