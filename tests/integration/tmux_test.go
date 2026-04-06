//go:build integration

package integration

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	ttmux "github.com/leonletto/thrum/internal/tmux"
)

func TestTmuxSessionLifecycle(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	sessionName := "thrum-test-lifecycle"

	// Cleanup in case a previous run left the session
	_ = ttmux.KillSession(sessionName)

	// 1. Create session
	if err := ttmux.CreateSession(sessionName, t.TempDir()); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	defer func() { _ = ttmux.KillSession(sessionName) }()

	// 2. Verify session exists
	if !ttmux.HasSession(sessionName) {
		t.Fatal("session should exist after creation")
	}

	// 3. Send text
	target := sessionName + ":0.0"
	if err := ttmux.SendKeys(target, "echo hello-tmux-test"); err != nil {
		t.Fatalf("SendKeys: %v", err)
	}
	if err := ttmux.SendSpecialKey(target, "Enter"); err != nil {
		t.Fatalf("SendSpecialKey Enter: %v", err)
	}

	// 4. Brief pause for command to execute
	time.Sleep(500 * time.Millisecond)

	// 5. Capture pane content
	content, err := ttmux.CapturePane(target, 10)
	if err != nil {
		t.Fatalf("CapturePane: %v", err)
	}
	if !strings.Contains(content, "hello-tmux-test") {
		t.Errorf("captured content should contain 'hello-tmux-test', got:\n%s", content)
	}

	// 6. Kill session
	if err := ttmux.KillSession(sessionName); err != nil {
		t.Fatalf("KillSession: %v", err)
	}

	// 7. Verify session gone
	if ttmux.HasSession(sessionName) {
		t.Error("session should not exist after kill")
	}
}

func TestTmuxSanitizeAndDefault(t *testing.T) {
	// Unit-level tests that don't need tmux running
	name := ttmux.DefaultSessionName("implementer", "website-dev")
	if name != "implementer-website-dev" {
		t.Errorf("DefaultSessionName = %q, want %q", name, "implementer-website-dev")
	}

	sanitized := ttmux.SanitizeSessionName("has.dots and:colons")
	if strings.ContainsAny(sanitized, ".: ") {
		t.Errorf("SanitizeSessionName should remove unsafe chars, got %q", sanitized)
	}
}
