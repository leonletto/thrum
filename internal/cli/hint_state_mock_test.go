package cli

import (
	"errors"
	"testing"
)

// TestMockStateSatisfiesInterface is a compile-time assertion that MockState
// implements StateAccessor. Kept as a runtime test so it shows up in coverage.
func TestMockStateSatisfiesInterface(t *testing.T) {
	var _ StateAccessor = (*MockState)(nil)
}

func TestMockStateZeroValueSafe(t *testing.T) {
	var m *MockState
	if a, err := m.AgentByName("x"); a != nil || err != nil {
		t.Errorf("nil MockState.AgentByName: got (%+v, %v), want (nil, nil)", a, err)
	}
	if b, err := m.TmuxSessionExists("x"); b || err != nil {
		t.Errorf("nil MockState.TmuxSessionExists: got (%v, %v), want (false, nil)", b, err)
	}
	if b, err := m.IsGitWorktree("/x"); b || err != nil {
		t.Errorf("nil MockState.IsGitWorktree: got (%v, %v), want (false, nil)", b, err)
	}
	if s, a, err := m.IdentityStatus("/x"); s != IdentityNone || a != nil || err != nil {
		t.Errorf("nil MockState.IdentityStatus: got (%v, %+v, %v), want (None, nil, nil)", s, a, err)
	}
}

func TestMockStateErrsPropagate(t *testing.T) {
	want := errors.New("boom")
	m := &MockState{Errs: MockErrs{
		AgentByName:       want,
		TmuxSessionExists: want,
		IsGitWorktree:     want,
		IdentityStatus:    want,
	}}
	if _, err := m.AgentByName("x"); !errors.Is(err, want) {
		t.Errorf("AgentByName error did not propagate")
	}
	if _, err := m.TmuxSessionExists("x"); !errors.Is(err, want) {
		t.Errorf("TmuxSessionExists error did not propagate")
	}
	if _, err := m.IsGitWorktree("/x"); !errors.Is(err, want) {
		t.Errorf("IsGitWorktree error did not propagate")
	}
	if _, _, err := m.IdentityStatus("/x"); !errors.Is(err, want) {
		t.Errorf("IdentityStatus error did not propagate")
	}
}

func TestMockStateReturnsConfiguredValues(t *testing.T) {
	agent := &AgentSummary{AgentID: "foo"}
	m := &MockState{
		Agents:           map[string]*AgentSummary{"foo": agent},
		TmuxSessions:     map[string]bool{"foo": true},
		GitWorktrees:     map[string]bool{"/w": true},
		IdentityStatuses: map[string]MockIdentity{"/w": {Status: IdentityLive, Agent: agent}},
	}
	if got, _ := m.AgentByName("foo"); got != agent {
		t.Errorf("AgentByName: got %+v, want %+v", got, agent)
	}
	if got, _ := m.TmuxSessionExists("foo"); !got {
		t.Errorf("TmuxSessionExists(foo) = false, want true")
	}
	if got, _ := m.IsGitWorktree("/w"); !got {
		t.Errorf("IsGitWorktree(/w) = false, want true")
	}
	if s, a, _ := m.IdentityStatus("/w"); s != IdentityLive || a != agent {
		t.Errorf("IdentityStatus(/w) = (%v, %+v), want (Live, %+v)", s, a, agent)
	}
}
