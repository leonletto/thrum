package rpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// TestReadIdentitiesAcrossWorktrees_SingleWorktree loads one identity file
// from the primary thrumDir and asserts it's returned.
func TestReadIdentitiesAcrossWorktrees_SingleWorktree(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}

	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "solo_agent", Role: "tester", Module: "unit",
		},
		AgentPID: 12345,
		Runtime:  "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	got := ReadIdentitiesAcrossWorktrees(context.Background(), thrumDir)
	if len(got) != 1 {
		t.Fatalf("got %d identities, want 1", len(got))
	}
	if got["solo_agent"] == nil {
		t.Fatalf("solo_agent not in result")
	}
	if got["solo_agent"].Runtime != "claude" {
		t.Errorf("Runtime = %q, want claude", got["solo_agent"].Runtime)
	}
	if got["solo_agent"].AgentPID != 12345 {
		t.Errorf("AgentPID = %d, want 12345", got["solo_agent"].AgentPID)
	}
}

// TestReadIdentitiesAcrossWorktrees_DivergentDuplicate asserts that when
// the same agent name appears in two dirs, the newer UpdatedAt wins. This
// requires a mockable AllIdentityDirs or a real git-worktree harness —
// skipped as a placeholder since neither is set up for this test.
func TestReadIdentitiesAcrossWorktrees_DivergentDuplicate(t *testing.T) {
	t.Skip("requires AllIdentityDirs injection; see plan Task 10")
}
