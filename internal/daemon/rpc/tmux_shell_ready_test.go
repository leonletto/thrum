package rpc

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// setupShellReadyEnv creates the .thrum/identities/ dir tree under a fresh
// tmpdir, writes a pre-existing identity file, and returns (cwd, idPath).
// Shared by the four ensureShellReadyAfterCreate tests so each one focuses
// on its specific timing / resend semantics rather than fixture wiring.
func setupShellReadyEnv(t *testing.T, agentName string) (cwd, idPath string) {
	t.Helper()
	cwd = t.TempDir()
	writeTestIdentityFile(t, cwd, agentName, 0, "old-session:0.0")
	idPath = filepath.Join(cwd, ".thrum", "identities", agentName+".json")
	return cwd, idPath
}

// shrinkShellReadyBudgets compresses the helper's initial+retry waits
// to single-digit millis so timeout / resend tests run in <100ms instead
// of the production 5+5s.
func shrinkShellReadyBudgets(t *testing.T) {
	t.Helper()
	prevInit, prevRetry := shellReadyInitialWait, shellReadyRetryWait
	shellReadyInitialWait = 30 * time.Millisecond
	shellReadyRetryWait = 30 * time.Millisecond
	t.Cleanup(func() {
		shellReadyInitialWait = prevInit
		shellReadyRetryWait = prevRetry
	})
}

// TestEnsureShellReadyAfterCreate_HappyPath pins thrum-8dl3 Regression A:
// after the first SendKeys lands (fake shell writes the identity file
// ~20ms later), the probe returns nil and the original identity file's
// backup is cleaned up.
func TestEnsureShellReadyAfterCreate_HappyPath(t *testing.T) {
	shrinkPollInterval(t)
	shrinkShellReadyBudgets(t)
	cwd, idPath := setupShellReadyEnv(t, "impl_test")

	prevSend, prevEnter := sendKeysFn, sendSpecialKeyFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevEnter
	})

	sendKeysFn = func(_, _ string) error {
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = os.WriteFile(idPath, []byte(`{"version":4,"agent":{"name":"impl_test"}}`), 0o600)
		}()
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	h := NewTmuxHandler(t.TempDir(), nil)
	if err := h.ensureShellReadyAfterCreate(context.Background(),
		"sess:0.0", cwd, "impl_test", "implementer", "test", "claude"); err != nil {
		t.Fatalf("ensureShellReadyAfterCreate: %v", err)
	}
	if _, err := os.Stat(idPath); err != nil {
		t.Errorf("identity file should exist after probe; got: %v", err)
	}
	if _, err := os.Stat(idPath + ".pre-restart-probe"); !os.IsNotExist(err) {
		t.Errorf("backup file should be cleaned up after successful probe; got err=%v", err)
	}
}

// TestEnsureShellReadyAfterCreate_FirstSendSwallowed_ResendSucceeds:
// shell-init eats the FIRST keystroke (zsh-init pattern). resend after
// the 5s initial-wait window writes the file. Probe still returns nil.
func TestEnsureShellReadyAfterCreate_FirstSendSwallowed_ResendSucceeds(t *testing.T) {
	shrinkPollInterval(t)
	shrinkShellReadyBudgets(t)
	cwd, idPath := setupShellReadyEnv(t, "impl_test")

	prevSend, prevEnter := sendKeysFn, sendSpecialKeyFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevEnter
	})

	var sendCount atomic.Int32
	// First send is a no-op (simulating swallowed keystroke during
	// shell-init); only the second send (the resend) writes the file.
	sendKeysFn = func(_, _ string) error {
		n := sendCount.Add(1)
		if n == 2 { // resend
			go func() {
				time.Sleep(5 * time.Millisecond)
				_ = os.WriteFile(idPath, []byte(`{"version":4,"agent":{"name":"impl_test"}}`), 0o600)
			}()
		}
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	h := NewTmuxHandler(t.TempDir(), nil)
	if err := h.ensureShellReadyAfterCreate(context.Background(),
		"sess:0.0", cwd, "impl_test", "implementer", "test", "claude"); err != nil {
		t.Fatalf("ensureShellReadyAfterCreate: %v", err)
	}
	if got := sendCount.Load(); got != 2 {
		t.Errorf("expected exactly 2 send-keys (initial + resend); got %d", got)
	}
}

