package cli

// MockState is a configurable StateAccessor for unit tests. It lives in a
// non-_test.go file so test files across `cli` and `cli_test` packages can
// share a single mock definition.
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
