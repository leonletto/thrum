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

func TestChainContains_Empty(t *testing.T) {
	if ChainContains(nil, 1) {
		t.Error("nil chain should not contain anything")
	}
	if ChainContains([]int{}, 1) {
		t.Error("empty chain should not contain anything")
	}
}
