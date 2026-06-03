package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/identity/guard"
)

// readAgentPID reads the agent_pid field back from an identity file written by
// guard.WritePID, so the apply tests assert the real on-disk outcome.
func readAgentPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read identity %s: %v", path, err)
	}
	var idf config.IdentityFile
	if err := json.Unmarshal(b, &idf); err != nil {
		t.Fatalf("unmarshal identity: %v", err)
	}
	return idf.AgentPID
}

// TestDecideQuickstartPID pins the thrum-ipbl PID-aware owner-protecting matrix
// at the pure-decision level.
func TestDecideQuickstartPID(t *testing.T) {
	cases := []struct {
		name               string
		runtimePID         int
		storedPID          int
		storedAliveRuntime bool
		want               quickstartPIDDecision
	}{
		{"live runtime ancestor writes its PID", 4321, 0, false, qpWriteRuntimePID},
		{"runtime ancestor wins even over a live stored owner", 4321, 999, true, qpWriteRuntimePID},
		{"bare shell + live stored owner -> protect", 0, 999, true, qpProtectLiveOwner},
		{"bare shell + dead stored owner -> free slot", 0, 999, false, qpFreeSlot},
		{"bare shell + no stored owner -> free slot", 0, 0, false, qpFreeSlot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideQuickstartPID(tc.runtimePID, tc.storedPID, tc.storedAliveRuntime)
			if got != tc.want {
				t.Fatalf("decideQuickstartPID(%d,%d,%v) = %d, want %d",
					tc.runtimePID, tc.storedPID, tc.storedAliveRuntime, got, tc.want)
			}
		})
	}
}

// TestApplyQuickstartAgentPID_ThreeCases pins the on-disk agent_pid outcome for
// the three thrum-ipbl matrix cases, with injected liveness probes so the test
// is deterministic (no dependency on the real process tree).
func TestApplyQuickstartAgentPID_ThreeCases(t *testing.T) {
	alive := func(int) bool { return true }
	dead := func(int) bool { return false }
	isRuntime := func(int) bool { return true }

	// Case 1: a live runtime ancestor (runtimePID>0) replaces a stale PID — the
	// /login pid-drift recovery.
	t.Run("runtime ancestor writes the live PID", func(t *testing.T) {
		idPath := filepath.Join(t.TempDir(), "a.json")
		if err := guard.WritePID(idPath, 11111); err != nil { // pre-existing stale PID
			t.Fatal(err)
		}
		if err := applyQuickstartAgentPID(idPath, 22222, 11111, alive, isRuntime); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := readAgentPID(t, idPath); got != 22222 {
			t.Fatalf("agent_pid = %d, want 22222 (live runtime PID written)", got)
		}
	})

	// Case 1 (override): a live runtime ancestor must win even when the stored
	// owner PID is ALSO alive — this is the actual /login pid-drift recovery:
	// the new runtime PID replaces the (still-briefly-alive) old one.
	t.Run("runtime ancestor overrides a live stored owner", func(t *testing.T) {
		idPath := filepath.Join(t.TempDir(), "a.json")
		if err := guard.WritePID(idPath, 99999); err != nil { // stored owner, still alive
			t.Fatal(err)
		}
		if err := applyQuickstartAgentPID(idPath, 22222, 99999, alive, isRuntime); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := readAgentPID(t, idPath); got != 22222 {
			t.Fatalf("agent_pid = %d, want 22222 (live runtime ancestor overrides live stored owner)", got)
		}
	})

	// Case 2: bare shell, but a live agent owns the worktree — the file must NOT
	// be rewritten (never clobber the live owner with the terminal PID).
	t.Run("bare shell + live owner -> no rewrite", func(t *testing.T) {
		idPath := filepath.Join(t.TempDir(), "a.json")
		if err := guard.WritePID(idPath, 33333); err != nil {
			t.Fatal(err)
		}
		if err := applyQuickstartAgentPID(idPath, 0, 33333, alive, isRuntime); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := readAgentPID(t, idPath); got != 33333 {
			t.Fatalf("agent_pid = %d, want 33333 (live owner protected, not rewritten)", got)
		}
	})

	// Case 3: bare shell, stored owner dead — free the slot (write 0).
	t.Run("bare shell + dead owner -> zeroed", func(t *testing.T) {
		idPath := filepath.Join(t.TempDir(), "a.json")
		if err := guard.WritePID(idPath, 44444); err != nil {
			t.Fatal(err)
		}
		if err := applyQuickstartAgentPID(idPath, 0, 44444, dead, isRuntime); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got := readAgentPID(t, idPath); got != 0 {
			t.Fatalf("agent_pid = %d, want 0 (dead owner -> slot freed)", got)
		}
	})
}
