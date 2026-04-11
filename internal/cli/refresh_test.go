package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/leonletto/thrum/internal/config"
)

// TestRefreshLocalIdentity_NoRuntime asserts that when FindClaudeAncestor
// returns (0, ""), the refresh still runs through but does not update
// PID/runtime fields. Tmux and branch may still update.
func TestRefreshLocalIdentity_NoRuntime(t *testing.T) {
	tmpDir := t.TempDir()
	thrumDir := filepath.Join(tmpDir, ".thrum")
	if err := os.MkdirAll(filepath.Join(thrumDir, "identities"), 0750); err != nil {
		t.Fatal(err)
	}

	// Isolate: pin THRUM_HOME to the tmp dir so paths.EffectiveRepoPath
	// does not redirect to the real repo, and unset THRUM_NAME so
	// LoadIdentityWithPath does not demand a specific name.
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "")

	// Write an identity file with some existing state.
	idFile := &config.IdentityFile{
		Version: 5,
		Agent: config.AgentConfig{
			Kind: "agent", Name: "test_agent", Role: "tester", Module: "unit",
		},
		AgentPID: 99999,
		Runtime:  "claude",
	}
	if err := config.SaveIdentityFile(thrumDir, idFile); err != nil {
		t.Fatal(err)
	}

	// Swap the detector to return a no-runtime result regardless of the
	// environment the test runs in (including under an actual claude session).
	origDetect := detectAncestor
	detectAncestor = func(_ context.Context) (int, string) { return 0, "" }
	t.Cleanup(func() { detectAncestor = origDetect })

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Fatalf("RefreshLocalIdentity: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	// Assert that PID/runtime fields were NOT marked as changed. Tmux and
	// branch may legitimately update depending on the test environment, so
	// we check only the three fields this test cares about.
	//
	// We cannot re-read via LoadIdentityWithPath here because that loader
	// has a silent PID-adoption side effect when the stored PID is dead
	// (see internal/config/config.go loadIdentityFromDir). The authoritative
	// signal for "refresh did not touch this field" is result.FileChanged.
	for _, f := range result.FileChanged {
		if f == "agent_pid" || f == "runtime" || f == "preferred_runtime" {
			t.Errorf("refresh changed %q unexpectedly when detector returned (0, \"\")", f)
		}
	}
}

// TestRefreshLocalIdentity_NoIdentityFile asserts (nil, nil) when no
// .thrum/identities/ directory exists at repoPath.
func TestRefreshLocalIdentity_NoIdentityFile(t *testing.T) {
	tmpDir := t.TempDir()
	// No .thrum directory created.

	// Pin THRUM_HOME so the load does not redirect to the real repo.
	t.Setenv("THRUM_HOME", tmpDir)
	t.Setenv("THRUM_NAME", "")

	result, err := RefreshLocalIdentity(nil, tmpDir)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
}
