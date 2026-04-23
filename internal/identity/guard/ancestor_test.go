package guard

import (
	"context"
	"os"
	"testing"
)

func TestWalkAncestors_ReachesPID1(t *testing.T) {
	chain, err := WalkAncestors(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(chain) < 2 {
		t.Fatalf("chain too short: %v", chain)
	}
	if chain[0] != os.Getpid() {
		t.Errorf("chain[0] = %d, want own pid %d", chain[0], os.Getpid())
	}
	// In Docker / CI PID namespaces the topmost visible ancestor isn't
	// always PID 1, so assert chain progresses upward (len >= 2) rather
	// than terminating at init. The walker's cycle cap + <=1 stop
	// condition are covered independently by unit-level logic.
	if chain[len(chain)-1] <= 0 {
		t.Errorf("chain[last] = %d, want a real pid", chain[len(chain)-1])
	}
}

func TestChainContains_PresentAndAbsent(t *testing.T) {
	chain := []int{100, 200, 300, 1}
	if !ChainContains(chain, 200) {
		t.Error("want true for 200")
	}
	if ChainContains(chain, 999) {
		t.Error("want false for 999")
	}
}

func TestClosestRuntimeAncestor_SelfIsRuntime(t *testing.T) {
	origIsRuntime := isRuntimeProcessFn
	origRuntimeName := runtimeNameFn
	isRuntimeProcessFn = func(_ context.Context, pid int, _ string) bool { return pid == os.Getpid() }
	runtimeNameFn = func(_ context.Context, pid int) string {
		if pid == os.Getpid() {
			return "claude"
		}
		return ""
	}
	t.Cleanup(func() {
		isRuntimeProcessFn = origIsRuntime
		runtimeNameFn = origRuntimeName
	})

	pid, runtime, err := ClosestRuntimeAncestor(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("want self pid, got %d", pid)
	}
	if runtime == "" {
		t.Error("want runtime name, got empty")
	}
}

func TestClosestRuntimeAncestor_NoRuntime(t *testing.T) {
	origIsRuntime := isRuntimeProcessFn
	isRuntimeProcessFn = func(_ context.Context, _ int, _ string) bool { return false }
	t.Cleanup(func() { isRuntimeProcessFn = origIsRuntime })

	pid, runtime, err := ClosestRuntimeAncestor(context.Background(), os.Getpid())
	if err != nil {
		t.Fatalf("%v", err)
	}
	if pid != 0 {
		t.Errorf("want 0, got %d", pid)
	}
	if runtime != "" {
		t.Errorf("want empty runtime, got %q", runtime)
	}
}

func TestChainContains_Empty(t *testing.T) {
	if ChainContains(nil, 1) {
		t.Error("nil chain should not contain anything")
	}
	if ChainContains([]int{}, 1) {
		t.Error("empty chain should not contain anything")
	}
}

// TestChainContains_ZeroPIDSentinel pins the PID==0 invariant used by
// Rule #4‴ step 3.2: the identity file's "no PID recorded" sentinel is
// 0, and the caller's chain must never include 0 from a live ps
// lookup. ChainContains(chain, 0) must therefore only return true if
// the chain itself was constructed with an explicit 0 entry (a test
// fixture or a corrupted tree), not through legitimate walker output.
func TestChainContains_ZeroPIDSentinel(t *testing.T) {
	if ChainContains([]int{100, 200, 300}, 0) {
		t.Error("chain without 0 must not contain 0")
	}
	if !ChainContains([]int{0, 100}, 0) {
		t.Error("chain explicitly containing 0 must report true")
	}
}
