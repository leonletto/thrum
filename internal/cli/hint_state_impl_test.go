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

// A non-repo path must return a DEFINITIVE (false, nil) — not a propagated
// "not a git repository" error, since the error IS the answer. This is the
// signal the tmux.create.not-a-worktree hint relies on.
func TestIsGitWorktree_NonRepoPathDefinitiveFalse(t *testing.T) {
	s := &LiveStateAccessor{}
	tmp := t.TempDir()
	ok, err := s.IsGitWorktree(tmp)
	if ok {
		t.Errorf("tmpdir incorrectly classified as worktree")
	}
	if err != nil {
		t.Errorf("non-repo path must return (false, nil) so hint sources can act on it; got err=%v", err)
	}
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

// FSOnlyStateAccessor should handle the same identity-status cases as
// LiveStateAccessor since both delegate to the same FS helper.
func TestFSOnlyAccessor_IdentityStatusMirrorsLive(t *testing.T) {
	tmp := t.TempDir()
	s := NewFSOnlyStateAccessor()

	// No identities dir → IdentityNone.
	status, agent, err := s.IdentityStatus(tmp)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if status != IdentityNone || agent != nil {
		t.Errorf("no-dir: got (%v, %+v), want (IdentityNone, nil)", status, agent)
	}

	// Set up an identity file with a dead session → IdentityStale.
	dir := filepath.Join(tmp, ".thrum", "identities")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := `{"version":3,"agent":{"name":"fsonly-test-abc","role":"test","module":"u"},"tmux_session":"fsonly-test-abc"}`
	if err := os.WriteFile(filepath.Join(dir, "fsonly-test-abc.json"), []byte(id), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	status, agent, err = s.IdentityStatus(tmp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != IdentityStale || agent == nil || agent.AgentID != "fsonly-test-abc" {
		t.Errorf("stale: got (%v, %+v), want (IdentityStale, agent{AgentID=fsonly-test-abc})", status, agent)
	}
}

func TestFSOnlyAccessor_TmuxSessionExistsEmpty(t *testing.T) {
	s := NewFSOnlyStateAccessor()
	if ok, err := s.TmuxSessionExists(""); ok || err != nil {
		t.Errorf("empty name: got (%v, %v), want (false, nil)", ok, err)
	}
}

func TestFSOnlyAccessor_IsGitWorktreeNonRepo(t *testing.T) {
	s := NewFSOnlyStateAccessor()
	tmp := t.TempDir()
	ok, err := s.IsGitWorktree(tmp)
	if ok || err != nil {
		t.Errorf("non-repo: got (%v, %v), want (false, nil)", ok, err)
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
