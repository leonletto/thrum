package permission

import (
	"context"
	"strings"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// TestScheduler_SetAgentStatus_G4_RefusesDeadPID proves G4 blocks the
// permission scheduler from mutating an identity file whose AgentPID is
// dead — closes the race where the scheduler writes "stuck" onto a
// crashed agent's file after the agent exited.
func TestScheduler_SetAgentStatus_G4_RefusesDeadPID(t *testing.T) {
	p, _ := newSchedulerFixture(t)

	// Rewrite the researcher identity with a definitely-dead PID.
	researcherID := &config.IdentityFile{
		Agent: config.AgentConfig{
			Kind:   "agent",
			Name:   "researcher_cursor",
			Role:   "researcher",
			Module: "cursor-test",
		},
		AgentPID:    999999,
		AgentStatus: "working",
	}
	if err := config.SaveIdentityFile(p.thrumDir, researcherID); err != nil {
		t.Fatalf("save identity: %v", err)
	}

	err := p.setAgentStatus(context.Background(), "researcher_cursor", "stuck", "")
	if err == nil {
		t.Fatal("want error on dead-PID agent, got nil")
	}
	if !strings.Contains(err.Error(), "daemon_writer_liveness") {
		t.Errorf("error should mention daemon_writer_liveness guard, got: %v", err)
	}

	// Verify status was not mutated.
	got := readIdentityFile(t, p.thrumDir, "researcher_cursor")
	if got.AgentStatus != "working" {
		t.Errorf("AgentStatus = %q, want unchanged working (G4 should have blocked)", got.AgentStatus)
	}
}
