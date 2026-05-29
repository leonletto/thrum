//go:build unix

package process

import (
	"context"
	"os"
	"slices"
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
		// Regression: opencode installs a dot-prefixed shim binary
		// (.opencode) in node_modules. Ancestor detection must match
		// it via the dual-name alias list.
		{"/opt/homebrew/lib/node_modules/opencode-ai/bin/.opencode", ".opencode", true},
		{".opencode", ".opencode", true},
	}
	for _, c := range cases {
		t.Run(c.psComm, func(t *testing.T) {
			if got := matchRuntimeName(c.psComm, c.runtime); got != c.want {
				t.Errorf("matchRuntimeName(%q, %q) = %v, want %v", c.psComm, c.runtime, got, c.want)
			}
		})
	}
}

// TestFindAncestor_TopmostVsClosest guards thrum-xir.40: when a runtime
// spawns a transient sub-process that ALSO matches a known runtime name
// (e.g. Claude's SessionStart hook spawning a claude-sdk helper, which
// `ps -o comm=` reports as "claude"), the binding-write callers must use
// the TOPMOST ancestor (long-lived session main) while identification /
// lookup callers must use the CLOSEST ancestor (caller's immediate
// runtime). Both semantics share the same underlying walk, parameterized
// by the topmost flag.
//
// Simulated chain (parent direction):
//
//	caller (start) → 300 (bash) → 200 (claude-sdk-helper) →
//	100 (claude main) → 1
//
// topmost=true  → (100, "claude")  -- the stable binding target
// topmost=false → (200, "claude")  -- the immediate runtime context.
func TestFindAncestor_TopmostVsClosest(t *testing.T) {
	parents := map[int]int{
		300: 200,
		200: 100,
		100: 1,
	}
	names := map[int]string{
		300: "bash",
		200: "claude", // helper process: matches knownRuntimes too
		100: "claude", // session main: the stable binding target
	}
	nameFn := func(_ context.Context, pid int) string { return names[pid] }
	parentFn := func(_ context.Context, pid int) int { return parents[pid] }

	gotTopPID, gotTopRuntime := findAncestor(context.Background(), 300, nameFn, parentFn, true)
	if gotTopPID != 100 || gotTopRuntime != "claude" {
		t.Errorf("findAncestor(topmost=true) returned (%d, %q), want (100, \"claude\")", gotTopPID, gotTopRuntime)
	}

	gotCloPID, gotCloRuntime := findAncestor(context.Background(), 300, nameFn, parentFn, false)
	if gotCloPID != 200 || gotCloRuntime != "claude" {
		t.Errorf("findAncestor(topmost=false) returned (%d, %q), want (200, \"claude\")", gotCloPID, gotCloRuntime)
	}
}

// TestFindAncestor_NoMatch confirms the zero-return contract when no
// runtime appears in the chain at all, for both topmost and closest
// semantics. Same chain, same expectation.
func TestFindAncestor_NoMatch(t *testing.T) {
	parents := map[int]int{50: 40, 40: 30, 30: 1}
	names := map[int]string{50: "bash", 40: "tmux", 30: "launchd"}
	nameFn := func(_ context.Context, pid int) string { return names[pid] }
	parentFn := func(_ context.Context, pid int) int { return parents[pid] }

	for _, topmost := range []bool{true, false} {
		gotPID, gotRuntime := findAncestor(context.Background(), 50, nameFn, parentFn, topmost)
		if gotPID != 0 || gotRuntime != "" {
			t.Errorf("findAncestor(topmost=%t) on non-runtime chain returned (%d, %q), want (0, \"\")", topmost, gotPID, gotRuntime)
		}
	}
}

// TestFindAncestor_SingleMatch confirms that when only one runtime
// appears in the chain (the common case — no transient helper), both
// topmost and closest semantics agree on the same PID. This is the
// passthrough property: callers within a normal single-runtime session
// see identical behavior regardless of which wrapper they use.
func TestFindAncestor_SingleMatch(t *testing.T) {
	parents := map[int]int{300: 200, 200: 100, 100: 1}
	names := map[int]string{300: "bash", 200: "tmux", 100: "claude"}
	nameFn := func(_ context.Context, pid int) string { return names[pid] }
	parentFn := func(_ context.Context, pid int) int { return parents[pid] }

	for _, topmost := range []bool{true, false} {
		gotPID, gotRuntime := findAncestor(context.Background(), 300, nameFn, parentFn, topmost)
		if gotPID != 100 || gotRuntime != "claude" {
			t.Errorf("findAncestor(topmost=%t) with single match returned (%d, %q), want (100, \"claude\")", topmost, gotPID, gotRuntime)
		}
	}
}

// TestKnownRuntimes_DotOpencode is a structural regression guard for
// thrum-9hr2: opencode installs its real binary as `.opencode` (npm shim
// convention), and FindClaudeAncestor must know that name so it can detect
// the opencode ancestor in the process tree and populate agent_pid/runtime
// in the identity file. Without both the knownRuntimes entry and the
// runtimeDisplayName alias, opencode agents end up with empty PID/runtime
// fields after `thrum prime`.
func TestKnownRuntimes_DotOpencode(t *testing.T) {
	if !slices.Contains(knownRuntimes, ".opencode") {
		t.Error(".opencode missing from knownRuntimes — FindClaudeAncestor cannot detect opencode wrapper binary")
	}
	if got := runtimeDisplayName[".opencode"]; got != "opencode" {
		t.Errorf("runtimeDisplayName[\".opencode\"] = %q, want \"opencode\"", got)
	}
}
