package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestIntegration_NotAWorktree_TextMode verifies the canonical
// tmux.create.not-a-worktree hint lands on stderr and the command exits
// non-zero when --cwd is not a git worktree.
func TestIntegration_NotAWorktree_TextMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "tmux", "create", "foo", "--cwd", "/tmp")
	_, errBuf := captureOut(cmd)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit, got success")
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "[tmux.create.not-a-worktree]") {
		t.Errorf("stderr missing hint code [tmux.create.not-a-worktree], got:\n%s", stderr)
	}
	if !strings.Contains(stderr, "warn") {
		t.Errorf("stderr missing 'warn' severity label, got:\n%s", stderr)
	}
}

// TestIntegration_NotAWorktree_ThrumNoHintsSuppresses verifies the
// env-var kill-switch suppresses the hint code from stderr even when the
// underlying condition fires. Abort still happens but quietly (no hint).
func TestIntegration_NotAWorktree_ThrumNoHintsSuppresses(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "tmux", "create", "foo", "--cwd", "/tmp")
	cmd.Env = append(cmd.Environ(), "THRUM_NO_HINTS=1")
	_, errBuf := captureOut(cmd)
	_ = cmd.Run()
	stderr := errBuf.String()
	if strings.Contains(stderr, "[tmux.create.not-a-worktree]") {
		t.Errorf("THRUM_NO_HINTS=1 must suppress hint code, got stderr:\n%s", stderr)
	}
}

// TestIntegration_NotAWorktree_JSONMode verifies --json produces a parseable
// body with hints[] populated, and that THRUM_NO_HINTS suppresses hints
// from the JSON body too.
func TestIntegration_NotAWorktree_JSONMode(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)

	// Positive: hints present.
	cmd := exec.Command(bin, "--json", "tmux", "create", "foo", "--cwd", "/tmp")
	outBuf, _ := captureOut(cmd)
	_ = cmd.Run()
	var body map[string]any
	if err := json.Unmarshal(outBuf.Bytes(), &body); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, outBuf.String())
	}
	hints, ok := body["hints"].([]any)
	if !ok {
		t.Fatalf("hints field missing or wrong type: %+v", body["hints"])
	}
	if len(hints) == 0 {
		t.Fatal("hints array empty")
	}
	first, _ := hints[0].(map[string]any)
	if first["code"] != "tmux.create.not-a-worktree" {
		t.Errorf("first hint code = %v, want tmux.create.not-a-worktree", first["code"])
	}
	if first["severity"] != "warn" {
		t.Errorf("first hint severity = %v, want warn", first["severity"])
	}

	// Suppression: hints should NOT appear with THRUM_NO_HINTS=1.
	cmd2 := exec.Command(bin, "--json", "tmux", "create", "foo", "--cwd", "/tmp")
	cmd2.Env = append(cmd2.Environ(), "THRUM_NO_HINTS=1")
	outBuf2, _ := captureOut(cmd2)
	_ = cmd2.Run()
	var body2 map[string]any
	if err := json.Unmarshal(outBuf2.Bytes(), &body2); err != nil {
		t.Fatalf("suppressed stdout not JSON: %v\n%s", err, outBuf2.String())
	}
	if _, present := body2["hints"]; present {
		t.Errorf("THRUM_NO_HINTS=1 must omit 'hints' key from JSON body, got: %+v", body2)
	}
}

// TestIntegration_NotAWorktree_QuietSuppressesText verifies --quiet
// suppresses the stderr hint trailer. The command still aborts (returns
// non-zero) — the suppression applies only to the hint text itself.
func TestIntegration_NotAWorktree_QuietSuppressesText(t *testing.T) {
	if testing.Short() {
		t.Skip("integration tests skipped in -short")
	}
	bin := buildTestBinary(t)
	cmd := exec.Command(bin, "--quiet", "tmux", "create", "foo", "--cwd", "/tmp")
	_, errBuf := captureOut(cmd)
	_ = cmd.Run()
	stderr := errBuf.String()
	if strings.Contains(stderr, "[tmux.create.not-a-worktree]") {
		t.Errorf("--quiet must suppress hint text, got stderr:\n%s", stderr)
	}
}
