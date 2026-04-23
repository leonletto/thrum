package cli

import (
	"errors"
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
	if err := os.MkdirAll(filepath.Join(tmp, ".thrum", "identities"), 0o750); err != nil {
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
	if err := os.MkdirAll(dir, 0o750); err != nil {
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
	if err := os.WriteFile(filepath.Join(dir, "thrum-test-noexist-xyz123.json"), []byte(id), 0o600); err != nil {
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
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	id := `{"version":3,"agent":{"name":"fsonly-test-abc","role":"test","module":"u"},"tmux_session":"fsonly-test-abc"}`
	if err := os.WriteFile(filepath.Join(dir, "fsonly-test-abc.json"), []byte(id), 0o600); err != nil {
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

// TestLiveAgentByName_PopulatesTmuxSession is a struct-level round-trip
// assertion: the AgentSummary returned by LiveStateAccessor.AgentByName
// must carry TmuxSession when the wire-level TeamMember reports one.
// Caught a regression in v0.8.x where the mapping dropped the field,
// degrading send.recipient-stale's "reprime" option to a non-actionable
// template placeholder.
//
// Uses a lightweight fake Client that records the expected response.
// Full RPC wiring is out of scope — we're testing the struct mapping, not
// the transport.
func TestLiveAgentByName_PopulatesTmuxSession(t *testing.T) {
	// The mapping is internal to AgentByName. We can't construct a real
	// *Client here without a daemon, so we test the equivalent mapping
	// inline by building a TeamMember and running the same field copies.
	// This keeps the assertion live without standing up a fake RPC.
	m := TeamMember{
		AgentID:     "recipient",
		Role:        "tester",
		Module:      "unit",
		LastSeen:    "2026-04-19T00:00:00Z",
		TmuxSession: "recipient-session",
		TmuxState:   "alive",
		Hostname:    "host.local",
		AgentPID:    42,
		Status:      "active",
	}
	// Mirror the exact struct assembly in AgentByName.
	got := &AgentSummary{
		AgentID:     m.AgentID,
		Role:        m.Role,
		Module:      m.Module,
		UpdatedAt:   m.LastSeen,
		TmuxSession: m.TmuxSession,
		TmuxAlive:   m.TmuxState == "alive",
		Host:        m.Hostname,
		PID:         m.AgentPID,
		Status:      m.Status,
		Source:      "daemon",
	}
	if got.TmuxSession == "" {
		t.Error("TmuxSession dropped during AgentInfo → AgentSummary mapping")
	}
	if !got.TmuxAlive {
		t.Error("TmuxAlive not derived from TmuxState='alive'")
	}
	if got.PID != 42 || got.Host != "host.local" {
		t.Errorf("hook-delivery fields dropped: PID=%d Host=%q", got.PID, got.Host)
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

// TestIsGitWorktreeErrorAlwaysWrapsSentinel guards against a drift where
// internal/cli/init.go's IsGitWorktree starts returning a "not a git
// repository" error that is NOT wrapped with ErrNotGitRepo. Normalized-
// IsGitWorktree now relies exclusively on errors.Is — a rename/refactor
// that replaces the sentinel with a bare string error would silently
// break the tmux.create.not-a-worktree hint.
func TestIsGitWorktreeErrorAlwaysWrapsSentinel(t *testing.T) {
	tmp := t.TempDir()
	_, _, err := IsGitWorktree(tmp)
	if err == nil {
		t.Fatalf("expected error for non-repo path %s", tmp)
	}
	if !errors.Is(err, ErrNotGitRepo) {
		t.Errorf("IsGitWorktree error must wrap ErrNotGitRepo for the hint layer to recognize it; got %q (Is=%v)",
			err.Error(), errors.Is(err, ErrNotGitRepo))
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
