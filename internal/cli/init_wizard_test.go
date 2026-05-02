package cli

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestTmuxGate_PassesWhenTmuxFound(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed in test env")
	}
	if err := tmuxGate(io.Discard); err != nil {
		t.Errorf("tmuxGate() = %v, want nil", err)
	}
}

func TestTmuxGate_ReturnsInstallMessageWhenMissing(t *testing.T) {
	// Stub PATH so tmux is not findable.
	t.Setenv("PATH", "/nonexistent")
	var buf bytes.Buffer
	err := tmuxGate(&buf)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"tmux is required", "brew install tmux", "port install tmux", "apt install tmux"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q:\n%s", want, msg)
		}
	}
}
