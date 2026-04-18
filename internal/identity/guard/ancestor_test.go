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
	if chain[len(chain)-1] != 1 {
		t.Errorf("chain[last] = %d, want pid 1 (init)", chain[len(chain)-1])
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
