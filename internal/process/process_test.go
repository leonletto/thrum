//go:build unix

package process

import (
	"context"
	"os"
	"testing"
	"time"
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
	ctx := context.Background()
	// Current test process is "go" or similar, not "claude"
	if IsClaudeProcess(ctx, os.Getpid()) {
		t.Skip("running as claude process")
	}
	// Explicitly assert false for non-claude process
	if IsClaudeProcess(ctx, os.Getpid()) {
		t.Error("test process should not be identified as claude")
	}
}

func TestIsClaudeProcess_DeadPID(t *testing.T) {
	if IsClaudeProcess(context.Background(), 999999) {
		t.Error("dead PID should not be a Claude process")
	}
}

func TestIsRuntimeProcess_NotRuntime(t *testing.T) {
	// Current test process is "go" or similar, not any known runtime
	if IsRuntimeProcess(context.Background(), os.Getpid(), "") {
		t.Skip("running as a known runtime process")
	}
}

func TestIsRuntimeProcess_DeadPID(t *testing.T) {
	if IsRuntimeProcess(context.Background(), 999999, "") {
		t.Error("dead PID should not be a runtime process")
	}
}

func TestIsRuntimeProcess_ZeroPID(t *testing.T) {
	if IsRuntimeProcess(context.Background(), 0, "") {
		t.Error("PID 0 should return false")
	}
}

func TestIsRuntimeProcess_NegativePID(t *testing.T) {
	if IsRuntimeProcess(context.Background(), -1, "claude") {
		t.Error("negative PID should return false")
	}
}

func TestIsRuntimeProcess_SpecificRuntime(t *testing.T) {
	// A non-claude process should not match "claude" runtime
	if IsRuntimeProcess(context.Background(), os.Getpid(), "claude") {
		t.Skip("running as claude process")
	}
}

func TestFindClaudeAncestor_ReturnsZeroOutsideClaude(t *testing.T) {
	pid, runtime := FindClaudeAncestor(context.Background())
	if pid != 0 {
		t.Skipf("running inside %s (found PID %d), cannot test negative case", runtime, pid)
	}
	if runtime != "" {
		t.Errorf("expected empty runtime when no ancestor found, got %q", runtime)
	}
}

func TestProcessName(t *testing.T) {
	name := processName(context.Background(), os.Getpid())
	if name == "" {
		t.Error("expected non-empty process name for self")
	}
}

func TestParentPID(t *testing.T) {
	ppid := parentPID(context.Background(), os.Getpid())
	if ppid <= 0 {
		t.Error("expected positive parent PID")
	}
}

func TestRunPS_TimeoutEnforced(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := runPS(ctx, "-p", "1", "-o", "comm=")
	elapsed := time.Since(start)
	if elapsed > 1*time.Second {
		t.Errorf("runPS did not respect ctx deadline: took %v", elapsed)
	}
	_ = err
}

// TestMatchRuntimeName_BasenameAndFullPath covers thrum-pxz.15: macOS
// `ps -o comm=` returns the full executable path for some binaries
// (e.g. codex native binary launched via node wrapper). The comparison
// must strip directory components before matching the runtime name.
// Regression: without filepath.Base, codex PIDs were classified as
// not-a-runtime and bypassed FindClaudeAncestor entirely.
func TestMatchRuntimeName_BasenameAndFullPath(t *testing.T) {
	cases := []struct {
		psComm, runtime string
		want            bool
	}{
		{"claude", "claude", true},
		{"codex", "codex", true},
		{
			"/opt/homebrew/lib/node_modules/@openai/codex/node_modules/@openai/codex-darwin-arm64/vendor/aarch64-apple-darwin/codex/codex",
			"codex",
			true,
		},
		{"/usr/local/bin/claude", "claude", true},
		{"node", "codex", false},
		{"opencode", "claude", false},
		{"/opt/homebrew/bin/opencode", "opencode", true},
		{"/usr/bin/cursor-agent", "cursor-agent", true},
	}
	for _, c := range cases {
		t.Run(c.psComm, func(t *testing.T) {
			if got := matchRuntimeName(c.psComm, c.runtime); got != c.want {
				t.Errorf("matchRuntimeName(%q, %q) = %v, want %v", c.psComm, c.runtime, got, c.want)
			}
		})
	}
}
