package daemon

import (
	"testing"
)

func TestNudgeState_Dedup(t *testing.T) {
	ns := NewNudgeState()

	// First nudge should be allowed
	if !ns.ShouldNudge("session-a", "idle") {
		t.Error("first nudge should be allowed")
	}
	ns.RecordNudge("session-a", "idle")

	// Immediate repeat should be blocked
	if ns.ShouldNudge("session-a", "idle") {
		t.Error("immediate repeat should be blocked")
	}

	// Different reason should be allowed
	if !ns.ShouldNudge("session-a", "permission:Write foo.go") {
		t.Error("different reason should be allowed")
	}

	// Different session should be allowed
	if !ns.ShouldNudge("session-b", "idle") {
		t.Error("different session should be allowed")
	}
}

func TestNudgeState_Clear(t *testing.T) {
	ns := NewNudgeState()
	ns.RecordNudge("session-a", "idle")
	ns.Clear("session-a")

	if !ns.ShouldNudge("session-a", "idle") {
		t.Error("after clear, nudge should be allowed again")
	}
}
