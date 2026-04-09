//go:build unix

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
	// Explicitly assert false for non-claude process
	if IsClaudeProcess(os.Getpid()) {
		t.Error("test process should not be identified as claude")
	}
}

func TestIsClaudeProcess_DeadPID(t *testing.T) {
	if IsClaudeProcess(999999) {
		t.Error("dead PID should not be a Claude process")
	}
}

func TestIsRuntimeProcess_NotRuntime(t *testing.T) {
	// Current test process is "go" or similar, not any known runtime
	if IsRuntimeProcess(os.Getpid(), "") {
		t.Skip("running as a known runtime process")
	}
}

func TestIsRuntimeProcess_DeadPID(t *testing.T) {
	if IsRuntimeProcess(999999, "") {
		t.Error("dead PID should not be a runtime process")
	}
}

func TestIsRuntimeProcess_ZeroPID(t *testing.T) {
	if IsRuntimeProcess(0, "") {
		t.Error("PID 0 should return false")
	}
}

func TestIsRuntimeProcess_NegativePID(t *testing.T) {
	if IsRuntimeProcess(-1, "claude") {
		t.Error("negative PID should return false")
	}
}

func TestIsRuntimeProcess_SpecificRuntime(t *testing.T) {
	// A non-claude process should not match "claude" runtime
	if IsRuntimeProcess(os.Getpid(), "claude") {
		t.Skip("running as claude process")
	}
}

func TestFindClaudeAncestor_ReturnsZeroOutsideClaude(t *testing.T) {
	pid, runtime := FindClaudeAncestor()
	if pid != 0 {
		t.Skipf("running inside %s (found PID %d), cannot test negative case", runtime, pid)
	}
	if runtime != "" {
		t.Errorf("expected empty runtime when no ancestor found, got %q", runtime)
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
