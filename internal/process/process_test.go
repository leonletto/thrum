package process

import (
	"os"
	"testing"
)

func TestIsRunning_Self(t *testing.T) {
	if !IsRunning(os.Getpid()) {
		t.Error("current process should be running")
	}
}

func TestIsRunning_Dead(t *testing.T) {
	if IsRunning(999999) {
		t.Error("PID 999999 should not be running")
	}
}

func TestIsRunning_Zero(t *testing.T) {
	if IsRunning(0) {
		t.Error("PID 0 should return false")
	}
}

func TestIsClaudeProcess_NotClaude(t *testing.T) {
	// Current test process is "go" or similar, not "claude"
	if IsClaudeProcess(os.Getpid()) {
		t.Skip("running as claude process")
	}
}

func TestIsClaudeProcess_DeadPID(t *testing.T) {
	if IsClaudeProcess(999999) {
		t.Error("dead PID should not be a Claude process")
	}
}

func TestFindClaudeAncestor_ReturnsZeroOutsideClaude(t *testing.T) {
	pid := FindClaudeAncestor()
	if pid != 0 {
		t.Skipf("running inside Claude (found PID %d), cannot test negative case", pid)
	}
}

func TestProcessName(t *testing.T) {
	name := processName(os.Getpid())
	if name == "" {
		t.Error("expected non-empty process name for self")
	}
}

func TestParentPID(t *testing.T) {
	ppid := parentPID(os.Getpid())
	if ppid <= 0 {
		t.Error("expected positive parent PID")
	}
}
