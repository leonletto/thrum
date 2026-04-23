package cli

import (
	"errors"
	"testing"
)

// MockState is a configurable StateAccessor for unit tests. It lives in a
// _test.go file so it never ships in the production binary.
//
// Zero value is safe to use — all maps are nil-read-friendly (reading from
// a nil map returns the zero value). Errs drives error-path coverage when
// set per-method.
type MockState struct {
	Agents           map[string]*AgentSummary
	TmuxSessions     map[string]bool
	GitWorktrees     map[string]bool
	IdentityStatuses map[string]MockIdentity
	Errs             MockErrs
}

// MockIdentity bundles an IdentityStatus with the agent the mock should
// return alongside it. Used as the value type of MockState.IdentityStatuses.
type MockIdentity struct {
	Status IdentityStatus
	Agent  *AgentSummary
}

// MockErrs lets a test force any single StateAccessor method to return an
// error without affecting the others. Used for best-effort error-path tests.
type MockErrs struct {
	AgentByName       error
	TmuxSessionExists error
	IsGitWorktree     error
	IdentityStatus    error
}

func (m *MockState) AgentByName(name string) (*AgentSummary, error) {
	if m == nil {
		return nil, nil
	}
	if m.Errs.AgentByName != nil {
		return nil, m.Errs.AgentByName
	}
	return m.Agents[name], nil
}

func (m *MockState) TmuxSessionExists(name string) (bool, error) {
	if m == nil {
		return false, nil
	}
	if m.Errs.TmuxSessionExists != nil {
		return false, m.Errs.TmuxSessionExists
	}
	return m.TmuxSessions[name], nil
}

func (m *MockState) IsGitWorktree(path string) (bool, error) {
	if m == nil {
		return false, nil
	}
	if m.Errs.IsGitWorktree != nil {
		return false, m.Errs.IsGitWorktree
	}
	return m.GitWorktrees[path], nil
}

func (m *MockState) IdentityStatus(path string) (IdentityStatus, *AgentSummary, error) {
	if m == nil {
		return IdentityNone, nil, nil
	}
	if m.Errs.IdentityStatus != nil {
		return IdentityNone, nil, m.Errs.IdentityStatus
	}
	mi := m.IdentityStatuses[path]
	return mi.Status, mi.Agent, nil
}

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
