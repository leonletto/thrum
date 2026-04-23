package rpc

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
	"github.com/leonletto/thrum/internal/daemon/state"
)

// TestHandleSetAgentStatus_G4_RefusesDeadPID proves G4 refuses to write
// agent_status onto an identity file whose AgentPID is dead.
func TestHandleSetAgentStatus_G4_RefusesDeadPID(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	idFile := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind: "agent",
			Name: "impl_g4_status",
			Role: "implementer",
		},
		AgentPID:    999999, // dead
		AgentStatus: "idle",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	st, err := state.NewState(thrumDir, thrumDir, "r_G4_STATUS", "")
	if err != nil {
		t.Fatalf("new state: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	h := NewAgentHandler(st)
	params, _ := json.Marshal(map[string]string{"agent": "impl_g4_status", "status": "working"})
	_, err = h.HandleSetAgentStatus(context.Background(), params)
	if err == nil {
		t.Fatal("want error on dead-PID agent, got nil")
	}
	if !strings.Contains(err.Error(), "daemon_writer_liveness") {
		t.Errorf("error should mention daemon_writer_liveness guard, got: %v", err)
	}

	// Verify file was NOT mutated.
	data, _ := os.ReadFile(filepath.Join(thrumDir, "identities", "impl_g4_status.json")) //nolint:gosec // test path
	var got config.IdentityFile
	_ = json.Unmarshal(data, &got)
	if got.AgentStatus != "idle" {
		t.Errorf("AgentStatus = %q, want unchanged idle (G4 should have blocked)", got.AgentStatus)
	}
}