// TestEnsureShellReadyAfterCreate_Timeout_RestoresBackup verifies the
// failure path: probe never produces a fresh file (shell totally dead).
// Helper returns an error AND the pre-existing identity file is restored
// from the .pre-restart-probe backup so the agent retains its identity.
func TestEnsureShellReadyAfterCreate_Timeout_RestoresBackup(t *testing.T) {
	shrinkPollInterval(t)
	shrinkShellReadyBudgets(t)
	cwd, idPath := setupShellReadyEnv(t, "impl_test")

	// Capture the pre-probe content so we can compare after timeout.
	preContent, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatal(err)
	}

	prevSend, prevEnter := sendKeysFn, sendSpecialKeyFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevEnter
	})

	sendKeysFn = func(_, _ string) error { return nil } // probe sent, nothing happens
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	// shrinkShellReadyBudgets sets both windows to 30ms, so the helper
	// times out after ~60ms total without needing a ctx deadline.
	h := NewTmuxHandler(t.TempDir(), nil)
	err = h.ensureShellReadyAfterCreate(context.Background(),
		"sess:0.0", cwd, "impl_test", "implementer", "test", "claude")
	if err == nil {
		t.Fatal("expected error on timeout; got nil")
	}

	// Backup MUST be restored to idPath on failure so the agent doesn't
	// lose its identity over a transient shell-init stall.
	got, err := os.ReadFile(idPath)
	if err != nil {
		t.Fatalf("identity file missing after failed probe; backup restore did not fire: %v", err)
	}
	if string(got) != string(preContent) {
		t.Errorf("identity file content changed after failed probe; backup restore should have preserved the pre-probe state.\nwant: %s\n got: %s", preContent, got)
	}
	// Backup file MUST be cleaned up after the restore (either renamed
	// back to idPath or removed).
	if _, err := os.Stat(idPath + ".pre-restart-probe"); !os.IsNotExist(err) {
		t.Errorf("backup file should be cleaned up after restore; got err=%v", err)
	}
}

// TestEnsureShellReadyAfterCreate_NoPreExistingFile: edge case where the
// identity file doesn't exist pre-probe (e.g. first restart of an agent
// whose prior identity got reaped). The helper should still work — the
// rename-aside no-ops, the probe writes a fresh file, success.
func TestEnsureShellReadyAfterCreate_NoPreExistingFile(t *testing.T) {
	shrinkPollInterval(t)
	shrinkShellReadyBudgets(t)
	cwd := t.TempDir()
	idDir := filepath.Join(cwd, ".thrum", "identities")
	if err := os.MkdirAll(idDir, 0o700); err != nil {
		t.Fatal(err)
	}
	idPath := filepath.Join(idDir, "impl_test.json")

	prevSend, prevEnter := sendKeysFn, sendSpecialKeyFn
	t.Cleanup(func() {
		sendKeysFn = prevSend
		sendSpecialKeyFn = prevEnter
	})

	sendKeysFn = func(_, _ string) error {
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = os.WriteFile(idPath, []byte(`{"version":4,"agent":{"name":"impl_test"}}`), 0o600)
		}()
		return nil
	}
	sendSpecialKeyFn = func(_, _ string) error { return nil }

	h := NewTmuxHandler(t.TempDir(), nil)
	if err := h.ensureShellReadyAfterCreate(context.Background(),
		"sess:0.0", cwd, "impl_test", "implementer", "test", "claude"); err != nil {
		t.Fatalf("ensureShellReadyAfterCreate (no pre-existing file): %v", err)
	}
	if _, err := os.Stat(idPath); err != nil {
		t.Errorf("identity file should exist after probe; got: %v", err)
	}
}
