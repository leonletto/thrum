package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLiveStateAccessorSatisfiesInterface is a compile-time assertion that
// LiveStateAccessor and FSOnlyStateAccessor both implement StateAccessor.
func TestLiveStateAccessorSatisfiesInterface(t *testing.T) {
	var _ StateAccessor = (*LiveStateAccessor)(nil)
	var _ StateAccessor = (*FSOnlyStateAccessor)(nil)
}

// TestIdentityStatus_NoIdentitiesDir — path without .thrum/identities → IdentityNone.
func TestIdentityStatus_NoIdentitiesDir(t *testing.T) {
	tmp := t.TempDir()
	s := &LiveStateAccessor{}
	status, agent, err := s.IdentityStatus(tmp)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if status != IdentityNone {
		t.Errorf("status = %v, want IdentityNone", status)
	}
	if agent != nil {
		t.Errorf("agent = %+v, want nil", agent)
	}
}

// TestIdentityStatus_EmptyIdentitiesDir — dir exists but no .json files → IdentityNone.
func TestIdentityStatus_EmptyIdentitiesDir(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".thrum", "identities"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	s := &LiveStateAccessor{}
	status, _, err := s.IdentityStatus(tmp)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if status != IdentityNone {
		t.Errorf("status = %v, want IdentityNone", status)
	}
}

// TestIdentityStatus_IdentityPresentButSessionDead — identity file exists, tmux
// session we encode is very unlikely to exist → IdentityStale.
func TestIdentityStatus_IdentityPresentButSessionDead(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, ".thrum", "identities")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Identity file with a session name that should NOT exist on the host.
	// Use a random-ish suffix to avoid colliding with real sessions.
	id := `{
  "version": 3,
  "agent": {
    "name": "thrum-test-noexist-xyz123",
    "role": "test",
    "module": "unit"
  },
  "tmux_session": "thrum-test-noexist-xyz123"
}`
	if err := os.WriteFile(filepath.Join(dir, "thrum-test-noexist-xyz123.json"), []byte(id), 0o644); err != nil {
		t.Fatalf("write identity: %v", err)
	}

	s := &LiveStateAccessor{}
	status, agent, err := s.IdentityStatus(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != IdentityStale {
		t.Errorf("status = %v, want IdentityStale (no live tmux session)", status)
	}
	if agent == nil || agent.AgentID != "thrum-test-noexist-xyz123" {
		t.Errorf("agent mismatch: %+v", agent)
	}
}

func TestIsGitWorktree_NonRepoPath(t *testing.T) {
	s := &LiveStateAccessor{}
	tmp := t.TempDir()
	ok, err := s.IsGitWorktree(tmp)
	if ok {
		t.Errorf("tmpdir incorrectly classified as worktree")
	}
	// err may be non-nil (not a git repo); that's fine — the hint source
	// treats it as "unknowable" and stays silent. We just assert the bool.
	_ = err
}

func TestTmuxSessionExists_Empty(t *testing.T) {
	s := &LiveStateAccessor{}
	ok, err := s.TmuxSessionExists("")
	if err != nil || ok {
		t.Errorf("empty name must return (false, nil), got (%v, %v)", ok, err)
	}
}

func TestFSOnlyAccessor_AgentByNameReturnsNil(t *testing.T) {
	s := NewFSOnlyStateAccessor()
	agent, err := s.AgentByName("anything")
	if agent != nil || err != nil {
		t.Errorf("FSOnlyStateAccessor.AgentByName must return (nil, nil), got (%+v, %v)", agent, err)
	}
}

func TestNewLiveStateAccessor_NilClientSafe(t *testing.T) {
	// A live accessor created with a nil client should at minimum not panic
	// on AgentByName; it returns (nil, nil) per the best-effort contract.
	s := NewLiveStateAccessor(nil)
	agent, err := s.AgentByName("x")
	if agent != nil || err != nil {
		t.Errorf("nil-client accessor: got (%+v, %v), want (nil, nil)", agent, err)
	}
}
